//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to let users talk to a trpc-agent-go agent
// in Slack using the Events API and Web API.
//
// Flow:
//
//	User (Slack DM or @mention) --> Slack --> POST /slack/events (this server)
//	                                              |
//	                                              v
//	                                     runner.Run(agent) -- tools --> answer
//	                                              |
//	                                              v
//	                              chat.postMessage --> User (Slack)
//
// Slack requires event deliveries to be acknowledged within 3 seconds, so the
// handler returns 200 immediately and processes the agent run asynchronously,
// replying via chat.postMessage (in-thread for channel @mentions).
//
// Required environment variables:
//
//	OPENAI_API_KEY         API key for the model provider.
//	OPENAI_BASE_URL        Base URL for the model provider (optional for OpenAI).
//	SLACK_BOT_TOKEN        Bot token (xoxb-...), scopes: chat:write, im:history,
//	                       app_mentions:read (+ im:write to open DMs).
//	SLACK_SIGNING_SECRET   App signing secret (verifies X-Slack-Signature).
//
// Optional environment variables:
//
//	SLACK_API_BASE         Web API base (default https://slack.com/api). For tests.
//
// Usage:
//
//	go run . -model deepseek-v4-flash -addr :8080
//
// Expose the port (e.g. `ngrok http 8080`), then in api.slack.com -> your app
// -> Event Subscriptions: enable, set the request URL to
// https://xxxx.ngrok.io/slack/events, and subscribe to the bot events
// "message.im" and "app_mention". Install the app to your workspace.
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
	appName   = "slack-agent"
	agentName = "slack-assistant"

	defaultAPIBase = "https://slack.com/api"

	// maxSegmentLen keeps outbound messages within Slack's 4000-char limit.
	maxSegmentLen = 3900

	// signatureMaxAge rejects replayed requests older than this.
	signatureMaxAge = 5 * time.Minute

	// agentTimeout bounds a single agent run so a stuck run cannot leak.
	agentTimeout = 90 * time.Second
)

var (
	modelName   = flag.String("model", "deepseek-v4-flash", "Model name to use")
	addr        = flag.String("addr", ":8080", "HTTP listen address")
	webhookPath = flag.String("path", "/slack/events", "Events API request path")
)

func main() {
	flag.Parse()

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	srv := &server{
		runner:        buildRunner(*modelName),
		botToken:      cfg.botToken,
		signingSecret: cfg.signingSecret,
		apiBase:       cfg.apiBase,
		httpClient:    &http.Client{Timeout: 15 * time.Second},
	}
	defer srv.runner.Close()

	mux := http.NewServeMux()
	mux.HandleFunc(*webhookPath, srv.handleEvents)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("🟢 Slack agent listening on %s (events path %s)", *addr, *webhookPath)
	log.Printf("   Point Slack Event Subscriptions at: <public-url>%s", *webhookPath)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// config holds the Slack-related runtime configuration.
type config struct {
	botToken      string
	signingSecret string
	apiBase       string
}

// loadConfig reads and validates the required environment variables.
func loadConfig() (config, error) {
	cfg := config{
		botToken:      os.Getenv("SLACK_BOT_TOKEN"),
		signingSecret: os.Getenv("SLACK_SIGNING_SECRET"),
		apiBase:       os.Getenv("SLACK_API_BASE"),
	}
	var missing []string
	if cfg.botToken == "" {
		missing = append(missing, "SLACK_BOT_TOKEN")
	}
	if cfg.signingSecret == "" {
		missing = append(missing, "SLACK_SIGNING_SECRET")
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
		llmagent.WithDescription("A support assistant reachable in Slack."),
		llmagent.WithInstruction(
			"You are a helpful assistant in a Slack workspace. Keep replies "+
				"concise; use Slack-friendly plain text. Use the order_status tool "+
				"to look up orders and current_time for time questions. If you "+
				"cannot help, say so politely.",
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
