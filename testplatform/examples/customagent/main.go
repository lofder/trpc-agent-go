// Command customagent 演示如何把「你自己的 Agent」接入测试监控平台。
//
// 对接方式（迭代友好）：
//  1. 写一个 AgentBuilder 函数：里面是你自己的 Agent 组装逻辑（模型/指令/工具随便改）。
//  2. 组装时 append 一行 bc.Instrument.LLMAgentOptions() —— 这就是接入监控的全部代价。
//  3. p.RegisterAgent("名字", builder) 注册；平台在启动、改设置、切换被测 Agent 时
//     自动重建你的 Agent，因此迭代时只改 builder 内部，平台代码零改动。
//
// 运行：
//
//	cd testplatform && go run ./examples/customagent
//	打开 http://localhost:8080 → 「设置」页可在 builtin-demo 和 order-assistant 之间切换。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	agentpkg "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"

	"trpc.group/trpc-go/trpc-agent-go/testplatform/internal/platform"
	"trpc.group/trpc-go/trpc-agent-go/testplatform/web"
)

var (
	addr    = flag.String("addr", ":8080", "HTTP listen address")
	dataDir = flag.String("data", "testplatform-data", "directory for persisted test cases")
)

func main() {
	flag.Parse()

	p, err := platform.New(*dataDir, 300)
	if err != nil {
		log.Fatalf("platform init failed: %v", err)
	}

	// ★ 核心：注册你自己的 Agent 构建器，并设为当前被测 Agent。
	if err := p.RegisterAgent("order-assistant", buildOrderAgent); err != nil {
		log.Fatal(err)
	}
	if err := p.SetActiveAgent("order-assistant"); err != nil {
		log.Fatal(err)
	}

	p.Logger.Infof(platform.CatServer, "", "自定义 Agent 示例已就绪: http://localhost%s/ (设置页可切换 builtin-demo / order-assistant)", *addr)
	server := &http.Server{Addr: *addr, Handler: p.NewHTTPHandler(web.FS()), ReadHeaderTimeout: 10 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("http server: %v", err)
	}
}

// buildOrderAgent 是你的 Agent 组装逻辑 —— 迭代时只改这里。
func buildOrderAgent(bc platform.BuildContext) (agentpkg.Agent, error) {
	// 指令：优先用平台「设置」页里的热更新值（迭代 prompt 时不用重启），
	// 平台默认值是给内置 demo 的，识别到时退回本 Agent 自己的默认指令。
	instruction := bc.Settings.Instruction
	if !strings.Contains(instruction, "订单") {
		instruction = "你是电商订单助手。用户询问订单时必须调用 query_order 查询真实状态，" +
			"询问送达时间时调用 estimate_delivery。回答要简洁并引用工具返回的数据。"
	}

	// 模型：默认跟随平台设置（openai provider 时即真实模型，可在 UI 热切换）。
	// mock provider 下平台内置 mock 不认识订单领域，换成本示例的领域 mock，
	// 保证离线也能演练完整工具调用链路 —— 你接入真实模型后删掉这个分支即可。
	m := bc.Model
	if bc.Settings.Provider == "mock" {
		m = &orderMockModel{name: bc.Settings.Model}
	}

	opts := []llmagent.Option{
		llmagent.WithModel(m),
		llmagent.WithDescription("电商订单查询/物流预估助手（自定义被测 Agent 示例）"),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(bc.Settings.MaxTokens),
			Temperature: floatPtr(bc.Settings.Temperature),
			Stream:      bc.Settings.Streaming,
		}),
		llmagent.WithTools([]tool.Tool{queryOrderTool(), estimateDeliveryTool()}),
	}
	// ★ 接入平台监控（链路/上下文归因/断点/日志）只需要这一行。
	// 已有自己的回调？在 bc.Instrument.Model 等对象上继续 Register 即可。
	opts = append(opts, bc.Instrument.LLMAgentOptions()...)

	return llmagent.New("order-assistant", opts...), nil
}

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }

// ---------------------------------------------------------------------------
// 你的业务工具
// ---------------------------------------------------------------------------

type orderQuery struct {
	OrderID string `json:"order_id" jsonschema:"description=订单号,例如 A123"`
}

type orderInfo struct {
	OrderID  string `json:"order_id"`
	Status   string `json:"status"`
	Item     string `json:"item"`
	City     string `json:"current_city"`
	UpdateAt string `json:"updated_at"`
}

func queryOrderTool() tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, in orderQuery) (orderInfo, error) {
			if in.OrderID == "" {
				return orderInfo{}, fmt.Errorf("order_id 不能为空")
			}
			// 演示用确定性假数据；换成你的 DB/RPC 调用即可。
			sum := 0
			for _, r := range in.OrderID {
				sum += int(r)
			}
			statuses := []string{"已揽收", "运输中", "派送中", "已签收"}
			items := []string{"机械键盘", "显示器", "咖啡豆", "运动鞋"}
			cities := []string{"深圳", "武汉", "上海", "北京"}
			return orderInfo{
				OrderID:  in.OrderID,
				Status:   statuses[sum%len(statuses)],
				Item:     items[sum%len(items)],
				City:     cities[sum%len(cities)],
				UpdateAt: time.Now().Format("2006-01-02 15:04"),
			}, nil
		},
		function.WithName("query_order"),
		function.WithDescription("按订单号查询订单状态、商品与当前所在城市"),
	)
}

type deliveryQuery struct {
	OrderID string `json:"order_id" jsonschema:"description=订单号"`
}

type deliveryInfo struct {
	OrderID string `json:"order_id"`
	ETA     string `json:"eta"`
	Carrier string `json:"carrier"`
}

func estimateDeliveryTool() tool.Tool {
	return function.NewFunctionTool(
		func(_ context.Context, in deliveryQuery) (deliveryInfo, error) {
			sum := 0
			for _, r := range in.OrderID {
				sum += int(r)
			}
			return deliveryInfo{
				OrderID: in.OrderID,
				ETA:     time.Now().Add(time.Duration(24+sum%48) * time.Hour).Format("01月02日"),
				Carrier: []string{"顺丰", "京东物流", "中通"}[sum%3],
			}, nil
		},
		function.WithName("estimate_delivery"),
		function.WithDescription("按订单号预估送达日期与承运商"),
	)
}

// ---------------------------------------------------------------------------
// 领域 mock 模型：让示例离线也能走通完整工具调用链路。
// 接入真实模型（openai provider）后不会用到它。
// ---------------------------------------------------------------------------

var orderIDRe = regexp.MustCompile(`[A-Za-z]{1,3}\d{2,10}`)

type orderMockModel struct{ name string }

func (m *orderMockModel) Info() model.Info { return model.Info{Name: m.name} }

func (m *orderMockModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	go func() {
		defer close(ch)
		select {
		case <-time.After(120 * time.Millisecond):
		case <-ctx.Done():
			return
		}
		ch <- m.respond(req)
	}()
	return ch, nil
}

func (m *orderMockModel) respond(req *model.Request) *model.Response {
	lastUser, lastUserIdx := "", -1
	for i, msg := range req.Messages {
		if msg.Role == model.RoleUser {
			lastUser, lastUserIdx = msg.Content, i
		}
	}
	// 已有工具结果 → 汇总作答。
	var results []string
	for i, msg := range req.Messages {
		if i > lastUserIdx && msg.Role == model.RoleTool {
			results = append(results, fmt.Sprintf("%s 返回 %s", msg.ToolName, strings.TrimSpace(msg.Content)))
		}
	}
	if len(results) > 0 {
		return final(m.name, "根据查询结果：\n- "+strings.Join(results, "\n- "))
	}
	// 领域路由：订单/物流问题 → 工具调用。
	orderID := orderIDRe.FindString(lastUser)
	low := strings.ToLower(lastUser)
	pick := func(name string) *model.Response {
		if orderID == "" {
			return final(m.name, "请提供订单号（例如 A123），我帮你查询。")
		}
		args, _ := json.Marshal(map[string]string{"order_id": orderID})
		return toolCall(m.name, name, args)
	}
	switch {
	case strings.Contains(low, "什么时候") || strings.Contains(low, "送达") || strings.Contains(low, "几天") || strings.Contains(low, "eta"):
		return pick("estimate_delivery")
	case strings.Contains(low, "订单") || strings.Contains(low, "order") || orderID != "":
		return pick("query_order")
	default:
		return final(m.name, "我是订单助手（自定义 Agent 示例）。试试：\"帮我查订单 A123\" 或 \"订单 B456 什么时候送达\"。")
	}
}

func final(name, content string) *model.Response {
	finish := "stop"
	return &model.Response{
		ID: "order-mock-" + uuid.NewString(), Object: model.ObjectTypeChatCompletion,
		Created: time.Now().Unix(), Model: name, Done: true,
		Choices: []model.Choice{{FinishReason: &finish, Message: model.Message{Role: model.RoleAssistant, Content: content}}},
		Usage:   &model.Usage{PromptTokens: 60, CompletionTokens: 30, TotalTokens: 90},
	}
}

func toolCall(name, toolName string, args []byte) *model.Response {
	finish := "tool_calls"
	return &model.Response{
		ID: "order-mock-" + uuid.NewString(), Object: model.ObjectTypeChatCompletion,
		Created: time.Now().Unix(), Model: name, Done: true,
		Choices: []model.Choice{{FinishReason: &finish, Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID: "call_" + uuid.NewString()[:8], Type: "function",
				Function: model.FunctionDefinitionParam{Name: toolName, Arguments: args},
			}},
		}}},
		Usage: &model.Usage{PromptTokens: 50, CompletionTokens: 16, TotalTokens: 66},
	}
}
