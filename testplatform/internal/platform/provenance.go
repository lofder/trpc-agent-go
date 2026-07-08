package platform

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Provenance sources for assembled context messages.
const (
	SrcSystemInstruction = "system_instruction"
	SrcInjectedSystem    = "injected_system"
	SrcSessionHistory    = "session_history"
	SrcCurrentInput      = "current_input"
	SrcToolCallRound     = "assistant_tool_call"
	SrcToolResponse      = "tool_response"
	SrcHumanEdited       = "human_edited"
	SrcInjectedOther     = "injected_other"
)

var sourceLabels = map[string]string{
	SrcSystemInstruction: "系统指令",
	SrcInjectedSystem:    "注入的系统上下文",
	SrcSessionHistory:    "会话历史",
	SrcCurrentInput:      "本次用户输入",
	SrcToolCallRound:     "模型工具调用(本轮)",
	SrcToolResponse:      "工具执行结果",
	SrcHumanEdited:       "人工修改",
	SrcInjectedOther:     "处理器注入",
}

// MessageProvenance explains where one assembled context message came from
// and under which condition it was included in the model request.
type MessageProvenance struct {
	Index        int            `json:"index"`
	Role         string         `json:"role"`
	Source       string         `json:"source"`
	SourceLabel  string         `json:"source_label"`
	Reason       string         `json:"reason"`
	Origin       map[string]any `json:"origin,omitempty"`
	Content      string         `json:"content"`
	ContentChars int            `json:"content_chars"`
	ToolCalls    []ToolCallView `json:"tool_calls,omitempty"`
	ToolID       string         `json:"tool_id,omitempty"`
	ToolName     string         `json:"tool_name,omitempty"`
}

// ToolCallView is a compact JSON view of a tool call inside a message.
type ToolCallView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// historyEntry is a flattened view of a completed session event used for
// matching request messages back to their originating events.
type historyEntry struct {
	role         model.Role
	content      string
	toolID       string
	toolCallIDs  string
	author       string
	eventID      string
	invocationID string
	timestamp    string
	used         bool
}

func toolCallSignature(calls []model.ToolCall) string {
	parts := make([]string, 0, len(calls))
	for _, c := range calls {
		parts = append(parts, c.ID+"/"+c.Function.Name)
	}
	return strings.Join(parts, ",")
}

func toolCallViews(calls []model.ToolCall) []ToolCallView {
	out := make([]ToolCallView, 0, len(calls))
	for _, c := range calls {
		out = append(out, ToolCallView{ID: c.ID, Name: c.Function.Name, Arguments: string(c.Function.Arguments)})
	}
	return out
}

// collectHistory flattens session events into matchable entries.
func collectHistory(inv *agent.Invocation) []historyEntry {
	if inv == nil || inv.Session == nil {
		return nil
	}
	inv.Session.EventMu.RLock()
	events := make([]event.Event, len(inv.Session.Events))
	copy(events, inv.Session.Events)
	inv.Session.EventMu.RUnlock()

	var out []historyEntry
	for i := range events {
		e := &events[i]
		if e.Response == nil || len(e.Response.Choices) == 0 || e.Response.IsPartial {
			continue
		}
		for _, ch := range e.Response.Choices {
			m := ch.Message
			if m.Role == "" {
				continue
			}
			out = append(out, historyEntry{
				role:         m.Role,
				content:      m.Content,
				toolID:       m.ToolID,
				toolCallIDs:  toolCallSignature(m.ToolCalls),
				author:       e.Author,
				eventID:      e.ID,
				invocationID: e.InvocationID,
				timestamp:    e.Timestamp.Format("15:04:05.000"),
			})
		}
	}
	return out
}

// matchHistory finds the first unused history entry matching the message.
func matchHistory(hist []historyEntry, m model.Message) *historyEntry {
	for i := range hist {
		h := &hist[i]
		if h.used || h.role != m.Role {
			continue
		}
		switch m.Role {
		case model.RoleTool:
			if h.toolID == m.ToolID {
				h.used = true
				return h
			}
		case model.RoleAssistant:
			if len(m.ToolCalls) > 0 {
				if h.toolCallIDs == toolCallSignature(m.ToolCalls) {
					h.used = true
					return h
				}
				continue
			}
			if h.content == m.Content && h.toolCallIDs == "" {
				h.used = true
				return h
			}
		default:
			if h.content == m.Content {
				h.used = true
				return h
			}
		}
	}
	return nil
}

// AnalyzeContext attributes every message of the outgoing model request to
// its origin: agent instruction, session history replay, current user input,
// current tool-calling round or other processor injection.
func AnalyzeContext(inv *agent.Invocation, req *model.Request, configuredInstruction string) []MessageProvenance {
	hist := collectHistory(inv)
	curInvID := ""
	curInput := ""
	if inv != nil {
		curInvID = inv.InvocationID
		curInput = inv.Message.Content
	}

	// Locate the last user message equal to the current input: that is the
	// "current turn" input; earlier identical ones belong to history.
	lastCurrentInputIdx := -1
	if curInput != "" {
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == model.RoleUser && req.Messages[i].Content == curInput {
				lastCurrentInputIdx = i
				break
			}
		}
	}

	out := make([]MessageProvenance, 0, len(req.Messages))
	seenSystem := false
	for i, m := range req.Messages {
		p := MessageProvenance{
			Index:        i,
			Role:         string(m.Role),
			Content:      m.Content,
			ContentChars: len([]rune(m.Content)),
			ToolID:       m.ToolID,
			ToolName:     m.ToolName,
		}
		if len(m.ToolCalls) > 0 {
			p.ToolCalls = toolCallViews(m.ToolCalls)
		}

		switch {
		case m.Role == model.RoleSystem && !seenSystem:
			seenSystem = true
			p.Source = SrcSystemInstruction
			exact := strings.TrimSpace(m.Content) == strings.TrimSpace(configuredInstruction)
			detail := "内容在配置的 Instruction 基础上有增强（可能合并了 GlobalInstruction / 身份说明 / 时间信息 / 状态占位符替换等处理器注入）。"
			if exact {
				detail = "内容与 Agent 配置的 Instruction 完全一致。"
			}
			p.Reason = "由 InstructionRequestProcessor 在每次模型调用构建请求时注入为第一条 system 消息。" +
				"拼接条件：Agent 配置了 WithInstruction/WithGlobalInstruction 即注入；每轮都会基于当前会话状态重新生成（支持 {key} 占位符替换）。" + detail
		case m.Role == model.RoleSystem && seenSystem:
			p.Source = SrcInjectedSystem
			p.Reason = "额外的 system 消息，通常由知识库(knowledge)、会话摘要(summary)、记忆(memory)等处理器在启用相应能力时注入。" +
				"拼接条件：对应能力开启且检索/摘要命中。"
		case i == lastCurrentInputIdx:
			p.Source = SrcCurrentInput
			if h := matchHistory(hist, m); h != nil {
				p.Origin = originOf(h, curInvID)
			}
			p.Reason = "本次调用的用户输入。Runner.Run 收到消息后先将其作为事件写入会话，再由 ContentRequestProcessor 追加为最后一条 user 消息。" +
				"拼接条件：每次调用必然包含。"
		case m.Role == model.RoleAssistant && len(m.ToolCalls) > 0:
			h := matchHistory(hist, m)
			if h != nil && h.invocationID == curInvID {
				p.Source = SrcToolCallRound
				p.Origin = originOf(h, curInvID)
				p.Reason = "本轮（当前调用）中模型上一步返回的工具调用请求，由 FunctionCall 流程回写进上下文。" +
					"拼接条件：仅当上一次模型响应包含 tool_calls 时出现，用于让模型在工具结果之后继续推理。"
			} else {
				p.Source = SrcSessionHistory
				if h != nil {
					p.Origin = originOf(h, curInvID)
				}
				p.Reason = "历史轮次中模型发起的工具调用，由 ContentRequestProcessor 从会话事件回放。" +
					"拼接条件：IncludeContents 未设为 none 时，历史事件按时间序全部转为消息。"
			}
		case m.Role == model.RoleTool:
			h := matchHistory(hist, m)
			if h != nil && h.invocationID == curInvID {
				p.Source = SrcToolResponse
				p.Origin = originOf(h, curInvID)
				p.Reason = fmt.Sprintf("本轮工具 %s 的执行结果（tool.response 事件），工具执行完成后由框架追加，供模型生成后续回答。"+
					"拼接条件：紧跟对应的 assistant tool_calls 消息之后，一次工具调用对应一条。", m.ToolName)
			} else {
				p.Source = SrcSessionHistory
				if h != nil {
					p.Origin = originOf(h, curInvID)
				}
				p.Reason = "历史轮次中的工具执行结果，由 ContentRequestProcessor 从会话事件回放。"
			}
		default:
			if h := matchHistory(hist, m); h != nil {
				p.Source = SrcSessionHistory
				p.Origin = originOf(h, curInvID)
				p.Reason = "来自会话历史：ContentRequestProcessor 把本会话此前的事件按时间序转换为消息回放给模型。" +
					"拼接条件：IncludeContents 未设为 none（默认回放全部历史）；多 Agent 场景还会按 Branch 过滤。"
			} else {
				p.Source = SrcInjectedOther
				p.Reason = "未能匹配到会话事件，可能由其他请求处理器（planner 计划、少样本示例、摘要替换等）动态注入，或历史事件已被压缩/改写。"
			}
		}
		p.SourceLabel = sourceLabels[p.Source]
		out = append(out, p)
	}
	return out
}

func originOf(h *historyEntry, curInvID string) map[string]any {
	scope := "history"
	if h.invocationID == curInvID {
		scope = "current_invocation"
	}
	return map[string]any{
		"event_id":      h.eventID,
		"author":        h.author,
		"event_time":    h.timestamp,
		"invocation_id": h.invocationID,
		"scope":         scope,
	}
}

// ContextRule documents one stage of the request assembly pipeline.
type ContextRule struct {
	Order     int    `json:"order"`
	Stage     string `json:"stage"`
	Name      string `json:"name"`
	Condition string `json:"condition"`
	Effect    string `json:"effect"`
}

// ContextRules returns the static description of trpc-agent-go's request
// processor pipeline: which processor appends what, and under which
// condition. Order mirrors llmagent.buildRequestProcessorsWithAgent.
func ContextRules() []ContextRule {
	return []ContextRule{
		{1, "BasicRequestProcessor", "基础配置", "始终执行",
			"设置生成参数（temperature/max_tokens/stream 等）并把 Agent 的工具列表写入请求的工具声明；不产生消息。"},
		{2, "PlanningRequestProcessor", "规划指令", "配置了 WithPlanner 时",
			"把 Planner 的规划指令合并进系统指令，引导模型先给出计划再执行。"},
		{3, "InstructionRequestProcessor", "系统指令", "配置了 WithInstruction / WithGlobalInstruction 时",
			"生成第一条 system 消息；支持 {state_key} 占位符——每轮从会话状态取值替换，因此同一 Agent 不同轮次的 system 消息可能不同。"},
		{4, "IdentityRequestProcessor", "身份信息", "配置了名称/描述且启用 AddNameToInstruction 时",
			"把 Agent 的名字和描述合并进 system 消息，让模型知道\"我是谁\"。"},
		{5, "SkillsRequestProcessor", "技能清单", "配置了 WithSkills 技能仓库时",
			"注入可用技能概览及技能加载协议说明。"},
		{6, "TimeRequestProcessor", "时间信息", "启用 WithTimeProcessor 时",
			"把当前日期/时间注入 system 消息，让模型具备时间感知。"},
		{7, "ContentRequestProcessor", "会话内容(核心)", "IncludeContents 未设为 none（默认包含）",
			"把会话中此前的所有事件按时间序转换为 user/assistant/tool 消息回放，最后追加本次用户输入。" +
				"多 Agent 场景按 Branch 过滤事件；若启用会话摘要，较早的历史会被摘要替换。"},
		{8, "FunctionCall 工具回路", "工具调用轮次", "上一次模型响应包含 tool_calls 时",
			"框架执行工具后，把 assistant(tool_calls) 消息与对应的 tool 结果消息追加到上下文，再次调用模型；循环直到模型不再请求工具或达到 MaxToolIterations。"},
		{9, "本平台 BeforeModel 断点", "人工介入", "在\"断点介入\"页开启模型断点时",
			"请求发出前挂起，允许在 Web 页面上查看/编辑全部已拼接消息（增删改），或直接注入自定义响应跳过真实模型调用。"},
	}
}
