// Package platform implements the agent test & observability platform built
// on top of trpc-agent-go. It instruments an agent-under-test with model /
// tool / agent callbacks, records execution traces (including how the model
// context is assembled), supports human-in-the-loop interventions and manages
// test cases.
package platform

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Log levels.
const (
	LevelInfo  = "INFO"
	LevelStep  = "STEP"
	LevelWarn  = "WARN"
	LevelError = "ERROR"
)

// Log categories, used for filtering in the UI.
const (
	CatServer     = "server"
	CatAgent      = "agent"
	CatModel      = "model"
	CatTool       = "tool"
	CatContext    = "context"
	CatEvent      = "event"
	CatBreakpoint = "breakpoint"
	CatSession    = "session"
	CatTestcase   = "testcase"
	CatHTTP       = "http"
)

// LogEntry is a single structured log record. Every step of the platform and
// of the instrumented agent produces one of these; they are printed to stdout
// and broadcast to the web UI.
type LogEntry struct {
	Seq      int64          `json:"seq"`
	Time     time.Time      `json:"time"`
	Level    string         `json:"level"`
	Category string         `json:"category"`
	RunID    string         `json:"run_id,omitempty"`
	Message  string         `json:"message"`
	Data     map[string]any `json:"data,omitempty"`
}

// StepLogger prints every step to stdout, keeps a bounded in-memory ring and
// broadcasts entries to SSE subscribers through the hub.
type StepLogger struct {
	mu      sync.Mutex
	seq     int64
	entries []LogEntry
	max     int
	hub     *Hub
}

// NewStepLogger creates a StepLogger keeping at most max entries in memory.
func NewStepLogger(hub *Hub, max int) *StepLogger {
	if max <= 0 {
		max = 5000
	}
	return &StepLogger{hub: hub, max: max}
}

// Log records one entry.
func (l *StepLogger) Log(level, category, runID, msg string, data map[string]any) {
	l.mu.Lock()
	l.seq++
	e := LogEntry{
		Seq:      l.seq,
		Time:     time.Now(),
		Level:    level,
		Category: category,
		RunID:    runID,
		Message:  msg,
		Data:     data,
	}
	l.entries = append(l.entries, e)
	if len(l.entries) > l.max {
		l.entries = l.entries[len(l.entries)-l.max:]
	}
	l.mu.Unlock()

	fmt.Fprintln(os.Stdout, formatEntry(e))
	if l.hub != nil {
		l.hub.Publish("log", e)
	}
}

// Infof logs an INFO entry.
func (l *StepLogger) Infof(category, runID, format string, args ...any) {
	l.Log(LevelInfo, category, runID, fmt.Sprintf(format, args...), nil)
}

// Stepf logs a STEP entry (execution steps of the agent pipeline).
func (l *StepLogger) Stepf(category, runID string, data map[string]any, format string, args ...any) {
	l.Log(LevelStep, category, runID, fmt.Sprintf(format, args...), data)
}

// Warnf logs a WARN entry.
func (l *StepLogger) Warnf(category, runID, format string, args ...any) {
	l.Log(LevelWarn, category, runID, fmt.Sprintf(format, args...), nil)
}

// Errorf logs an ERROR entry.
func (l *StepLogger) Errorf(category, runID, format string, args ...any) {
	l.Log(LevelError, category, runID, fmt.Sprintf(format, args...), nil)
}

// Recent returns up to limit most recent entries, optionally filtered by
// level, category and run id.
func (l *StepLogger) Recent(limit int, level, category, runID string) []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if limit <= 0 || limit > len(l.entries) {
		limit = len(l.entries)
	}
	out := make([]LogEntry, 0, limit)
	for i := len(l.entries) - 1; i >= 0 && len(out) < limit; i-- {
		e := l.entries[i]
		if level != "" && e.Level != level {
			continue
		}
		if category != "" && e.Category != category {
			continue
		}
		if runID != "" && e.RunID != runID {
			continue
		}
		out = append(out, e)
	}
	// Reverse back to chronological order.
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

func formatEntry(e LogEntry) string {
	var b strings.Builder
	b.WriteString(e.Time.Format("2006-01-02T15:04:05.000"))
	b.WriteString(fmt.Sprintf(" [%-5s] [%-10s]", e.Level, e.Category))
	if e.RunID != "" {
		b.WriteString(" run=")
		b.WriteString(shortID(e.RunID))
	}
	b.WriteString(" ")
	b.WriteString(e.Message)
	if len(e.Data) > 0 {
		keys := make([]string, 0, len(e.Data))
		for k := range e.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(fmt.Sprintf(" %s=%v", k, compact(e.Data[k])))
		}
	}
	return b.String()
}

func compact(v any) string {
	s := fmt.Sprintf("%v", v)
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// truncate shortens s to max runes for previews.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
