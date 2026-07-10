//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to let users talk to a trpc-agent-go agent
// over Telegram using the official Bot API.
//
// Flow (long polling — no public URL or webhook needed):
//
//	User (Telegram) --> Bot API --> getUpdates (this process)
//	                                     |
//	                                     v
//	                            runner.Run(agent) -- tools --> answer
//	                                     |
//	                                     v
//	                          sendMessage --> User (Telegram)
//
// Telegram bots cannot start a conversation: the user must open the bot and
// press Start (or send any message) first. After that the bot can reply and
// even push messages proactively — there is no 24-hour window like WhatsApp.
//
// Required environment variables:
//
//	OPENAI_API_KEY       API key for the model provider.
//	OPENAI_BASE_URL      Base URL for the model provider (optional for OpenAI).
//	TELEGRAM_BOT_TOKEN   Bot token from @BotFather (looks like 123456:ABC-...).
//
// Optional environment variables:
//
//	TELEGRAM_API_BASE    Bot API base URL (default https://api.telegram.org).
//	                     Useful for a self-hosted Bot API server or tests.
//
// Usage:
//
//	go run . -model deepseek-v4-flash
//
// Then open your bot in Telegram, press Start, and chat with the agent.
package main

import (
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
	appName   = "telegram-agent"
	agentName = "telegram-assistant"

	defaultAPIBase = "https://api.telegram.org"

	// maxSegmentLen keeps each outbound message within Telegram's 4096-char limit.
	maxSegmentLen = 4000

	// pollTimeout is the long-poll wait passed to getUpdates.
	pollTimeout = 50 * time.Second

	// agentTimeout bounds a single agent run so a stuck run cannot leak.
	agentTimeout = 90 * time.Second
)

var modelName = flag.String("model", "deepseek-v4-flash", "Model name to use")

func main() {
	flag.Parse()

	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("missing required env: TELEGRAM_BOT_TOKEN")
	}
	apiBase := os.Getenv("TELEGRAM_API_BASE")
	if apiBase == "" {
		apiBase = defaultAPIBase
	}

	bot := &bot{
		runner:  buildRunner(*modelName),
		baseURL: strings.TrimRight(apiBase, "/") + "/bot" + token,
		// Client timeout must exceed the long-poll timeout.
		httpClient: &http.Client{Timeout: pollTimeout + 15*time.Second},
	}
	defer bot.runner.Close()

	log.Printf("🟢 Telegram agent polling for updates (model=%s)", *modelName)
	bot.pollLoop()
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
		llmagent.WithDescription("A customer-support assistant reachable over Telegram."),
		llmagent.WithInstruction(
			"You are a helpful customer-support assistant talking to a customer on "+
				"Telegram. Keep replies concise and friendly. Use the order_status "+
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
func (b *bot) runAgent(userID, sessionID, text string) (string, error) {
	ctx, cancel := newAgentContext()
	defer cancel()

	eventChan, err := b.runner.Run(ctx, userID, sessionID, model.NewUserMessage(text))
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
