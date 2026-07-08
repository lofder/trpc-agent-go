package platform

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	agentpkg "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	toolpkg "trpc.group/trpc-go/trpc-agent-go/tool"
)

// AppName is the runner application name.
const AppName = "agent-test-platform"

// DefaultUserID is used for playground sessions.
const DefaultUserID = "tester"

// BuiltinAgentName is the registry key of the built-in demo agent.
const BuiltinAgentName = "builtin-demo"

// ModelSettings is the runtime-configurable model / agent configuration.
type ModelSettings struct {
	Provider    string  `json:"provider"` // mock | openai
	Model       string  `json:"model"`
	BaseURL     string  `json:"base_url,omitempty"`
	APIKey      string  `json:"-"` // never serialized back to the UI
	APIKeySet   bool    `json:"api_key_set"`
	Streaming   bool    `json:"streaming"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
	AgentName   string  `json:"agent_name"`
	Instruction string  `json:"instruction"`
	Description string  `json:"description"`
	// ActiveAgent selects which registered agent builder is under test.
	ActiveAgent string `json:"active_agent"`
	// AvailableAgents lists registered builders (output only, ignored on input).
	AvailableAgents []string `json:"available_agents,omitempty"`
}

// Instrumentation bundles the three callback groups that wire an agent into
// the platform (tracing, context provenance, step logs, breakpoints). Attach
// all three to your agent; if you already have your own callbacks, register
// them onto these objects instead (callbacks run in registration order).
type Instrumentation struct {
	Agent *agentpkg.Callbacks
	Model *model.Callbacks
	Tool  *toolpkg.Callbacks
}

// LLMAgentOptions returns the three llmagent options in one slice, so
// attaching the platform to your agent is a single append:
//
//	opts = append(opts, bc.Instrument.LLMAgentOptions()...)
func (i Instrumentation) LLMAgentOptions() []llmagent.Option {
	return []llmagent.Option{
		llmagent.WithAgentCallbacks(i.Agent),
		llmagent.WithModelCallbacks(i.Model),
		llmagent.WithToolCallbacks(i.Tool),
	}
}

// BuildContext is what an AgentBuilder receives on every (re)build: the
// current platform settings, a model constructed from those settings (use it
// to make the UI's provider/model switch work for your agent, or ignore it
// and use your own), and the instrumentation that must be attached.
type BuildContext struct {
	Settings   ModelSettings
	Model      model.Model
	Instrument Instrumentation
}

// AgentBuilder constructs the agent under test. It is invoked on startup and
// whenever settings change or the active agent is switched, so iterating on
// your agent never requires touching platform code.
type AgentBuilder func(bc BuildContext) (agentpkg.Agent, error)

// SessionMeta tracks sessions created through the platform.
type SessionMeta struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	LastInput string    `json:"last_input"`
	RunCount  int       `json:"run_count"`
}

// Platform owns all subsystems: instrumented runner, traces, logs,
// breakpoints, test cases.
type Platform struct {
	Logger *StepLogger
	Hub    *Hub
	Store  *TraceStore
	Breaks *BreakpointManager
	Coll   *Collector
	Cases  *CaseStore
	Tests  *TestRunManager

	sessions session.Service

	mu              sync.Mutex
	settings        ModelSettings
	runner          runner.Runner
	model           model.Model
	startedAt       time.Time
	builders        map[string]AgentBuilder
	builderOrder    []string
	activeAgentName string // Info().Name of the built agent under test

	runCancelMu sync.Mutex
	runCancels  map[string]context.CancelFunc

	sessMu   sync.Mutex
	sessMeta map[string]*SessionMeta
}

// DefaultSettings derives the initial model settings from the environment.
func DefaultSettings() ModelSettings {
	s := ModelSettings{
		Provider:    "mock",
		Model:       "mock-llm",
		Streaming:   true,
		Temperature: 0.7,
		MaxTokens:   2000,
		AgentName:   "chat-assistant",
		Instruction: "你是一个乐于助人的中文智能助手。可以使用 calculator 做数学计算、current_time 查询时间、get_weather 查询天气。需要时优先调用工具，保持回答简洁。",
		Description: "带计算器/时间/天气工具的演示智能体（被测 Agent）",
		ActiveAgent: BuiltinAgentName,
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		s.Provider = "openai"
		s.APIKey = key
		s.APIKeySet = true
		s.Model = envOr("MODEL_NAME", "deepseek-v4-flash")
		s.BaseURL = os.Getenv("OPENAI_BASE_URL")
	}
	return s
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// New assembles the platform.
func New(dataDir string, maxRuns int) (*Platform, error) {
	hub := NewHub()
	logger := NewStepLogger(hub, 8000)
	store := NewTraceStore(maxRuns)
	breaks := NewBreakpointManager(hub, logger, 15*time.Minute)

	p := &Platform{
		Logger:       logger,
		Hub:          hub,
		Store:        store,
		Breaks:       breaks,
		sessions:     sessioninmemory.NewSessionService(),
		settings:     DefaultSettings(),
		startedAt:    time.Now(),
		runCancels:   make(map[string]context.CancelFunc),
		sessMeta:     make(map[string]*SessionMeta),
		builders:     map[string]AgentBuilder{BuiltinAgentName: BuiltinDemoBuilder},
		builderOrder: []string{BuiltinAgentName},
	}
	p.Coll = NewCollector(store, logger, hub, breaks, func() string {
		p.mu.Lock()
		defer p.mu.Unlock()
		// Exact-match provenance annotation only applies to the built-in
		// agent, whose instruction is owned by the settings page. Custom
		// builders own their instruction; return "" so the analysis stays
		// generic instead of comparing against an unrelated string.
		if p.settings.ActiveAgent == BuiltinAgentName {
			return p.settings.Instruction
		}
		return ""
	})

	cases, err := NewCaseStore(dataDir, logger)
	if err != nil {
		return nil, err
	}
	p.Cases = cases
	p.Tests = NewTestRunManager(p)

	if err := p.rebuildLocked(); err != nil {
		return nil, err
	}
	return p, nil
}

// RegisterAgent adds an agent builder under the given name. Call it before
// serving (or any time: the builder takes effect once selected). Registering
// does not switch the agent under test; use SetActiveAgent or the settings
// page for that.
func (p *Platform) RegisterAgent(name string, b AgentBuilder) error {
	if strings.TrimSpace(name) == "" || b == nil {
		return fmt.Errorf("agent name and builder are required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, dup := p.builders[name]; dup {
		return fmt.Errorf("agent %q already registered", name)
	}
	p.builders[name] = b
	p.builderOrder = append(p.builderOrder, name)
	p.Logger.Infof(CatServer, "", "已注册被测 Agent 构建器: %s (共 %d 个)", name, len(p.builders))
	return nil
}

// SetActiveAgent switches the agent under test and rebuilds.
func (p *Platform) SetActiveAgent(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.builders[name]; !ok {
		return fmt.Errorf("agent %q 未注册 (可选: %s)", name, strings.Join(p.builderOrder, ", "))
	}
	old := p.settings.ActiveAgent
	p.settings.ActiveAgent = name
	if err := p.rebuildLocked(); err != nil {
		p.settings.ActiveAgent = old
		_ = p.rebuildLocked()
		return err
	}
	p.Hub.Publish("status", p.statusLocked())
	return nil
}

// AgentNames lists registered builders in registration order.
func (p *Platform) AgentNames() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.builderOrder...)
}

// Settings returns a copy of the current settings.
func (p *Platform) Settings() ModelSettings {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.settings
	s.APIKeySet = s.APIKey != ""
	s.AvailableAgents = append([]string(nil), p.builderOrder...)
	return s
}

// UpdateSettings applies new settings and rebuilds the instrumented agent.
func (p *Platform) UpdateSettings(ns ModelSettings) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ns.Provider != "mock" && ns.Provider != "openai" {
		return fmt.Errorf("unknown provider %q (supported: mock, openai)", ns.Provider)
	}
	if ns.Model == "" {
		return fmt.Errorf("model name is required")
	}
	if ns.AgentName == "" {
		ns.AgentName = p.settings.AgentName
	}
	if ns.APIKey == "" { // keep the existing key unless a new one is provided
		ns.APIKey = p.settings.APIKey
	}
	if ns.ActiveAgent == "" {
		ns.ActiveAgent = p.settings.ActiveAgent
	}
	if _, ok := p.builders[ns.ActiveAgent]; !ok {
		return fmt.Errorf("agent %q 未注册 (可选: %s)", ns.ActiveAgent, strings.Join(p.builderOrder, ", "))
	}
	ns.AvailableAgents = nil
	old := p.settings
	p.settings = ns
	if err := p.rebuildLocked(); err != nil {
		p.settings = old
		_ = p.rebuildLocked()
		return err
	}
	p.Logger.Log(LevelStep, CatServer, "", fmt.Sprintf(
		"模型/Agent 配置已更新: provider=%s model=%s streaming=%v agent=%s",
		ns.Provider, ns.Model, ns.Streaming, ns.ActiveAgent), nil)
	p.Hub.Publish("status", p.statusLocked())
	return nil
}

// buildModel constructs the model from settings.
func buildModel(s ModelSettings) model.Model {
	switch s.Provider {
	case "openai":
		var opts []openaimodel.Option
		if s.BaseURL != "" {
			opts = append(opts, openaimodel.WithBaseURL(s.BaseURL))
		}
		if s.APIKey != "" {
			opts = append(opts, openaimodel.WithAPIKey(s.APIKey))
		}
		return openaimodel.New(s.Model, opts...)
	default:
		return NewMockModel(s.Model, 150*time.Millisecond)
	}
}

// BuiltinDemoBuilder assembles the built-in demo agent (calculator /
// current_time / get_weather). It doubles as the reference implementation
// for integrating your own agent: build it however you like, then append
// bc.Instrument.LLMAgentOptions().
func BuiltinDemoBuilder(bc BuildContext) (agentpkg.Agent, error) {
	s := bc.Settings
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(s.MaxTokens),
		Temperature: floatPtr(s.Temperature),
		Stream:      s.Streaming,
	}
	opts := []llmagent.Option{
		llmagent.WithModel(bc.Model),
		llmagent.WithDescription(s.Description),
		llmagent.WithInstruction(s.Instruction),
		llmagent.WithGenerationConfig(genCfg),
		llmagent.WithTools(BuildDemoTools()),
	}
	opts = append(opts, bc.Instrument.LLMAgentOptions()...)
	return llmagent.New(s.AgentName, opts...), nil
}

// instrumentation builds a fresh set of platform callbacks.
func (p *Platform) instrumentation() Instrumentation {
	return Instrumentation{
		Agent: p.Coll.AgentCallbacks(),
		Model: p.Coll.ModelCallbacks(),
		Tool:  p.Coll.ToolCallbacks(),
	}
}

// rebuildLocked (re)creates the model, agent and runner via the active
// builder. Caller holds p.mu.
func (p *Platform) rebuildLocked() error {
	s := p.settings
	m := buildModel(s)

	build := p.builders[s.ActiveAgent]
	if build == nil {
		build = BuiltinDemoBuilder
	}
	ag, err := build(BuildContext{Settings: s, Model: m, Instrument: p.instrumentation()})
	if err != nil {
		return fmt.Errorf("构建被测 Agent %q 失败: %w", s.ActiveAgent, err)
	}
	if ag == nil {
		return fmt.Errorf("构建被测 Agent %q 返回 nil", s.ActiveAgent)
	}
	p.model = m
	p.activeAgentName = ag.Info().Name
	if p.runner != nil {
		_ = p.runner.Close()
	}
	p.runner = runner.NewRunner(AppName, ag, runner.WithSessionService(p.sessions))

	toolNames := make([]string, 0, len(ag.Tools()))
	for _, t := range ag.Tools() {
		if d := t.Declaration(); d != nil {
			toolNames = append(toolNames, d.Name)
		}
	}
	p.Logger.Log(LevelStep, CatServer, "", fmt.Sprintf(
		"被测 Agent 已构建: builder=%s name=%s provider=%s model=%s tools=%v 埋点=[BeforeAgent/AfterAgent BeforeModel/AfterModel BeforeTool/AfterTool]",
		s.ActiveAgent, p.activeAgentName, s.Provider, s.Model, toolNames), nil)
	return nil
}

// CurrentModel returns the active model (used by the LLM test-case generator).
func (p *Platform) CurrentModel() (model.Model, ModelSettings) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.model, p.settings
}

func (p *Platform) statusLocked() map[string]any {
	s := p.settings
	agentName := p.activeAgentName
	if agentName == "" {
		agentName = s.AgentName
	}
	return map[string]any{
		"app_name":     AppName,
		"agent_name":   agentName,
		"active_agent": s.ActiveAgent,
		"agents":       append([]string(nil), p.builderOrder...),
		"provider":     s.Provider,
		"model":        s.Model,
		"streaming":    s.Streaming,
		"api_key_set":  s.APIKey != "",
		"started_at":   p.startedAt,
		"uptime_sec":   int(time.Since(p.startedAt).Seconds()),
	}
}

// Status returns dashboard status info.
func (p *Platform) Status() map[string]any {
	p.mu.Lock()
	st := p.statusLocked()
	p.mu.Unlock()
	st["runs"] = p.Store.Stats()
	st["breakpoints"] = p.Breaks.State()
	st["cases"] = p.Cases.Count()
	st["subscribers"] = p.Hub.SubscriberCount()
	return st
}

// Sessions lists sessions seen by the platform, newest first.
func (p *Platform) Sessions() []*SessionMeta {
	p.sessMu.Lock()
	defer p.sessMu.Unlock()
	out := make([]*SessionMeta, 0, len(p.sessMeta))
	for _, sm := range p.sessMeta {
		cp := *sm
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (p *Platform) touchSession(sessionID, userID, input string) {
	p.sessMu.Lock()
	defer p.sessMu.Unlock()
	sm, ok := p.sessMeta[sessionID]
	if !ok {
		sm = &SessionMeta{ID: sessionID, UserID: userID, CreatedAt: time.Now()}
		p.sessMeta[sessionID] = sm
	}
	sm.LastInput = truncate(input, 80)
	sm.RunCount++
}

// SessionTranscript returns the stored events of a session as simple views.
func (p *Platform) SessionTranscript(userID, sessionID string) ([]map[string]any, error) {
	sess, err := p.sessions.GetSession(context.Background(), session.Key{
		AppName: AppName, UserID: userID, SessionID: sessionID,
	})
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()
	out := make([]map[string]any, 0, len(sess.Events))
	for i := range sess.Events {
		e := &sess.Events[i]
		if e.Response == nil || len(e.Response.Choices) == 0 || e.Response.IsPartial {
			continue
		}
		m := e.Response.Choices[0].Message
		view := map[string]any{
			"event_id":      e.ID,
			"invocation_id": e.InvocationID,
			"author":        e.Author,
			"time":          e.Timestamp,
			"role":          string(m.Role),
			"content":       m.Content,
			"object":        e.Response.Object,
		}
		if len(m.ToolCalls) > 0 {
			view["tool_calls"] = toolCallViews(m.ToolCalls)
		}
		if m.ToolID != "" {
			view["tool_id"] = m.ToolID
			view["tool_name"] = m.ToolName
		}
		out = append(out, view)
	}
	return out, nil
}

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }
