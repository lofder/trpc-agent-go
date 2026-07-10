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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// server holds the runner and Messenger Platform transport configuration.
type server struct {
	runner      runner.Runner
	pageToken   string
	appSecret   string
	verifyToken string
	apiBase     string // e.g. https://graph.facebook.com/v23.0
	httpClient  *http.Client
}

// webhookPayload mirrors the subset of the Messenger webhook body we consume.
type webhookPayload struct {
	Object string `json:"object"`
	Entry  []struct {
		Messaging []struct {
			Sender struct {
				ID string `json:"id"` // page-scoped user id (PSID)
			} `json:"sender"`
			Message *struct {
				MID    string `json:"mid"`
				Text   string `json:"text"`
				IsEcho bool   `json:"is_echo"`
			} `json:"message"`
		} `json:"messaging"`
	} `json:"entry"`
}

// handleWebhook serves both the GET verification handshake and POST events.
func (s *server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleVerify(w, r)
	case http.MethodPost:
		s.handleEvent(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleVerify implements Meta's webhook subscription handshake: echo
// hub.challenge when hub.verify_token matches.
func (s *server) handleVerify(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("hub.mode") == "subscribe" && q.Get("hub.verify_token") == s.verifyToken {
		_, _ = w.Write([]byte(q.Get("hub.challenge")))
		return
	}
	http.Error(w, "verification failed", http.StatusForbidden)
}

// handleEvent validates the signature, acks immediately, and processes each
// inbound text message asynchronously (Meta requires a fast 200 response).
func (s *server) handleEvent(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.signatureValid(r.Header.Get("X-Hub-Signature-256"), body) {
		log.Printf("⚠️  rejected webhook: invalid X-Hub-Signature-256")
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	// Ack first; process in the background.
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("EVENT_RECEIVED"))

	if payload.Object != "page" {
		return
	}
	for _, entry := range payload.Entry {
		for _, m := range entry.Messaging {
			// Skip echoes of our own sends and non-text events
			// (delivery receipts, postbacks, attachments...).
			if m.Message == nil || m.Message.IsEcho {
				continue
			}
			psid := m.Sender.ID
			text := strings.TrimSpace(m.Message.Text)
			log.Printf("📥 psid=%s text=%q", psid, text)
			if psid == "" {
				continue
			}
			go s.process(psid, text)
		}
	}
}

// signatureValid verifies Meta's X-Hub-Signature-256 header:
// "sha256=" + hex(HMAC-SHA256(appSecret, rawBody)).
func (s *server) signatureValid(header string, body []byte) bool {
	provided, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	mac := hmac.New(sha256.New, []byte(s.appSecret))
	_, _ = mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(provided))
}

// process runs the agent for one inbound message and sends the reply back.
func (s *server) process(psid, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), agentTimeout)
	defer cancel()

	if text == "" {
		s.reply(ctx, psid, "I can only handle text messages right now. Please send text. 🙂")
		return
	}

	// Use the page-scoped user id as both user and session id so the
	// conversation keeps context across messages.
	reply, err := s.runAgent(ctx, psid, psid, text)
	if err != nil {
		log.Printf("❌ agent run failed: %v", err)
		s.reply(ctx, psid, "Sorry, something went wrong on our side. Please try again.")
		return
	}
	if reply == "" {
		reply = "(no response)"
	}
	s.reply(ctx, psid, reply)
}

// reply sends text back to the customer, logging any error.
func (s *server) reply(ctx context.Context, psid, text string) {
	if err := s.sendMessage(ctx, psid, text); err != nil {
		log.Printf("❌ send failed: %v", err)
		return
	}
	log.Printf("📤 psid=%s len=%d", psid, len(text))
}

// sendMessage posts an outbound message to the Send API, splitting long text
// into segments within Messenger's 2000-character limit.
func (s *server) sendMessage(ctx context.Context, psid, text string) error {
	endpoint := s.apiBase + "/me/messages?access_token=" + s.pageToken
	for _, segment := range splitMessage(text, maxSegmentLen) {
		payload, err := json.Marshal(map[string]any{
			"recipient":      map[string]string{"id": psid},
			"messaging_type": "RESPONSE",
			"message":        map[string]string{"text": segment},
		})
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := s.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("do request: %w", err)
		}
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("graph api status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}
	}
	return nil
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
