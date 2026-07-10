//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// bot holds the runner and Telegram Bot API transport state.
type bot struct {
	runner     runner.Runner
	baseURL    string // https://api.telegram.org/bot<token>
	httpClient *http.Client
	offset     int64 // next update_id to request
}

// update mirrors the subset of the Telegram Update object we consume.
type update struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		From *struct {
			ID    int64 `json:"id"`
			IsBot bool  `json:"is_bot"`
		} `json:"from"`
		Text string `json:"text"`
	} `json:"message"`
}

// apiResponse is the generic Bot API envelope.
type apiResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

// pollLoop runs the long-polling loop forever, dispatching each text message
// to the agent in its own goroutine.
func (b *bot) pollLoop() {
	for {
		updates, err := b.getUpdates()
		if err != nil {
			log.Printf("⚠️  getUpdates: %v (retrying in 3s)", err)
			time.Sleep(3 * time.Second)
			continue
		}
		for _, u := range updates {
			if u.UpdateID >= b.offset {
				b.offset = u.UpdateID + 1
			}
			msg := u.Message
			if msg == nil || msg.From == nil || msg.From.IsBot {
				continue
			}
			text := strings.TrimSpace(msg.Text)
			chatID := msg.Chat.ID
			log.Printf("📥 chat=%d text=%q", chatID, text)
			go b.process(chatID, text)
		}
	}
}

// getUpdates long-polls the Bot API for new updates.
func (b *bot) getUpdates() ([]update, error) {
	q := fmt.Sprintf(
		"%s/getUpdates?timeout=%d&offset=%d&allowed_updates=%s",
		b.baseURL, int(pollTimeout.Seconds()), b.offset, `["message"]`,
	)
	resp, err := b.httpClient.Get(q)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var env apiResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode envelope: %w", err)
	}
	if !env.OK {
		return nil, fmt.Errorf("bot api: %s", env.Description)
	}
	var updates []update
	if err := json.Unmarshal(env.Result, &updates); err != nil {
		return nil, fmt.Errorf("decode updates: %w", err)
	}
	return updates, nil
}

// process runs the agent for one inbound message and sends the reply back.
func (b *bot) process(chatID int64, text string) {
	if text == "" {
		b.reply(chatID, "I can only handle text messages right now. Please send text. 🙂")
		return
	}
	if text == "/start" {
		b.reply(chatID, "Hi! I'm your support assistant. Ask me anything — for example: "+
			"\"What's the status of order A1001?\"")
		return
	}

	// Use the chat id as both user and session id so the conversation keeps
	// context across messages.
	id := strconv.FormatInt(chatID, 10)
	reply, err := b.runAgent(id, id, text)
	if err != nil {
		log.Printf("❌ agent run failed: %v", err)
		b.reply(chatID, "Sorry, something went wrong on our side. Please try again.")
		return
	}
	if reply == "" {
		reply = "(no response)"
	}
	b.reply(chatID, reply)
}

// reply sends text back to the chat, logging any error.
func (b *bot) reply(chatID int64, text string) {
	if err := b.sendMessage(chatID, text); err != nil {
		log.Printf("❌ send failed: %v", err)
		return
	}
	log.Printf("📤 chat=%d len=%d", chatID, len(text))
}

// sendMessage posts an outbound message, splitting long text into segments
// within Telegram's 4096-character message limit.
func (b *bot) sendMessage(chatID int64, text string) error {
	for _, segment := range splitMessage(text, maxSegmentLen) {
		payload, err := json.Marshal(map[string]any{
			"chat_id": chatID,
			"text":    segment,
		})
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		req, err := http.NewRequestWithContext(
			ctx, http.MethodPost, b.baseURL+"/sendMessage", bytes.NewReader(payload))
		if err != nil {
			cancel()
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := b.httpClient.Do(req)
		cancel()
		if err != nil {
			return fmt.Errorf("do request: %w", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var env apiResponse
		if err := json.Unmarshal(body, &env); err != nil || !env.OK {
			return fmt.Errorf("bot api status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
	return nil
}

// newAgentContext returns the bounded context used for one agent run.
func newAgentContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), agentTimeout)
}

// splitMessage splits text into chunks no longer than max runes, preferring to
// break on newlines or spaces so words are not cut mid-way.
func splitMessage(text string, max int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{""}
	}
	runes := []rune(text)
	if len(runes) <= max {
		return []string{text}
	}
	var segments []string
	for len(runes) > 0 {
		if len(runes) <= max {
			segments = append(segments, string(runes))
			break
		}
		cut := max
		for i := max; i > max/2; i-- {
			if runes[i] == '\n' || runes[i] == ' ' {
				cut = i
				break
			}
		}
		segments = append(segments, strings.TrimSpace(string(runes[:cut])))
		runes = runes[cut:]
	}
	return segments
}
