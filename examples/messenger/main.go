//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to let customers talk to a trpc-agent-go agent
// over Facebook Messenger using the official Messenger Platform (Graph API).
//
// Flow:
//
//	Customer (Messenger) --> Meta --> POST webhook (this server)
//	                                       |
//	                                       v
//	                              runner.Run(agent) -- tools --> answer
//	                                       |
//	                                       v
//	                       Graph API Send API --> Customer (Messenger)
//
// Meta requires webhooks to be acknowledged quickly, so the handler returns
// 200 immediately and processes the agent run asynchronously, sending the
// reply through the Send API once it is ready.
//
// Required environment variables:
//
//	OPENAI_API_KEY          API key for the model provider.
//	OPENAI_BASE_URL         Base URL for the model provider (optional for OpenAI).
//	MESSENGER_PAGE_TOKEN    Page access token for the Facebook Page.
//	MESSENGER_APP_SECRET    Meta app secret (verifies X-Hub-Signature-256).
//	MESSENGER_VERIFY_TOKEN  Your own string; must match the webhook config.
//
// Optional environment variables:
//
//	MESSENGER_API_BASE      Graph API base (default https://graph.facebook.com/v23.0).
//	                        Useful for tests.
//
// Usage:
//
//	go run . -model deepseek-v4-flash -addr :8080
//
// Expose the port publicly (e.g. `ngrok http 8080`) and configure the webhook
// in Meta App Dashboard -> Messenger -> Settings: callback URL
// https://xxxx.ngrok.io/messenger, your verify token, and subscribe the Page
// to the "messages" field.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	appName   = "messenger-agent"
	agentName = "messenger-assistant"

	defaultAPIBase = "https://graph.facebook.com/v23.0"

	// maxSegmentLen keeps outbound messages within Messenger's 2000-char limit.
	maxSegmentLen = 1900

	// agentTimeout bounds a single agent run so a stuck run cannot leak.
	agentTimeout = 90 * time.Second
)

var (
	modelName   = flag.String("model", "deepseek-v4-flash", "Model name to use")
	addr        = flag.String("addr", ":8080", "HTTP listen address")
	webhookPath = flag.String("path", "/messenger", "Webhook path")
)

func main() {
	flag.Parse()

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	srv := &server{
		runner:      buildRunner(*modelName),
		pageToken:   cfg.pageToken,
		appSecret:   cfg.appSecret,
		verifyToken: cfg.verifyToken,
		apiBase:     cfg.apiBase,
		httpClient:  &http.Client{Timeout: 15 * time.Second},
	}
	defer srv.runner.Close()

	mux := http.NewServeMux()
	mux.HandleFunc(*webhookPath, srv.handleWebhook)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("🟢 Messenger agent listening on %s (webhook path %s)", *addr, *webhookPath)
	log.Printf("   Configure Meta webhook at: <public-url>%s", *webhookPath)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// config holds the Messenger-related runtime configuration.
type config struct {
	pageToken   string
	appSecret   string
	verifyToken string
	apiBase     string
}

// loadConfig reads and validates the required environment variables.
func loadConfig() (config, error) {
	cfg := config{
		pageToken:   os.Getenv("MESSENGER_PAGE_TOKEN"),
		appSecret:   os.Getenv("MESSENGER_APP_SECRET"),
		verifyToken: os.Getenv("MESSENGER_VERIFY_TOKEN"),
		apiBase:     os.Getenv("MESSENGER_API_BASE"),
	}
	var missing []string
	if cfg.pageToken == "" {
		missing = append(missing, "MESSENGER_PAGE_TOKEN")
	}
	if cfg.appSecret == "" {
		missing = append(missing, "MESSENGER_APP_SECRET")
	}
	if cfg.verifyToken == "" {
		missing = append(missing, "MESSENGER_VERIFY_TOKEN")
	}
	if len(missing) > 0 {
		return cfg, fmt.Errorf("missing required env: %s", strings.Join(missing, ", "))
	}
	if cfg.apiBase == "" {
		cfg.apiBase = defaultAPIBase
	}
	cfg.apiBase = strings.TrimRight(cfg.apiBase, "/")
	return cfg, nil
}

// buildRunner wires an LLM agent (with demo tools) into a Runner.
func buildRunner(name string) runner.Runner {
	modelInstance := openai.New(name)

	orderTool := function.NewFunctionTool(
		lookupOrderStatus,
		function.WithName("order_status"),
		function.WithDescription("Look up the status of a customer order by its order ID."),
	)
	timeTool := function.NewFunctionTool(
		currentTime,
		function.WithName("current_time"),
		function.WithDescription("Get the current date and time for a timezone."),
	)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1024),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}

	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A customer-support assistant reachable over Facebook Messenger."),
		llmagent.WithInstruction(
			"You are a helpful customer-support assistant talking to a customer on "+
				"Facebook Messenger. Keep replies concise and friendly. Use the "+
				"order_status tool to look up orders and current_time for time "+
				"questions. If you cannot help, say so politely.",
		),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{orderTool, timeTool}),
	)

	return runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
}

// runAgent runs the agent for one user message and returns the aggregated
// assistant reply text.
func (s *server) runAgent(ctx context.Context, userID, sessionID, text string) (string, error) {
	eventChan, err := s.runner.Run(ctx, userID, sessionID, model.NewUserMessage(text))
	if err != nil {
		return "", fmt.Errorf("run agent: %w", err)
	}

	var reply strings.Builder
	for evt := range eventChan {
		if evt.Error != nil {
			return "", fmt.Errorf("agent error: %s", evt.Error.Message)
		}
		if evt.Response != nil {
			for _, choice := range evt.Response.Choices {
				if choice.Delta.Content != "" {
					reply.WriteString(choice.Delta.Content)
				}
			}
		}
		if evt.IsFinalResponse() {
			break
		}
	}
	return strings.TrimSpace(reply.String()), nil
}

func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }
