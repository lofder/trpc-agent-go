package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type ctxKey string

const ctxKeyModelSpan ctxKey = "testplatform.model_span"

// Collector wires agent/model/tool callbacks into the trace store, the step
// logger and the breakpoint manager. It is the platform's instrumentation
// core.
type Collector struct {
	store  *TraceStore
	logger *StepLogger
	hub    *Hub
	breaks *BreakpointManager

	mu             sync.Mutex
	invToRun       map[string]string // invocation id -> run id
	invAgentSpan   map[string]string // invocation id -> agent span id
	invModelSeq    map[string]int    // invocation id -> model call counter
	invLastModel   map[string]string // invocation id -> last model span id
	toolCallToSpan map[string]string // tool call id -> issuing model span id

	// instructionFn returns the currently configured agent instruction, used
	// by provenance analysis to compare against the assembled system message.
	instructionFn func() string
}

// NewCollector creates the instrumentation collector.
func NewCollector(store *TraceStore, logger *StepLogger, hub *Hub, breaks *BreakpointManager, instructionFn func() string) *Collector {
	return &Collector{
		store:          store,
		logger:         logger,
		hub:            hub,
		breaks:         breaks,
		invToRun:       make(map[string]string),
		invAgentSpan:   make(map[string]string),
		invModelSeq:    make(map[string]int),
		invLastModel:   make(map[string]string),
		toolCallToSpan: make(map[string]string),
		instructionFn:  instructionFn,
	}
}

func (c *Collector) runIDFor(inv *agent.Invocation) string {
	if inv == nil {
		return ""
	}
	if inv.RunOptions.RequestID != "" {
		return inv.RunOptions.RequestID
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.invToRun[inv.InvocationID]
}

func (c *Collector) publishRun(runID string) {
	if sum, ok := c.store.SummaryFor(runID); ok {
		c.hub.Publish("run_update", sum)
	}
}

// ---------------------------------------------------------------------------
// Agent callbacks
// ---------------------------------------------------------------------------

// AgentCallbacks returns instrumented agent callbacks.
func (c *Collector) AgentCallbacks() *agent.Callbacks {
	cb := agent.NewCallbacks()
	cb.RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
		inv := args.Invocation
		if inv == nil {
			return nil, nil
		}
		runID := c.runIDFor(inv)
		c.mu.Lock()
		c.invToRun[inv.InvocationID] = runID
		c.mu.Unlock()

		span := c.store.AddSpan(runID, &Span{
			ID:   uuid.NewString(),
			Kind: SpanKindAgent,
			Name: fmt.Sprintf("Agent: %s", inv.AgentName),
			Detail: map[string]any{
				"invocation_id": inv.InvocationID,
				"session_id":    sessionIDOf(inv),
				"input":         inv.Message.Content,
			},
		})
		if span != nil {
			c.mu.Lock()
			c.invAgentSpan[inv.InvocationID] = span.ID
			c.mu.Unlock()
		}
		c.store.Update(runID, func(r *RunRecord) {
			if r.AgentName == "" {
				r.AgentName = inv.AgentName
			}
		})
		histCount := 0
		if inv.Session != nil {
			inv.Session.EventMu.RLock()
			histCount = len(inv.Session.Events)
			inv.Session.EventMu.RUnlock()
		}
		c.logger.Stepf(CatAgent, runID, map[string]any{
			"invocation_id": inv.InvocationID,
			"agent":         inv.AgentName,
			"session_event": histCount,
		}, "① Agent[%s] 开始执行，输入=%q，会话已有 %d 条事件", inv.AgentName, truncate(inv.Message.Content, 80), histCount)
		c.publishRun(runID)
		return nil, nil
	})
	cb.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		inv := args.Invocation
		if inv == nil {
			return nil, nil
		}
		runID := c.runIDFor(inv)
		c.mu.Lock()
		spanID := c.invAgentSpan[inv.InvocationID]
		delete(c.invAgentSpan, inv.InvocationID)
		delete(c.invModelSeq, inv.InvocationID)
		delete(c.invLastModel, inv.InvocationID)
		delete(c.invToRun, inv.InvocationID)
		c.mu.Unlock()

		status := StatusOK
		errMsg := ""
		if args.Error != nil {
			status = StatusError
			errMsg = args.Error.Error()
		}
		if spanID != "" {
			c.store.EndSpan(runID, spanID, status, errMsg)
		}
		c.logger.Stepf(CatAgent, runID, nil, "⑧ Agent[%s] 执行结束 status=%s %s", inv.AgentName, status, errMsg)
		c.publishRun(runID)
		return nil, nil
	})
	return cb
}

func sessionIDOf(inv *agent.Invocation) string {
	if inv == nil || inv.Session == nil {
		return ""
	}
	return inv.Session.ID
}

// ---------------------------------------------------------------------------
// Model callbacks
// ---------------------------------------------------------------------------

// serializableMessage is the JSON view of a context message shown/edited in
// the UI.
type serializableMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	ToolID    string         `json:"tool_id,omitempty"`
	ToolName  string         `json:"tool_name,omitempty"`
	ToolCalls []ToolCallView `json:"tool_calls,omitempty"`
}

func serializeMessages(msgs []model.Message) []serializableMessage {
	out := make([]serializableMessage, 0, len(msgs))
	for _, m := range msgs {
		sm := serializableMessage{
			Role: string(m.Role), Content: m.Content,
			ToolID: m.ToolID, ToolName: m.ToolName,
		}
		if len(m.ToolCalls) > 0 {
			sm.ToolCalls = toolCallViews(m.ToolCalls)
		}
		out = append(out, sm)
	}
	return out
}

func deserializeMessages(in []serializableMessage) []model.Message {
	out := make([]model.Message, 0, len(in))
	for _, sm := range in {
		m := model.Message{
			Role: model.Role(sm.Role), Content: sm.Content,
			ToolID: sm.ToolID, ToolName: sm.ToolName,
		}
		for _, tc := range sm.ToolCalls {
			m.ToolCalls = append(m.ToolCalls, model.ToolCall{
				ID: tc.ID, Type: "function",
				Function: model.FunctionDefinitionParam{Name: tc.Name, Arguments: []byte(tc.Arguments)},
			})
		}
		out = append(out, m)
	}
	return out
}

// ModelCallbacks returns instrumented model callbacks: they capture the
// assembled context (with provenance), optionally pause for human
// intervention, and record the model response.
func (c *Collector) ModelCallbacks() *model.Callbacks {
	cb := model.NewCallbacks()
	cb.RegisterBeforeModel(model.BeforeModelCallbackStructured(c.beforeModel))
	cb.RegisterAfterModel(model.AfterModelCallbackStructured(c.afterModel))
	return cb
}

func (c *Collector) beforeModel(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
	req := args.Request
	inv, _ := agent.InvocationFromContext(ctx)
	runID := c.runIDFor(inv)

	invID := ""
	if inv != nil {
		invID = inv.InvocationID
	}
	c.mu.Lock()
	c.invModelSeq[invID]++
	seq := c.invModelSeq[invID]
	parent := c.invAgentSpan[invID]
	c.mu.Unlock()

	modelName := ""
	if inv != nil && inv.Model != nil {
		modelName = inv.Model.Info().Name
	}

	// Provenance: attribute every assembled message to its origin.
	prov := AnalyzeContext(inv, req, c.instructionFn())
	toolNames := make([]string, 0, len(req.Tools))
	for name := range req.Tools {
		toolNames = append(toolNames, name)
	}

	span := c.store.AddSpan(runID, &Span{
		ID:       uuid.NewString(),
		ParentID: parent,
		Kind:     SpanKindModel,
		Name:     fmt.Sprintf("模型调用 #%d (%s)", seq, modelName),
		Detail: map[string]any{
			"model":       modelName,
			"call_seq":    seq,
			"messages":    serializeMessages(req.Messages),
			"provenance":  prov,
			"tools":       toolNames,
			"gen_config":  genConfigView(req),
			"msg_count":   len(req.Messages),
			"total_chars": totalChars(req.Messages),
		},
	})
	spanID := ""
	if span != nil {
		spanID = span.ID
	}
	c.mu.Lock()
	c.invLastModel[invID] = spanID
	c.mu.Unlock()

	// Step logs: the assembly summary plus one line per message.
	srcCount := map[string]int{}
	for _, p := range prov {
		srcCount[p.SourceLabel]++
	}
	c.logger.Stepf(CatContext, runID, map[string]any{
		"call_seq": seq, "messages": len(req.Messages), "sources": srcCount, "tools": toolNames,
	}, "② 上下文拼接完成(第 %d 次模型调用)：共 %d 条消息 [%s]", seq, len(req.Messages), summarizeSources(srcCount))
	for _, p := range prov {
		c.logger.Stepf(CatContext, runID, nil, "   · 消息#%d role=%s 来源=%s | %s",
			p.Index, p.Role, p.SourceLabel, truncate(strings.ReplaceAll(p.Content, "\n", " "), 90))
	}

	// Breakpoint: pause and let the human inspect / edit the context.
	if c.breaks.ModelEnabled() {
		c.store.UpdateSpan(runID, spanID, func(sp *Span) { sp.Status = StatusWaiting })
		c.store.Update(runID, func(r *RunRecord) { r.Status = StatusWaiting })
		c.publishRun(runID)

		res := c.breaks.Wait(ctx, InterventionModel, runID, spanID,
			fmt.Sprintf("模型调用 #%d (%s)，%d 条消息", seq, modelName, len(req.Messages)),
			map[string]any{
				"messages":   serializeMessages(req.Messages),
				"provenance": prov,
				"model":      modelName,
				"call_seq":   seq,
			})
		c.store.Update(runID, func(r *RunRecord) {
			if r.Status == StatusWaiting {
				r.Status = StatusRunning
			}
		})
		switch res.Action {
		case ActionModify:
			if len(res.Messages) > 0 {
				before := len(req.Messages)
				req.Messages = res.Messages
				newProv := AnalyzeContext(inv, req, c.instructionFn())
				for i := range newProv {
					if i >= len(prov) || prov[i].Content != newProv[i].Content || prov[i].Role != newProv[i].Role {
						newProv[i].Source = SrcHumanEdited
						newProv[i].SourceLabel = sourceLabels[SrcHumanEdited]
						newProv[i].Reason = "该消息在断点介入时被人工修改/新增。" + newProv[i].Reason
					}
				}
				c.store.UpdateSpan(runID, spanID, func(sp *Span) {
					sp.Intervened = true
					sp.Status = StatusRunning
					sp.Detail["messages"] = serializeMessages(req.Messages)
					sp.Detail["provenance"] = newProv
					sp.Detail["original_messages"] = toSerializable(prov)
					sp.Detail["intervention"] = map[string]any{"action": res.Action, "note": res.Note}
				})
				c.store.Update(runID, func(r *RunRecord) { r.Intervened = true })
				c.logger.Stepf(CatBreakpoint, runID, nil,
					"✏️ 人工修改了上下文：%d 条 → %d 条消息，继续调用模型", before, len(req.Messages))
			}
		case ActionMock:
			mock := c.buildMockResponse(modelName, res.MockContent)
			c.store.UpdateSpan(runID, spanID, func(sp *Span) {
				sp.Intervened = true
				sp.Detail["intervention"] = map[string]any{"action": res.Action, "note": res.Note}
				sp.Detail["response"] = map[string]any{"content": res.MockContent, "mocked": true}
			})
			c.store.EndSpan(runID, spanID, StatusMocked, "")
			c.store.Update(runID, func(r *RunRecord) { r.Intervened = true; r.ModelCalls++ })
			c.logger.Stepf(CatBreakpoint, runID, nil, "🧪 人工注入了模型响应（跳过真实调用）：%s", truncate(res.MockContent, 100))
			c.publishRun(runID)
			return &model.BeforeModelResult{
				Context:        context.WithValue(ctx, ctxKeyModelSpan, spanID),
				CustomResponse: mock,
			}, nil
		case ActionAbort:
			c.store.EndSpan(runID, spanID, StatusCancelled, "人工中止")
			c.publishRun(runID)
			c.logger.Warnf(CatBreakpoint, runID, "🛑 人工中止了本次模型调用")
			return nil, fmt.Errorf("model call aborted by human intervention")
		default:
			c.store.UpdateSpan(runID, spanID, func(sp *Span) { sp.Status = StatusRunning })
			c.logger.Stepf(CatBreakpoint, runID, nil, "▶ 人工放行，未修改上下文")
		}
		c.publishRun(runID)
	}

	c.logger.Stepf(CatModel, runID, nil, "③ 调用模型 %s (第 %d 次)…", modelName, seq)
	return &model.BeforeModelResult{
		Context: context.WithValue(ctx, ctxKeyModelSpan, spanID),
	}, nil
}

// toSerializable rebuilds serializable messages from provenance snapshots
// (used to keep the pre-edit copy).
func toSerializable(prov []MessageProvenance) []serializableMessage {
	out := make([]serializableMessage, 0, len(prov))
	for _, p := range prov {
		out = append(out, serializableMessage{
			Role: p.Role, Content: p.Content, ToolID: p.ToolID, ToolName: p.ToolName, ToolCalls: p.ToolCalls,
		})
	}
	return out
}

func (c *Collector) afterModel(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
	rsp := args.Response
	// Streaming: AfterModel fires for every chunk; only finalize on the
	// terminal response.
	if rsp != nil && rsp.IsPartial {
		return nil, nil
	}
	if rsp != nil && !rsp.Done && args.Error == nil && rsp.Error == nil {
		return nil, nil
	}

	inv, _ := agent.InvocationFromContext(ctx)
	runID := c.runIDFor(inv)
	invID := ""
	if inv != nil {
		invID = inv.InvocationID
	}
	spanID, _ := ctx.Value(ctxKeyModelSpan).(string)
	c.mu.Lock()
	if spanID == "" {
		spanID = c.invLastModel[invID]
	}
	c.mu.Unlock()

	status := StatusOK
	errMsg := ""
	if args.Error != nil {
		status, errMsg = StatusError, args.Error.Error()
	}
	if rsp != nil && rsp.Error != nil {
		status, errMsg = StatusError, rsp.Error.Message
	}

	content, toolCalls := "", []ToolCallView{}
	finish := ""
	if rsp != nil && len(rsp.Choices) > 0 {
		content = rsp.Choices[0].Message.Content
		toolCalls = toolCallViews(rsp.Choices[0].Message.ToolCalls)
		if rsp.Choices[0].FinishReason != nil {
			finish = *rsp.Choices[0].FinishReason
		}
		// Map tool call ids to this span so tool spans nest correctly.
		c.mu.Lock()
		for _, tc := range rsp.Choices[0].Message.ToolCalls {
			c.toolCallToSpan[tc.ID] = spanID
		}
		c.mu.Unlock()
	}

	var usage *UsageStat
	if rsp != nil && rsp.Usage != nil {
		usage = &UsageStat{
			PromptTokens:     rsp.Usage.PromptTokens,
			CompletionTokens: rsp.Usage.CompletionTokens,
			TotalTokens:      rsp.Usage.TotalTokens,
		}
	}

	// A span already closed here was human-mocked in beforeModel: the
	// framework still routes the custom response through AfterModel, so skip
	// finalization to preserve the "mocked" status and avoid double counting.
	closed := false
	now := time.Now()
	c.store.UpdateSpan(runID, spanID, func(sp *Span) {
		if sp.EndedAt != nil {
			return
		}
		closed = true
		respView := map[string]any{
			"content":       content,
			"tool_calls":    toolCalls,
			"finish_reason": finish,
		}
		if usage != nil {
			respView["usage"] = usage
		}
		sp.Detail["response"] = respView
		sp.EndedAt = &now
		sp.DurationMS = now.Sub(sp.StartedAt).Milliseconds()
		sp.Status = status
		if errMsg != "" {
			sp.Error = errMsg
		}
	})
	if !closed {
		return nil, nil
	}
	c.store.Update(runID, func(r *RunRecord) {
		r.ModelCalls++
		if usage != nil {
			r.Usage.PromptTokens += usage.PromptTokens
			r.Usage.CompletionTokens += usage.CompletionTokens
			r.Usage.TotalTokens += usage.TotalTokens
		}
	})

	if status == StatusError {
		c.logger.Errorf(CatModel, runID, "④ 模型调用失败: %s", errMsg)
	} else if len(toolCalls) > 0 {
		names := make([]string, 0, len(toolCalls))
		for _, tc := range toolCalls {
			names = append(names, tc.Name)
		}
		c.logger.Stepf(CatModel, runID, map[string]any{"usage": usage}, "④ 模型返回工具调用请求: %s", strings.Join(names, ", "))
	} else {
		c.logger.Stepf(CatModel, runID, map[string]any{"usage": usage}, "④ 模型返回最终内容(%d 字): %s", len([]rune(content)), truncate(content, 100))
	}
	c.publishRun(runID)
	return nil, nil
}

func (c *Collector) buildMockResponse(modelName, content string) *model.Response {
	finish := "stop"
	return &model.Response{
		ID:      "human-mock-" + uuid.NewString(),
		Object:  model.ObjectTypeChatCompletion,
		Created: time.Now().Unix(),
		Model:   modelName,
		Done:    true,
		Choices: []model.Choice{{
			Index:        0,
			FinishReason: &finish,
			Message:      model.Message{Role: model.RoleAssistant, Content: content},
		}},
	}
}

// ---------------------------------------------------------------------------
// Tool callbacks
// ---------------------------------------------------------------------------

// ToolCallbacks returns instrumented tool callbacks with breakpoint support.
func (c *Collector) ToolCallbacks() *tool.Callbacks {
	cb := tool.NewCallbacks()
	cb.RegisterBeforeTool(tool.BeforeToolCallbackStructured(c.beforeTool))
	cb.RegisterAfterTool(tool.AfterToolCallbackStructured(c.afterTool))
	return cb
}

func (c *Collector) beforeTool(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
	inv, _ := agent.InvocationFromContext(ctx)
	runID := c.runIDFor(inv)
	invID := ""
	if inv != nil {
		invID = inv.InvocationID
	}

	c.mu.Lock()
	parent := c.toolCallToSpan[args.ToolCallID]
	if parent == "" {
		parent = c.invLastModel[invID]
	}
	c.mu.Unlock()

	span := c.store.AddSpan(runID, &Span{
		ID:       uuid.NewString(),
		ParentID: parent,
		Kind:     SpanKindTool,
		Name:     fmt.Sprintf("工具: %s", args.ToolName),
		Detail: map[string]any{
			"tool_name":    args.ToolName,
			"tool_call_id": args.ToolCallID,
			"arguments":    string(args.Arguments),
		},
	})
	spanID := ""
	if span != nil {
		spanID = span.ID
	}
	c.mu.Lock()
	c.toolCallToSpan[args.ToolCallID] = spanID // repoint: children/after lookup
	c.mu.Unlock()

	c.logger.Stepf(CatTool, runID, map[string]any{"args": string(args.Arguments), "tool_call_id": args.ToolCallID},
		"⑤ 执行工具 %s，参数=%s", args.ToolName, truncate(string(args.Arguments), 120))

	if c.breaks.ToolEnabled() {
		c.store.UpdateSpan(runID, spanID, func(sp *Span) { sp.Status = StatusWaiting })
		c.store.Update(runID, func(r *RunRecord) { r.Status = StatusWaiting })
		c.publishRun(runID)
		res := c.breaks.Wait(ctx, InterventionTool, runID, spanID,
			fmt.Sprintf("工具调用 %s", args.ToolName),
			map[string]any{
				"tool_name":    args.ToolName,
				"tool_call_id": args.ToolCallID,
				"arguments":    string(args.Arguments),
				"declaration":  declarationView(args.Declaration),
			})
		c.store.Update(runID, func(r *RunRecord) {
			if r.Status == StatusWaiting {
				r.Status = StatusRunning
			}
		})
		switch res.Action {
		case ActionModify:
			if len(res.ToolArgs) > 0 {
				c.store.UpdateSpan(runID, spanID, func(sp *Span) {
					sp.Intervened = true
					sp.Status = StatusRunning
					sp.Detail["original_arguments"] = string(args.Arguments)
					sp.Detail["arguments"] = string(res.ToolArgs)
				})
				c.store.Update(runID, func(r *RunRecord) { r.Intervened = true })
				c.logger.Stepf(CatBreakpoint, runID, nil, "✏️ 人工修改了工具参数: %s", truncate(string(res.ToolArgs), 120))
				c.publishRun(runID)
				return &tool.BeforeToolResult{
					Context:           context.WithValue(ctx, ctxKey("tool_span_"+args.ToolCallID), spanID),
					ModifiedArguments: res.ToolArgs,
				}, nil
			}
		case ActionMock:
			c.store.UpdateSpan(runID, spanID, func(sp *Span) {
				sp.Intervened = true
				sp.Detail["result"] = res.ToolResult
				sp.Detail["intervention"] = map[string]any{"action": res.Action, "note": res.Note}
			})
			c.store.EndSpan(runID, spanID, StatusMocked, "")
			c.store.Update(runID, func(r *RunRecord) { r.Intervened = true })
			c.logger.Stepf(CatBreakpoint, runID, nil, "🧪 人工注入了工具结果（跳过真实执行）")
			c.publishRun(runID)
			return &tool.BeforeToolResult{CustomResult: res.ToolResult}, nil
		case ActionAbort:
			c.store.EndSpan(runID, spanID, StatusCancelled, "人工中止")
			c.publishRun(runID)
			return nil, fmt.Errorf("tool call aborted by human intervention")
		default:
			c.store.UpdateSpan(runID, spanID, func(sp *Span) { sp.Status = StatusRunning })
			c.logger.Stepf(CatBreakpoint, runID, nil, "▶ 人工放行工具调用 %s", args.ToolName)
		}
		c.publishRun(runID)
	}
	return nil, nil
}

func (c *Collector) afterTool(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
	inv, _ := agent.InvocationFromContext(ctx)
	runID := c.runIDFor(inv)
	c.mu.Lock()
	spanID := c.toolCallToSpan[args.ToolCallID]
	delete(c.toolCallToSpan, args.ToolCallID)
	c.mu.Unlock()

	status := StatusOK
	errMsg := ""
	if args.Error != nil {
		status, errMsg = StatusError, args.Error.Error()
	}
	resultJSON := jsonify(args.Result)
	// A mocked tool span was already closed by beforeTool; only finalize
	// spans still open.
	c.store.UpdateSpan(runID, spanID, func(sp *Span) {
		if sp.EndedAt == nil {
			now := time.Now()
			sp.EndedAt = &now
			sp.DurationMS = now.Sub(sp.StartedAt).Milliseconds()
			sp.Status = status
			if errMsg != "" {
				sp.Error = errMsg
			}
			sp.Detail["result"] = resultJSON
		}
	})
	c.store.Update(runID, func(r *RunRecord) {
		r.ToolCalls++
		r.ToolNames = appendUnique(r.ToolNames, args.ToolName)
	})
	if status == StatusError {
		c.logger.Errorf(CatTool, runID, "⑥ 工具 %s 执行失败: %s", args.ToolName, errMsg)
	} else {
		c.logger.Stepf(CatTool, runID, nil, "⑥ 工具 %s 执行完成，结果=%s", args.ToolName, truncate(resultJSON, 120))
	}
	c.publishRun(runID)
	return nil, nil
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func declarationView(d *tool.Declaration) map[string]any {
	if d == nil {
		return nil
	}
	return map[string]any{"name": d.Name, "description": d.Description}
}

func jsonify(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func genConfigView(req *model.Request) map[string]any {
	out := map[string]any{"stream": req.Stream}
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	if req.MaxTokens != nil {
		out["max_tokens"] = *req.MaxTokens
	}
	if req.TopP != nil {
		out["top_p"] = *req.TopP
	}
	return out
}

func totalChars(msgs []model.Message) int {
	n := 0
	for _, m := range msgs {
		n += len([]rune(m.Content))
	}
	return n
}

func summarizeSources(m map[string]int) string {
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s×%d", k, v))
	}
	return strings.Join(parts, " ")
}
