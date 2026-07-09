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
	"strings"
	"time"
)

// orderStatusArgs is the input for the order_status tool.
type orderStatusArgs struct {
	OrderID string `json:"order_id" jsonschema:"description=The customer's order ID, e.g. A1001"`
}

// orderStatusResult is the output for the order_status tool.
type orderStatusResult struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
	ETA     string `json:"eta,omitempty"`
	Carrier string `json:"carrier,omitempty"`
	Found   bool   `json:"found"`
	Note    string `json:"note,omitempty"`
}

// demoOrders is a stand-in for a real order database / API.
var demoOrders = map[string]orderStatusResult{
	"A1001": {OrderID: "A1001", Status: "shipped", ETA: "2 days", Carrier: "DHL", Found: true},
	"A1002": {OrderID: "A1002", Status: "processing", ETA: "5 days", Found: true},
	"A1003": {OrderID: "A1003", Status: "delivered", Carrier: "FedEx", Found: true},
}

// lookupOrderStatus is a demo tool. Replace the body with a real database or
// API call to make the agent do genuine work for your customers.
func lookupOrderStatus(_ context.Context, args orderStatusArgs) (orderStatusResult, error) {
	id := strings.ToUpper(strings.TrimSpace(args.OrderID))
	if order, ok := demoOrders[id]; ok {
		return order, nil
	}
	return orderStatusResult{
		OrderID: id,
		Found:   false,
		Note:    "No order found with that ID. Please double-check the order number.",
	}, nil
}

// timeArgs is the input for the current_time tool.
type timeArgs struct {
	Timezone string `json:"timezone" jsonschema:"description=Timezone like UTC, EST, PST; empty for local"`
}

// timeResult is the output for the current_time tool.
type timeResult struct {
	Timezone string `json:"timezone"`
	Time     string `json:"time"`
	Date     string `json:"date"`
	Weekday  string `json:"weekday"`
}

// currentTime returns the current time for a (simplified) timezone.
func currentTime(_ context.Context, args timeArgs) (timeResult, error) {
	now := time.Now()
	t := now
	tz := args.Timezone
	switch strings.ToUpper(strings.TrimSpace(args.Timezone)) {
	case "UTC":
		t = now.UTC()
	case "EST", "EASTERN":
		t = now.UTC().Add(-5 * time.Hour)
	case "PST", "PACIFIC":
		t = now.UTC().Add(-8 * time.Hour)
	case "CST", "CENTRAL":
		t = now.UTC().Add(-6 * time.Hour)
	case "":
		tz = "Local"
	default:
		t = now.UTC()
		tz = "UTC"
	}
	return timeResult{
		Timezone: tz,
		Time:     t.Format("15:04:05"),
		Date:     t.Format("2006-01-02"),
		Weekday:  t.Weekday().String(),
	}, nil
}
