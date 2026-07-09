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
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// server holds the runner and Twilio transport configuration.
type server struct {
	runner            runner.Runner
	accountSID        string
	authToken         string
	fromNumber        string // e.g. "whatsapp:+14155238886"
	publicURL         string // exact webhook URL configured in Twilio, optional
	validateSignature bool
	httpClient        *http.Client
}

// handleInbound receives Twilio's inbound WhatsApp webhook. Twilio posts
// application/x-www-form-urlencoded fields (Body, From, To, MessageSid, ...).
//
// The agent run may exceed Twilio's webhook timeout, so we acknowledge with an
// empty TwiML response right away and process the reply asynchronously, sending
// it back via the Twilio REST API.
func (s *server) handleInbound(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if s.validateSignature && !s.signatureValid(r) {
		log.Printf("⚠️  rejected webhook: invalid X-Twilio-Signature")
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	from := r.PostForm.Get("From")
	body := strings.TrimSpace(r.PostForm.Get("Body"))
	log.Printf("📥 from=%s body=%q", from, body)

	// Acknowledge immediately with empty TwiML; the real reply is sent later
	// through the REST API.
	w.Header().Set("Content-Type", "text/xml")
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`))

	if from == "" {
		return
	}
	go s.process(from, body)
}

// process runs the agent for one inbound message and sends the reply back.
func (s *server) process(from, body string) {
	ctx, cancel := context.WithTimeout(context.Background(), agentTimeout)
	defer cancel()

	if body == "" {
		s.reply(ctx, from, "I can only handle text messages right now. Please send text. 🙂")
		return
	}

	// Use the sender's WhatsApp address as both user and session id so the
	// conversation keeps context across messages.
	reply, err := s.runAgent(ctx, from, from, body)
	if err != nil {
		log.Printf("❌ agent run failed: %v", err)
		s.reply(ctx, from, "Sorry, something went wrong on our side. Please try again.")
		return
	}
	if reply == "" {
		reply = "(no response)"
	}
	s.reply(ctx, from, reply)
}

// reply sends text back to the customer, logging any error.
func (s *server) reply(ctx context.Context, to, text string) {
	if err := s.sendWhatsApp(ctx, to, text); err != nil {
		log.Printf("❌ send failed: %v", err)
		return
	}
	log.Printf("📤 to=%s len=%d", to, len(text))
}

// sendWhatsApp posts an outbound message to the Twilio REST API, splitting long
// text into segments within Twilio's per-message limit.
func (s *server) sendWhatsApp(ctx context.Context, to, body string) error {
	endpoint := fmt.Sprintf("%s/Accounts/%s/Messages.json", twilioAPIBase, s.accountSID)
	for _, segment := range splitMessage(body, maxSegmentLen) {
		form := url.Values{}
		form.Set("From", s.fromNumber)
		form.Set("To", to)
		form.Set("Body", segment)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.SetBasicAuth(s.accountSID, s.authToken)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("do request: %w", err)
		}
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("twilio status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}
	}
	return nil
}

// signatureValid verifies Twilio's X-Twilio-Signature for a form POST.
//
// Twilio computes: base64(HMAC-SHA1(authToken, url + sorted(key+value)...)).
// See https://www.twilio.com/docs/usage/security#validating-requests
func (s *server) signatureValid(r *http.Request) bool {
	provided := r.Header.Get("X-Twilio-Signature")
	if provided == "" {
		return false
	}

	fullURL := s.publicURL
	if fullURL == "" {
		fullURL = requestURL(r)
	}

	keys := make([]string, 0, len(r.PostForm))
	for k := range r.PostForm {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var data strings.Builder
	data.WriteString(fullURL)
	for _, k := range keys {
		data.WriteString(k)
		data.WriteString(r.PostForm.Get(k))
	}

	mac := hmac.New(sha1.New, []byte(s.authToken))
	_, _ = mac.Write([]byte(data.String()))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(provided))
}

// requestURL reconstructs the public URL Twilio used to reach this handler,
// honoring the proxy headers that ngrok / load balancers set.
func requestURL(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host + r.URL.RequestURI()
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
		// Try to break on a newline or space within the window.
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
