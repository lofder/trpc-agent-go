//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to let customers talk to a trpc-agent-go agent
// over WhatsApp using Twilio as the WhatsApp Business Solution Provider (BSP).
//
// Flow:
//
//	Customer (WhatsApp) --> Twilio --> POST webhook (this server)
//	                                       |
//	                                       v
//	                              runner.Run(agent) -- tools --> answer
//	                                       |
//	                                       v
//	                        Twilio REST API --> Customer (WhatsApp)
//
// Because an LLM turn can take longer than Twilio's webhook timeout, the
// handler immediately acknowledges the webhook with an empty TwiML response and
// processes the agent run asynchronously, sending the reply back through the
// Twilio REST API once it is ready.
//
// Required environment variables:
//
//	OPENAI_API_KEY        API key for the model provider.
//	OPENAI_BASE_URL       Base URL for the model provider (optional for OpenAI).
//	TWILIO_ACCOUNT_SID    Twilio Account SID (starts with "AC...").
//	TWILIO_AUTH_TOKEN     Twilio Auth Token (secret, never commit it).
//	TWILIO_WHATSAPP_FROM  Sender number, e.g. "whatsapp:+14155238886" (sandbox).
//
// Optional environment variables:
//
//	TWILIO_PUBLIC_URL     Exact public webhook URL configured in the Twilio
//	                      console (e.g. https://xxxx.ngrok.io/whatsapp). Used to
//	                      validate the X-Twilio-Signature. When empty the URL is
//	                      reconstructed from request headers.
//	TWILIO_VALIDATE_SIGNATURE  Set to "false" to skip signature validation while
//	                      testing (default: validate when an auth token is set).
//
// Usage:
//
//	go run . -model deepseek-v4-flash -addr :8080
//
// Then expose the port publicly (e.g. `ngrok http 8080`) and paste the HTTPS
// URL + path into Twilio Console -> Messaging -> Try it out -> Send a WhatsApp
// message -> Sandbox settings -> "When a message comes in".
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
	appName   = "whatsapp-agent"
	agentName = "whatsapp-assistant"

	// twilioAPIBase is the Twilio REST API base for sending messages.
	twilioAPIBase = "https://api.twilio.com/2010-04-01"

	// maxSegmentLen keeps each outbound WhatsApp message within Twilio's limit.
	maxSegmentLen = 1500

	// agentTimeout bounds a single agent run so a stuck run cannot leak.
	agentTimeout = 90 * time.Second
)

var (
	modelName   = flag.String("model", "deepseek-v4-flash", "Model name to use")
	addr        = flag.String("addr", ":8080", "HTTP listen address")
	webhookPath = flag.String("path", "/whatsapp", "Inbound webhook path")
)

func main() {
	flag.Parse()

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	srv := &server{
		runner:            buildRunner(*modelName),
		accountSID:        cfg.accountSID,
		authToken:         cfg.authToken,
		fromNumber:        cfg.fromNumber,
		publicURL:         cfg.publicURL,
		validateSignature: cfg.validateSignature,
		httpClient:        &http.Client{Timeout: 15 * time.Second},
	}
	defer srv.runner.Close()

	mux := http.NewServeMux()
	mux.HandleFunc(*webhookPath, srv.handleInbound)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("🟢 WhatsApp agent listening on %s (webhook path %s)", *addr, *webhookPath)
	log.Printf("   From: %s | signature validation: %t", cfg.fromNumber, cfg.validateSignature)
	log.Printf("   Point Twilio 'When a message comes in' at: <public-url>%s", *webhookPath)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// config holds the Twilio-related runtime configuration.
type config struct {
	accountSID        string
	authToken         string
	fromNumber        string
	publicURL         string
	validateSignature bool
}

// loadConfig reads and validates the required environment variables.
func loadConfig() (config, error) {
	cfg := config{
		accountSID: os.Getenv("TWILIO_ACCOUNT_SID"),
		authToken:  os.Getenv("TWILIO_AUTH_TOKEN"),
		fromNumber: os.Getenv("TWILIO_WHATSAPP_FROM"),
		publicURL:  os.Getenv("TWILIO_PUBLIC_URL"),
	}
	var missing []string
	if cfg.accountSID == "" {
		missing = append(missing, "TWILIO_ACCOUNT_SID")
	}
	if cfg.authToken == "" {
		missing = append(missing, "TWILIO_AUTH_TOKEN")
	}
	if cfg.fromNumber == "" {
		missing = append(missing, "TWILIO_WHATSAPP_FROM")
	}
	if len(missing) > 0 {
		return cfg, fmt.Errorf("missing required env: %s", strings.Join(missing, ", "))
	}
	// Normalize: allow the sandbox number to be provided with or without the
	// "whatsapp:" prefix.
	if !strings.HasPrefix(cfg.fromNumber, "whatsapp:") {
		cfg.fromNumber = "whatsapp:" + cfg.fromNumber
	}
	// Validate the signature by default; allow opting out for quick tests.
	cfg.validateSignature = true
	if v := strings.ToLower(os.Getenv("TWILIO_VALIDATE_SIGNATURE")); v == "false" || v == "0" || v == "no" {
		cfg.validateSignature = false
	}
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
		llmagent.WithDescription("A customer-support assistant reachable over WhatsApp."),
		llmagent.WithInstruction(
			"You are a helpful customer-support assistant talking to a customer on "+
				"WhatsApp. Keep replies concise and friendly. Use the order_status "+
				"tool to look up orders and current_time for time questions. If you "+
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
		// Accumulate streamed assistant text; tool call / tool response events
		// carry no Delta content so they are naturally skipped.
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
