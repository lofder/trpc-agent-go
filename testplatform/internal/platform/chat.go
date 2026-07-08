package platform

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// RunRequest describes one agent invocation to execute.
type RunRequest struct {
	Source    string // playground | testcase
	CaseID    string
	CaseName  string
	UserID    string
	SessionID string
	Input     string
}

// StartRun launches an agent invocation asynchronously and returns the run
// id plus a channel closed when the run finishes.
func (p *Platform) StartRun(req RunRequest) (string, <-chan struct{}, error) {
	if strings.TrimSpace(req.Input) == "" {
		return "", nil, fmt.Errorf("input is empty")
	}
	if req.UserID == "" {
		req.UserID = DefaultUserID
	}
	if req.SessionID == "" {
		req.SessionID = fmt.Sprintf("session-%s", uuid.NewString()[:8])
	}
	if req.Source == "" {
		req.Source = "playground"
	}

	p.mu.Lock()
	r := p.runner
	settings := p.settings
	p.mu.Unlock()
	if r == nil {
		return "", nil, fmt.Errorf("runner not ready")
	}

	runID := uuid.NewString()
	p.Store.CreateRun(&RunRecord{
		ID:        runID,
		Source:    req.Source,
		CaseID:    req.CaseID,
		CaseName:  req.CaseName,
		AppName:   AppName,
		UserID:    req.UserID,
		SessionID: req.SessionID,
		AgentName: settings.AgentName,
		ModelName: settings.Model,
		Input:     req.Input,
	})
	p.touchSession(req.SessionID, req.UserID, req.Input)

	ctx, cancel := context.WithCancel(context.Background())
	p.runCancelMu.Lock()
	p.runCancels[runID] = cancel
	p.runCancelMu.Unlock()

	p.Logger.Stepf(CatSession, runID, map[string]any{
		"session_id": req.SessionID, "user_id": req.UserID, "source": req.Source,
	}, "🚀 新运行启动: source=%s session=%s 输入=%q", req.Source, req.SessionID, truncate(req.Input, 100))
	if sum, ok := p.Store.SummaryFor(runID); ok {
		p.Hub.Publish("run_update", sum)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			p.runCancelMu.Lock()
			delete(p.runCancels, runID)
			p.runCancelMu.Unlock()
			cancel()
		}()
		p.executeRun(ctx, r, runID, req)
	}()
	return runID, done, nil
}

// CancelRun cancels a running invocation.
func (p *Platform) CancelRun(runID string) bool {
	p.runCancelMu.Lock()
	cancel, ok := p.runCancels[runID]
	p.runCancelMu.Unlock()
	if !ok {
		return false
	}
	p.Logger.Warnf(CatSession, runID, "运行被用户取消")
	cancel()
	return true
}

type runnerIface interface {
	Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error)
}

func (p *Platform) executeRun(ctx context.Context, r runnerIface, runID string, req RunRequest) {
	start := time.Now()
	finish := func(status, errMsg, finalOutput string) {
		now := time.Now()
		p.Store.Update(runID, func(rr *RunRecord) {
			if finalOutput != "" {
				rr.FinalOutput = finalOutput
			}
			rr.Status = status
			rr.Error = errMsg
			rr.EndedAt = &now
			rr.DurationMS = now.Sub(rr.StartedAt).Milliseconds()
		})
		if sum, ok := p.Store.SummaryFor(runID); ok {
			p.Hub.Publish("run_update", sum)
			p.Hub.Publish("run_finished", sum)
		}
	}

	evCh, err := r.Run(ctx, req.UserID, req.SessionID, model.NewUserMessage(req.Input), agent.WithRequestID(runID))
	if err != nil {
		p.Logger.Errorf(CatSession, runID, "Runner.Run 启动失败: %v", err)
		finish(StatusError, err.Error(), "")
		return
	}

	var finalOutput strings.Builder
	var streamed strings.Builder
	evSeq := 0
	var runErr string

	for ev := range evCh {
		evSeq++
		entry := p.eventLog(evSeq, ev)
		p.Store.Update(runID, func(rr *RunRecord) {
			rr.Events = append(rr.Events, entry)
		})
		// Live output for the playground: streaming deltas or full content.
		if ev.Response != nil && len(ev.Response.Choices) > 0 {
			ch := ev.Response.Choices[0]
			if ev.Response.IsPartial && ch.Delta.Content != "" {
				streamed.WriteString(ch.Delta.Content)
				p.Hub.Publish("run_output", map[string]any{
					"run_id": runID, "delta": ch.Delta.Content, "partial": true,
				})
			}
		}
		if entry.Error != "" {
			runErr = entry.Error
			p.Logger.Errorf(CatEvent, runID, "⚠ 事件错误: %s (object=%s)", entry.Error, entry.Object)
		} else {
			p.Logger.Stepf(CatEvent, runID, nil, "⑦ 事件#%d author=%s object=%s%s %s",
				evSeq, entry.Author, entry.Object, partialMark(entry.Partial), entry.Preview)
		}
		if ev.Response != nil && !ev.Response.IsPartial && len(ev.Response.Choices) > 0 {
			m := ev.Response.Choices[0].Message
			if m.Role == model.RoleAssistant && m.Content != "" && len(m.ToolCalls) == 0 &&
				ev.Response.Object != model.ObjectTypeRunnerCompletion {
				finalOutput.Reset()
				finalOutput.WriteString(m.Content)
				p.Hub.Publish("run_output", map[string]any{
					"run_id": runID, "content": m.Content, "partial": false,
				})
			}
		}
	}

	out := finalOutput.String()
	if out == "" {
		out = streamed.String()
	}
	elapsed := time.Since(start)
	if ctx.Err() != nil {
		p.Logger.Warnf(CatSession, runID, "🏁 运行已取消 (%.2fs)", elapsed.Seconds())
		finish(StatusCancelled, "cancelled", out)
		return
	}
	if runErr != "" {
		p.Logger.Errorf(CatSession, runID, "🏁 运行结束(含错误): %s (%.2fs)", runErr, elapsed.Seconds())
		finish(StatusError, runErr, out)
		return
	}
	sum, _ := p.Store.SummaryFor(runID)
	p.Logger.Stepf(CatSession, runID, map[string]any{
		"duration_ms": elapsed.Milliseconds(), "model_calls": sum.ModelCalls,
		"tool_calls": sum.ToolCalls, "tokens": sum.Usage.TotalTokens,
	}, "🏁 运行完成 (%.2fs)：模型调用 %d 次，工具调用 %d 次，输出 %d 字",
		elapsed.Seconds(), sum.ModelCalls, sum.ToolCalls, len([]rune(out)))
	finish(StatusCompleted, "", out)
}

func partialMark(partial bool) string {
	if partial {
		return " (delta)"
	}
	return ""
}

func (p *Platform) eventLog(seq int, ev *event.Event) EventLog {
	entry := EventLog{
		Seq:     seq,
		Time:    time.Now(),
		EventID: ev.ID,
		Author:  ev.Author,
		Branch:  ev.Branch,
	}
	if ev.Response != nil {
		entry.Object = ev.Response.Object
		entry.Partial = ev.Response.IsPartial
		entry.Final = ev.Response.IsFinalResponse()
		if ev.Response.Error != nil {
			entry.Error = ev.Response.Error.Message
		}
		if len(ev.Response.Choices) > 0 {
			ch := ev.Response.Choices[0]
			content := ch.Message.Content
			if content == "" {
				content = ch.Delta.Content
			}
			entry.Preview = truncate(strings.ReplaceAll(content, "\n", " "), 120)
			for _, tc := range ch.Message.ToolCalls {
				entry.ToolCalls = append(entry.ToolCalls, tc.Function.Name)
			}
			if ch.Message.Role == model.RoleTool && ch.Message.ToolName != "" {
				entry.Preview = fmt.Sprintf("[%s] %s", ch.Message.ToolName, entry.Preview)
			}
		}
	}
	return entry
}
