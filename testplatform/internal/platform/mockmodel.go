package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// MockModel is a deterministic offline LLM used when no real model API key
// is configured. It understands the demo tools (calculator / current_time /
// get_weather) well enough to exercise the full tool-calling loop, so every
// platform feature (context assembly, call chain, breakpoints, test cases)
// works without external dependencies.
type MockModel struct {
	name    string
	latency time.Duration
}

// NewMockModel creates a mock model.
func NewMockModel(name string, latency time.Duration) *MockModel {
	if name == "" {
		name = "mock-llm"
	}
	if latency <= 0 {
		latency = 150 * time.Millisecond
	}
	return &MockModel{name: name, latency: latency}
}

// Info implements model.Model.
func (m *MockModel) Info() model.Info {
	return model.Info{Name: m.name}
}

var (
	calcRe   = regexp.MustCompile(`(-?\d+(?:\.\d+)?)\s*([-+*/^xX×÷])\s*(-?\d+(?:\.\d+)?)`)
	numberRe = regexp.MustCompile(`-?\d+(?:\.\d+)?`)
)

// GenerateContent implements model.Model.
func (m *MockModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("mock model: nil request")
	}
	ch := make(chan *model.Response, 8)
	go func() {
		defer close(ch)
		select {
		case <-time.After(m.latency):
		case <-ctx.Done():
			return
		}
		rsp := m.respond(req)
		if req.Stream && len(rsp.Choices) > 0 && len(rsp.Choices[0].Message.ToolCalls) == 0 {
			// Emit partial chunks followed by the aggregated final response,
			// mirroring the OpenAI adapter's streaming behavior.
			content := rsp.Choices[0].Message.Content
			for _, part := range splitChunks(content, 4) {
				select {
				case <-ctx.Done():
					return
				case ch <- &model.Response{
					ID: rsp.ID, Object: model.ObjectTypeChatCompletionChunk,
					Created: rsp.Created, Model: m.name, IsPartial: true,
					Choices: []model.Choice{{Index: 0, Delta: model.Message{Role: model.RoleAssistant, Content: part}}},
				}:
					time.Sleep(40 * time.Millisecond)
				}
			}
		}
		select {
		case ch <- rsp:
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

func splitChunks(s string, n int) []string {
	r := []rune(s)
	if n <= 1 || len(r) == 0 {
		return []string{s}
	}
	size := (len(r) + n - 1) / n
	var out []string
	for i := 0; i < len(r); i += size {
		end := i + size
		if end > len(r) {
			end = len(r)
		}
		out = append(out, string(r[i:end]))
	}
	return out
}

func (m *MockModel) respond(req *model.Request) *model.Response {
	lastUser := ""
	lastUserIdx := -1
	for i, msg := range req.Messages {
		if msg.Role == model.RoleUser {
			lastUser = msg.Content
			lastUserIdx = i
		}
	}
	// Tool results present after the last user message => produce the final
	// answer summarizing them.
	var toolResults []model.Message
	toolCalled := map[string]string{} // tool call id -> name
	for i, msg := range req.Messages {
		if i <= lastUserIdx {
			continue
		}
		if msg.Role == model.RoleAssistant {
			for _, tc := range msg.ToolCalls {
				toolCalled[tc.ID] = tc.Function.Name
			}
		}
		if msg.Role == model.RoleTool {
			toolResults = append(toolResults, msg)
		}
	}
	if len(toolResults) > 0 {
		return m.finalResponse(m.summarize(lastUser, toolResults, toolCalled), req)
	}
	if name, args, ok := m.pickTool(lastUser, req); ok {
		return m.toolCallResponse(name, args, req)
	}
	return m.finalResponse(m.smallTalk(lastUser), req)
}

func (m *MockModel) pickTool(input string, req *model.Request) (string, []byte, bool) {
	if req.Tools == nil {
		return "", nil, false
	}
	has := func(name string) bool { _, ok := req.Tools[name]; return ok }
	low := strings.ToLower(input)

	if has("calculator") {
		if mm := calcRe.FindStringSubmatch(input); mm != nil {
			op := map[string]string{
				"+": "add", "-": "subtract", "*": "multiply", "x": "multiply", "X": "multiply",
				"×": "multiply", "/": "divide", "÷": "divide", "^": "power",
			}[mm[2]]
			args, _ := json.Marshal(map[string]any{"operation": op, "a": atof(mm[1]), "b": atof(mm[3])})
			return "calculator", args, true
		}
		if strings.Contains(low, "计算") || strings.Contains(low, "算一下") || strings.Contains(low, "calculate") {
			nums := numberRe.FindAllString(input, 2)
			if len(nums) == 2 {
				args, _ := json.Marshal(map[string]any{"operation": "add", "a": atof(nums[0]), "b": atof(nums[1])})
				return "calculator", args, true
			}
		}
	}
	if has("current_time") && (strings.Contains(low, "时间") || strings.Contains(low, "几点") ||
		strings.Contains(low, "日期") || strings.Contains(low, "time") || strings.Contains(low, "date")) {
		tz := "Asia/Shanghai"
		if strings.Contains(low, "utc") {
			tz = "UTC"
		}
		args, _ := json.Marshal(map[string]any{"timezone": tz})
		return "current_time", args, true
	}
	if has("get_weather") && (strings.Contains(low, "天气") || strings.Contains(low, "weather")) {
		city := extractCity(input)
		args, _ := json.Marshal(map[string]any{"city": city})
		return "get_weather", args, true
	}
	return "", nil, false
}

func extractCity(input string) string {
	for _, c := range []string{"北京", "上海", "深圳", "广州", "杭州", "成都", "武汉", "西安", "南京", "重庆"} {
		if strings.Contains(input, c) {
			return c
		}
	}
	return "深圳"
}

func atof(s string) float64 {
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}

func (m *MockModel) summarize(question string, toolResults []model.Message, called map[string]string) string {
	var b strings.Builder
	b.WriteString("根据工具执行结果：\n")
	for _, tr := range toolResults {
		name := tr.ToolName
		if name == "" {
			name = called[tr.ToolID]
		}
		b.WriteString(fmt.Sprintf("- %s 返回 %s\n", name, strings.TrimSpace(tr.Content)))
	}
	b.WriteString(fmt.Sprintf("已完成你的请求：%s", strings.TrimSpace(question)))
	return b.String()
}

func (m *MockModel) smallTalk(input string) string {
	in := strings.TrimSpace(input)
	if in == "" {
		return "你好，我是测试平台内置的 Mock 助手。"
	}
	low := strings.ToLower(in)
	switch {
	case strings.Contains(low, "你好") || strings.Contains(low, "hello") || strings.Contains(low, "hi"):
		return "你好！我是 Mock 模式下的测试助手。我可以做算术（例如\"计算 12*34\"）、报时间（\"现在几点\"）、查天气（\"深圳天气怎么样\"）。这些会触发真实的工具调用链路，方便你观察上下文拼接过程。"
	case strings.Contains(low, "你是谁") || strings.Contains(low, "who are you"):
		return "我是 trpc-agent-go 测试监控平台内置的确定性 Mock 模型，用于在没有真实 LLM API Key 时演练完整的 Agent 执行链路。"
	default:
		return fmt.Sprintf("收到你的消息：「%s」。当前为 Mock 模型模式（未配置真实 LLM）。试试包含算式、时间或天气的问题来触发工具调用链路。", in)
	}
}

func (m *MockModel) toolCallResponse(name string, args []byte, req *model.Request) *model.Response {
	finish := "tool_calls"
	return &model.Response{
		ID:      "mock-" + uuid.NewString(),
		Object:  model.ObjectTypeChatCompletion,
		Created: time.Now().Unix(),
		Model:   m.name,
		Done:    true,
		Choices: []model.Choice{{
			Index:        0,
			FinishReason: &finish,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call_" + uuid.NewString()[:8],
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name:      name,
						Arguments: args,
					},
				}},
			},
		}},
		Usage: m.usage(req, 24),
	}
}

func (m *MockModel) finalResponse(content string, req *model.Request) *model.Response {
	finish := "stop"
	return &model.Response{
		ID:      "mock-" + uuid.NewString(),
		Object:  model.ObjectTypeChatCompletion,
		Created: time.Now().Unix(),
		Model:   m.name,
		Done:    true,
		Choices: []model.Choice{{
			Index:        0,
			FinishReason: &finish,
			Message:      model.Message{Role: model.RoleAssistant, Content: content},
		}},
		Usage: m.usage(req, len([]rune(content))/2+8),
	}
}

// usage estimates token usage so dashboards have data in mock mode.
func (m *MockModel) usage(req *model.Request, completion int) *model.Usage {
	prompt := 0
	for _, msg := range req.Messages {
		prompt += len([]rune(msg.Content))/2 + 4
	}
	return &model.Usage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
	}
}
