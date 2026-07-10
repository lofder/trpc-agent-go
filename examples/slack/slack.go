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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// server holds the runner and Slack transport configuration.
type server struct {
	runner        runner.Runner
	botToken      string
	signingSecret string
	apiBase       string // e.g. https://slack.com/api
	httpClient    *http.Client
}

// eventsPayload mirrors the subset of Events API payloads we consume.
type eventsPayload struct {
	Type      string `json:"type"`      // url_verification | event_callback
	Challenge string `json:"challenge"` // for url_verification
	Event     struct {
		Type     string `json:"type"` // message | app_mention
		Subtype  string `json:"subtype"`
		User     string `json:"user"`
		BotID    string `json:"bot_id"`
		Text     string `json:"text"`
		Channel  string `json:"channel"`
		TS       string `json:"ts"`
		ThreadTS string `json:"thread_ts"`
	} `json:"event"`
}

// mentionRe strips <@U12345> bot mentions from message text.
var mentionRe = regexp.MustCompile(`<@[A-Z0-9]+>`)

// handleEvents serves the Slack Events API endpoint: URL verification
// handshake, signature validation, immediate ack (3-second budget), and
// asynchronous agent processing.
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.signatureValid(r, body) {
		log.Printf("⚠️  rejected request: invalid X-Slack-Signature")
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	var payload eventsPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	switch payload.Type {
	case "url_verification":
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(payload.Challenge))
		return
	case "event_callback":
		// Ack within Slack's 3-second budget; process in the background.
		w.WriteHeader(http.StatusOK)
		// Slack redelivers on timeout/5xx; skip retries to avoid double runs.
		if r.Header.Get("X-Slack-Retry-Num") != "" {
			return
		}
		s.dispatch(payload)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

// dispatch filters events and hands text messages to the agent.
func (s *server) dispatch(p eventsPayload) {
	ev := p.Event
	// Ignore our own / other bots' messages and message edits/joins etc.
	if ev.BotID != "" || ev.Subtype != "" || ev.User == "" {
		return
	}
	switch ev.Type {
	case "message", "app_mention":
	default:
		return
	}
	text := strings.TrimSpace(mentionRe.ReplaceAllString(ev.Text, ""))
	// Reply in-thread for channel mentions; in-channel for DMs.
	threadTS := ev.ThreadTS
	if ev.Type == "app_mention" && threadTS == "" {
		threadTS = ev.TS
	}
	log.Printf("📥 channel=%s user=%s text=%q", ev.Channel, ev.User, text)
	go s.process(ev.Channel, ev.User, threadTS, text)
}

// process runs the agent for one inbound message and sends the reply back.
func (s *server) process(channel, user, threadTS, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), agentTimeout)
	defer cancel()

	if text == "" {
		s.reply(ctx, channel, threadTS, "I can only handle text messages right now. 🙂")
		return
	}

	// Scope the session to channel+user so each conversation keeps its own
	// context (a user's DM and their thread in a channel stay separate).
	sessionID := channel + ":" + user
	reply, err := s.runAgent(ctx, user, sessionID, text)
	if err != nil {
		log.Printf("❌ agent run failed: %v", err)
		s.reply(ctx, channel, threadTS, "Sorry, something went wrong on our side. Please try again.")
		return
	}
	if reply == "" {
		reply = "(no response)"
	}
	s.reply(ctx, channel, threadTS, reply)
}

// reply sends text back to the channel/thread, logging any error.
func (s *server) reply(ctx context.Context, channel, threadTS, text string) {
	if err := s.postMessage(ctx, channel, threadTS, text); err != nil {
		log.Printf("❌ send failed: %v", err)
		return
	}
	log.Printf("📤 channel=%s thread=%s len=%d", channel, threadTS, len(text))
}

// postMessage calls chat.postMessage, splitting long text into segments
// within Slack's 4000-character limit.
func (s *server) postMessage(ctx context.Context, channel, threadTS, text string) error {
	endpoint := s.apiBase + "/chat.postMessage"
	for _, segment := range splitMessage(text, maxSegmentLen) {
		body := map[string]any{
			"channel": channel,
			"text":    segment,
		}
		if threadTS != "" {
			body["thread_ts"] = threadTS
		}
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("Authorization", "Bearer "+s.botToken)
		resp, err := s.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("do request: %w", err)
		}
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var apiResp struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(respBody, &apiResp); err != nil || !apiResp.OK {
			return fmt.Errorf("chat.postMessage status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}
	}
	return nil
}

// signatureValid verifies Slack's request signature:
// "v0=" + hex(HMAC-SHA256(signingSecret, "v0:<timestamp>:<rawBody>")),
// rejecting requests whose timestamp is too old (replay protection).
func (s *server) signatureValid(r *http.Request, body []byte) bool {
	provided := r.Header.Get("X-Slack-Signature")
	tsHeader := r.Header.Get("X-Slack-Request-Timestamp")
	if provided == "" || tsHeader == "" {
		return false
	}
	ts, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil {
		return false
	}
	age := time.Since(time.Unix(ts, 0))
	if age > signatureMaxAge || age < -signatureMaxAge {
		return false
	}
	mac := hmac.New(sha256.New, []byte(s.signingSecret))
	fmt.Fprintf(mac, "v0:%s:", tsHeader)
	_, _ = mac.Write(body)
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(provided))
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
