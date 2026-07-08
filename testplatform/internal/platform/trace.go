package platform

import (
	"encoding/json"
	"sync"
	"time"
)

// Span kinds.
const (
	SpanKindAgent = "agent"
	SpanKindModel = "model"
	SpanKindTool  = "tool"
)

// Span / run statuses.
const (
	StatusRunning   = "running"
	StatusWaiting   = "waiting" // paused on a breakpoint, waiting for human
	StatusOK        = "ok"
	StatusCompleted = "completed"
	StatusError     = "error"
	StatusCancelled = "cancelled"
	StatusMocked    = "mocked" // human injected a custom response
)

// UsageStat aggregates token usage for a run.
type UsageStat struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Span is one node of the invocation trace tree: the agent execution, a
// model call or a tool call.
type Span struct {
	ID         string         `json:"id"`
	ParentID   string         `json:"parent_id,omitempty"`
	RunID      string         `json:"run_id"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Seq        int            `json:"seq"`
	StartedAt  time.Time      `json:"started_at"`
	EndedAt    *time.Time     `json:"ended_at,omitempty"`
	DurationMS int64          `json:"duration_ms"`
	Status     string         `json:"status"`
	Error      string         `json:"error,omitempty"`
	Intervened bool           `json:"intervened,omitempty"`
	Detail     map[string]any `json:"detail,omitempty"`
}

// EventLog is a compact record of one event received from the runner event
// channel (the full call-chain visible to the client).
type EventLog struct {
	Seq       int       `json:"seq"`
	Time      time.Time `json:"time"`
	EventID   string    `json:"event_id"`
	Author    string    `json:"author"`
	Object    string    `json:"object"`
	Branch    string    `json:"branch,omitempty"`
	Preview   string    `json:"preview,omitempty"`
	ToolCalls []string  `json:"tool_calls,omitempty"`
	Partial   bool      `json:"partial,omitempty"`
	Final     bool      `json:"final,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// RunRecord is the trace of one Runner.Run invocation.
type RunRecord struct {
	ID          string     `json:"id"`
	Seq         int        `json:"seq"`
	Source      string     `json:"source"` // playground | testcase
	CaseID      string     `json:"case_id,omitempty"`
	CaseName    string     `json:"case_name,omitempty"`
	AppName     string     `json:"app_name"`
	UserID      string     `json:"user_id"`
	SessionID   string     `json:"session_id"`
	AgentName   string     `json:"agent_name"`
	ModelName   string     `json:"model_name"`
	Input       string     `json:"input"`
	FinalOutput string     `json:"final_output"`
	Status      string     `json:"status"`
	Error       string     `json:"error,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	DurationMS  int64      `json:"duration_ms"`
	Usage       UsageStat  `json:"usage"`
	ModelCalls  int        `json:"model_calls"`
	ToolCalls   int        `json:"tool_calls"`
	ToolNames   []string   `json:"tool_names,omitempty"`
	Intervened  bool       `json:"intervened"`
	Spans       []*Span    `json:"spans"`
	Events      []EventLog `json:"events"`
}

// RunSummary is the list-view projection of a RunRecord.
type RunSummary struct {
	ID          string     `json:"id"`
	Seq         int        `json:"seq"`
	Source      string     `json:"source"`
	CaseID      string     `json:"case_id,omitempty"`
	CaseName    string     `json:"case_name,omitempty"`
	SessionID   string     `json:"session_id"`
	AgentName   string     `json:"agent_name"`
	ModelName   string     `json:"model_name"`
	Input       string     `json:"input"`
	FinalOutput string     `json:"final_output"`
	Status      string     `json:"status"`
	Error       string     `json:"error,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	DurationMS  int64      `json:"duration_ms"`
	Usage       UsageStat  `json:"usage"`
	ModelCalls  int        `json:"model_calls"`
	ToolCalls   int        `json:"tool_calls"`
	Intervened  bool       `json:"intervened"`
}

// TraceStore keeps recent run traces in memory.
type TraceStore struct {
	mu      sync.RWMutex
	runs    map[string]*RunRecord
	order   []string // oldest first
	max     int
	seq     int
	spanSeq int
}

// NewTraceStore creates a store keeping at most max runs.
func NewTraceStore(max int) *TraceStore {
	if max <= 0 {
		max = 300
	}
	return &TraceStore{runs: make(map[string]*RunRecord), max: max}
}

// CreateRun registers a new run and returns its sequence number.
func (s *TraceStore) CreateRun(r *RunRecord) *RunRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	r.Seq = s.seq
	r.Status = StatusRunning
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now()
	}
	s.runs[r.ID] = r
	s.order = append(s.order, r.ID)
	for len(s.order) > s.max {
		old := s.order[0]
		s.order = s.order[1:]
		delete(s.runs, old)
	}
	return r
}

// Update applies fn to the run with the given id under the store lock.
func (s *TraceStore) Update(runID string, fn func(*RunRecord)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return false
	}
	fn(r)
	return true
}

// AddSpan creates a span attached to the run and returns its id.
func (s *TraceStore) AddSpan(runID string, span *Span) *Span {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return nil
	}
	s.spanSeq++
	span.Seq = s.spanSeq
	span.RunID = runID
	if span.StartedAt.IsZero() {
		span.StartedAt = time.Now()
	}
	if span.Status == "" {
		span.Status = StatusRunning
	}
	if span.Detail == nil {
		span.Detail = map[string]any{}
	}
	r.Spans = append(r.Spans, span)
	return span
}

// UpdateSpan applies fn to the span with the given id.
func (s *TraceStore) UpdateSpan(runID, spanID string, fn func(*Span)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return false
	}
	for _, sp := range r.Spans {
		if sp.ID == spanID {
			fn(sp)
			return true
		}
	}
	return false
}

// EndSpan closes a span with the given status/error.
func (s *TraceStore) EndSpan(runID, spanID, status, errMsg string) {
	now := time.Now()
	s.UpdateSpan(runID, spanID, func(sp *Span) {
		sp.EndedAt = &now
		sp.DurationMS = now.Sub(sp.StartedAt).Milliseconds()
		sp.Status = status
		if errMsg != "" {
			sp.Error = errMsg
		}
	})
}

// GetRun returns a deep copy (JSON round-trip) of the run, safe for
// concurrent marshaling.
func (s *TraceStore) GetRun(runID string) (*RunRecord, bool) {
	s.mu.RLock()
	r, ok := s.runs[runID]
	if !ok {
		s.mu.RUnlock()
		return nil, false
	}
	raw, err := json.Marshal(r)
	s.mu.RUnlock()
	if err != nil {
		return nil, false
	}
	var cp RunRecord
	if err := json.Unmarshal(raw, &cp); err != nil {
		return nil, false
	}
	return &cp, true
}

// Summary builds the list-view projection of a run. Caller must hold the lock.
func summaryOf(r *RunRecord) RunSummary {
	return RunSummary{
		ID: r.ID, Seq: r.Seq, Source: r.Source, CaseID: r.CaseID, CaseName: r.CaseName,
		SessionID: r.SessionID, AgentName: r.AgentName, ModelName: r.ModelName,
		Input: truncate(r.Input, 120), FinalOutput: truncate(r.FinalOutput, 200),
		Status: r.Status, Error: r.Error, StartedAt: r.StartedAt, EndedAt: r.EndedAt,
		DurationMS: r.DurationMS, Usage: r.Usage, ModelCalls: r.ModelCalls,
		ToolCalls: r.ToolCalls, Intervened: r.Intervened,
	}
}

// ListRuns returns run summaries, newest first.
func (s *TraceStore) ListRuns(limit int) []RunSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.order) {
		limit = len(s.order)
	}
	out := make([]RunSummary, 0, limit)
	for i := len(s.order) - 1; i >= 0 && len(out) < limit; i-- {
		if r, ok := s.runs[s.order[i]]; ok {
			out = append(out, summaryOf(r))
		}
	}
	return out
}

// SummaryFor returns the summary of one run.
func (s *TraceStore) SummaryFor(runID string) (RunSummary, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.runs[runID]
	if !ok {
		return RunSummary{}, false
	}
	return summaryOf(r), true
}

// Stats returns aggregate counters for the dashboard.
func (s *TraceStore) Stats() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total, running, failed, completed := 0, 0, 0, 0
	var tokens int
	for _, r := range s.runs {
		total++
		switch r.Status {
		case StatusRunning, StatusWaiting:
			running++
		case StatusError:
			failed++
		case StatusCompleted:
			completed++
		}
		tokens += r.Usage.TotalTokens
	}
	return map[string]any{
		"total": total, "running": running, "failed": failed,
		"completed": completed, "total_tokens": tokens,
	}
}
