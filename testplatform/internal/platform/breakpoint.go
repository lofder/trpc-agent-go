package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Intervention kinds.
const (
	InterventionModel = "model_request"
	InterventionTool  = "tool_call"
)

// Intervention actions.
const (
	ActionContinue = "continue" // proceed unchanged
	ActionModify   = "modify"   // apply edited messages / arguments, then proceed
	ActionMock     = "mock"     // skip the real call, inject a custom response/result
	ActionAbort    = "abort"    // fail the call with an error
)

// Resolution carries the human decision for a pending intervention.
type Resolution struct {
	Action      string          `json:"action"`
	Messages    []model.Message `json:"messages,omitempty"`     // model_request + modify
	MockContent string          `json:"mock_content,omitempty"` // model_request + mock
	ToolArgs    json.RawMessage `json:"tool_args,omitempty"`    // tool_call + modify
	ToolResult  any             `json:"tool_result,omitempty"`  // tool_call + mock
	Note        string          `json:"note,omitempty"`
}

// Intervention is a paused step waiting for a human decision.
type Intervention struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	RunID     string         `json:"run_id"`
	SpanID    string         `json:"span_id"`
	CreatedAt time.Time      `json:"created_at"`
	Deadline  time.Time      `json:"deadline"`
	Summary   string         `json:"summary"`
	Payload   map[string]any `json:"payload"`

	resolveCh chan Resolution
	once      sync.Once
}

// BreakpointManager holds breakpoint switches and pending interventions.
type BreakpointManager struct {
	mu           sync.Mutex
	modelEnabled bool
	toolEnabled  bool
	timeout      time.Duration
	pending      map[string]*Intervention
	hub          *Hub
	logger       *StepLogger
}

// NewBreakpointManager creates a manager with the given wait timeout.
func NewBreakpointManager(hub *Hub, logger *StepLogger, timeout time.Duration) *BreakpointManager {
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	return &BreakpointManager{
		timeout: timeout,
		pending: make(map[string]*Intervention),
		hub:     hub,
		logger:  logger,
	}
}

// State returns the current switches and timeout.
func (b *BreakpointManager) State() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return map[string]any{
		"model_enabled": b.modelEnabled,
		"tool_enabled":  b.toolEnabled,
		"timeout_sec":   int(b.timeout.Seconds()),
		"pending":       len(b.pending),
	}
}

// Configure updates the switches. Disabling a breakpoint kind auto-releases
// its pending interventions so runs don't stay stuck until timeout.
func (b *BreakpointManager) Configure(modelEnabled, toolEnabled bool, timeoutSec int) {
	b.mu.Lock()
	b.modelEnabled = modelEnabled
	b.toolEnabled = toolEnabled
	if timeoutSec > 0 {
		b.timeout = time.Duration(timeoutSec) * time.Second
	}
	var release []*Intervention
	for _, iv := range b.pending {
		if (iv.Kind == InterventionModel && !modelEnabled) ||
			(iv.Kind == InterventionTool && !toolEnabled) {
			release = append(release, iv)
		}
	}
	b.mu.Unlock()
	b.logger.Log(LevelStep, CatBreakpoint, "", fmt.Sprintf("断点配置更新: 模型断点=%v 工具断点=%v 超时=%s", modelEnabled, toolEnabled, b.timeout), nil)
	for _, iv := range release {
		b.logger.Log(LevelStep, CatBreakpoint, iv.RunID, "断点已关闭，自动放行挂起的介入 "+shortID(iv.ID), nil)
		iv.once.Do(func() { iv.resolveCh <- Resolution{Action: ActionContinue, Note: "breakpoint disabled"} })
	}
	b.hub.Publish("breakpoints", b.State())
}

// ModelEnabled reports whether model-request breakpoints are on.
func (b *BreakpointManager) ModelEnabled() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.modelEnabled
}

// ToolEnabled reports whether tool-call breakpoints are on.
func (b *BreakpointManager) ToolEnabled() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.toolEnabled
}

// Pending lists pending interventions, oldest first.
func (b *BreakpointManager) Pending() []*Intervention {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*Intervention, 0, len(b.pending))
	for _, iv := range b.pending {
		out = append(out, iv)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].CreatedAt.Before(out[i].CreatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// ErrAborted is returned when the human aborts a paused call.
var ErrAborted = errors.New("aborted by human intervention")

// Wait registers an intervention and blocks until it is resolved, the
// timeout elapses or the run context is cancelled. On timeout/cancel the
// call continues unchanged.
func (b *BreakpointManager) Wait(ctx context.Context, kind, runID, spanID, summary string, payload map[string]any) Resolution {
	iv := &Intervention{
		ID:        uuid.NewString(),
		Kind:      kind,
		RunID:     runID,
		SpanID:    spanID,
		CreatedAt: time.Now(),
		Summary:   summary,
		Payload:   payload,
		resolveCh: make(chan Resolution, 1),
	}
	b.mu.Lock()
	iv.Deadline = iv.CreatedAt.Add(b.timeout)
	timeout := b.timeout
	b.pending[iv.ID] = iv
	b.mu.Unlock()

	b.logger.Log(LevelStep, CatBreakpoint, runID,
		fmt.Sprintf("⏸ 断点命中(%s)，等待人工介入 (intervention=%s, 超时 %s)", kind, shortID(iv.ID), timeout),
		map[string]any{"intervention_id": iv.ID, "summary": summary})
	b.hub.Publish("intervention", map[string]any{"state": "pending", "intervention": iv})

	defer func() {
		b.mu.Lock()
		delete(b.pending, iv.ID)
		b.mu.Unlock()
		b.hub.Publish("intervention", map[string]any{"state": "closed", "id": iv.ID, "run_id": runID})
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-iv.resolveCh:
		b.logger.Log(LevelStep, CatBreakpoint, runID,
			fmt.Sprintf("▶ 人工介入完成: action=%s %s", res.Action, res.Note),
			map[string]any{"intervention_id": iv.ID})
		return res
	case <-timer.C:
		b.logger.Warnf(CatBreakpoint, runID, "断点等待超时(%s)，自动放行", timeout)
		return Resolution{Action: ActionContinue, Note: "timeout auto-continue"}
	case <-ctx.Done():
		b.logger.Warnf(CatBreakpoint, runID, "运行被取消，断点自动放行以便清理")
		return Resolution{Action: ActionAbort, Note: "run cancelled"}
	}
}

// Resolve delivers a decision for the pending intervention.
func (b *BreakpointManager) Resolve(id string, res Resolution) error {
	b.mu.Lock()
	iv, ok := b.pending[id]
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("intervention %s not found (已被处理或已超时)", id)
	}
	switch res.Action {
	case ActionContinue, ActionModify, ActionMock, ActionAbort:
	default:
		return fmt.Errorf("unknown action %q", res.Action)
	}
	iv.once.Do(func() { iv.resolveCh <- res })
	return nil
}
