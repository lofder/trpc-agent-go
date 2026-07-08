package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Assertion types.
const (
	AssertContains      = "contains"     // final output contains value
	AssertNotContains   = "not_contains" // final output does not contain value
	AssertRegex         = "regex"        // final output matches regex
	AssertToolCalled    = "tool_called"  // a tool with this name was called
	AssertToolNotCalled = "tool_not_called"
	AssertMaxLatencyMS  = "max_latency_ms" // total duration below threshold
	AssertNoError       = "no_error"       // run finished without error
	AssertMaxModelCalls = "max_model_calls"
)

// Assertion is one check evaluated against a finished test run.
type Assertion struct {
	Type  string `json:"type"`
	Value string `json:"value,omitempty"`
}

// TestCase is a runnable scenario: one or more user turns plus assertions.
type TestCase struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Tags        []string    `json:"tags,omitempty"`
	Turns       []string    `json:"turns"` // sequential user inputs in one session
	Assertions  []Assertion `json:"assertions"`
	Source      string      `json:"source"` // manual | imported | generated
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// Validate checks the case is runnable.
func (c *TestCase) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("用例名称不能为空")
	}
	if len(c.Turns) == 0 {
		return fmt.Errorf("用例至少需要一轮用户输入")
	}
	for i, t := range c.Turns {
		if strings.TrimSpace(t) == "" {
			return fmt.Errorf("第 %d 轮输入为空", i+1)
		}
	}
	for i, a := range c.Assertions {
		switch a.Type {
		case AssertContains, AssertNotContains, AssertToolCalled, AssertToolNotCalled:
			if strings.TrimSpace(a.Value) == "" {
				return fmt.Errorf("断言 %d (%s) 需要 value", i+1, a.Type)
			}
		case AssertRegex:
			if _, err := regexp.Compile(a.Value); err != nil {
				return fmt.Errorf("断言 %d 正则不合法: %v", i+1, err)
			}
		case AssertMaxLatencyMS, AssertMaxModelCalls:
			if _, err := parsePositiveInt(a.Value); err != nil {
				return fmt.Errorf("断言 %d (%s) 需要正整数 value: %v", i+1, a.Type, err)
			}
		case AssertNoError:
		default:
			return fmt.Errorf("断言 %d 类型未知: %q", i+1, a.Type)
		}
	}
	return nil
}

func parsePositiveInt(s string) (int, error) {
	var v int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &v); err != nil {
		return 0, err
	}
	if v <= 0 {
		return 0, fmt.Errorf("必须为正整数")
	}
	return v, nil
}

// CaseStore persists test cases to a JSON file under the data directory.
type CaseStore struct {
	mu     sync.Mutex
	path   string
	cases  map[string]*TestCase
	order  []string
	logger *StepLogger
}

// NewCaseStore loads (or seeds) the case store.
func NewCaseStore(dataDir string, logger *StepLogger) (*CaseStore, error) {
	if dataDir == "" {
		dataDir = "testplatform-data"
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	s := &CaseStore{
		path:   filepath.Join(dataDir, "testcases.json"),
		cases:  make(map[string]*TestCase),
		logger: logger,
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	if len(s.cases) == 0 {
		for _, c := range seedCases() {
			cc := c
			s.cases[cc.ID] = &cc
			s.order = append(s.order, cc.ID)
		}
		_ = s.save()
		logger.Infof(CatTestcase, "", "首次启动：已写入 %d 条示例测试用例到 %s", len(s.cases), s.path)
	} else {
		logger.Infof(CatTestcase, "", "已从 %s 加载 %d 条测试用例", s.path, len(s.cases))
	}
	return s, nil
}

func (s *CaseStore) load() error {
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read cases: %w", err)
	}
	var list []*TestCase
	if err := json.Unmarshal(raw, &list); err != nil {
		return fmt.Errorf("parse cases file %s: %w", s.path, err)
	}
	for _, c := range list {
		s.cases[c.ID] = c
		s.order = append(s.order, c.ID)
	}
	return nil
}

func (s *CaseStore) save() error {
	list := s.listLocked()
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *CaseStore) listLocked() []*TestCase {
	out := make([]*TestCase, 0, len(s.order))
	for _, id := range s.order {
		if c, ok := s.cases[id]; ok {
			out = append(out, c)
		}
	}
	return out
}

// List returns all cases in insertion order.
func (s *CaseStore) List() []*TestCase {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.listLocked()
	out := make([]*TestCase, len(list))
	for i, c := range list {
		cp := *c
		out[i] = &cp
	}
	return out
}

// Get returns one case by id.
func (s *CaseStore) Get(id string) (*TestCase, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[id]
	if !ok {
		return nil, false
	}
	cp := *c
	return &cp, true
}

// Count returns the number of cases.
func (s *CaseStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cases)
}

// Create adds a new case.
func (s *CaseStore) Create(c *TestCase) (*TestCase, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.ID == "" {
		c.ID = "case-" + uuid.NewString()[:8]
	}
	if _, exists := s.cases[c.ID]; exists {
		return nil, fmt.Errorf("用例 %s 已存在", c.ID)
	}
	if c.Source == "" {
		c.Source = "manual"
	}
	now := time.Now()
	c.CreatedAt, c.UpdatedAt = now, now
	s.cases[c.ID] = c
	s.order = append(s.order, c.ID)
	if err := s.save(); err != nil {
		return nil, err
	}
	s.logger.Stepf(CatTestcase, "", nil, "新增测试用例 [%s] %s（%d 轮, %d 断言, source=%s）", c.ID, c.Name, len(c.Turns), len(c.Assertions), c.Source)
	cp := *c
	return &cp, nil
}

// Update replaces an existing case.
func (s *CaseStore) Update(id string, c *TestCase) (*TestCase, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.cases[id]
	if !ok {
		return nil, fmt.Errorf("用例 %s 不存在", id)
	}
	c.ID = id
	c.CreatedAt = old.CreatedAt
	c.Source = old.Source
	c.UpdatedAt = time.Now()
	s.cases[id] = c
	if err := s.save(); err != nil {
		return nil, err
	}
	s.logger.Stepf(CatTestcase, "", nil, "更新测试用例 [%s] %s", id, c.Name)
	cp := *c
	return &cp, nil
}

// Delete removes a case.
func (s *CaseStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cases[id]; !ok {
		return fmt.Errorf("用例 %s 不存在", id)
	}
	delete(s.cases, id)
	for i, x := range s.order {
		if x == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	if err := s.save(); err != nil {
		return err
	}
	s.logger.Stepf(CatTestcase, "", nil, "删除测试用例 [%s]", id)
	return nil
}

// Import merges a list of cases (new ids are assigned when missing or
// conflicting), marks them with the given source ("imported"/"generated")
// and returns the number imported.
func (s *CaseStore) Import(list []*TestCase, source string) (int, error) {
	if source == "" {
		source = "imported"
	}
	imported := 0
	for _, c := range list {
		if c == nil {
			continue
		}
		if err := c.Validate(); err != nil {
			return imported, fmt.Errorf("用例 %q 校验失败: %w", c.Name, err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, c := range list {
		if c == nil {
			continue
		}
		if c.ID == "" {
			c.ID = "case-" + uuid.NewString()[:8]
		}
		if _, exists := s.cases[c.ID]; exists {
			c.ID = "case-" + uuid.NewString()[:8]
		}
		c.Source = source
		c.CreatedAt, c.UpdatedAt = now, now
		s.cases[c.ID] = c
		s.order = append(s.order, c.ID)
		imported++
	}
	if err := s.save(); err != nil {
		return imported, err
	}
	s.logger.Stepf(CatTestcase, "", nil, "导入了 %d 条测试用例 (source=%s)", imported, source)
	return imported, nil
}

// ---------------------------------------------------------------------------
// Generators
// ---------------------------------------------------------------------------

// seedCases provides out-of-the-box sample cases.
func seedCases() []TestCase {
	now := time.Now()
	mk := func(id, name, desc string, turns []string, asserts []Assertion) TestCase {
		return TestCase{
			ID: id, Name: name, Description: desc, Turns: turns,
			Assertions: asserts, Source: "manual", CreatedAt: now, UpdatedAt: now,
		}
	}
	return []TestCase{
		mk("case-sample-1", "计算器工具链路", "验证模型会调用 calculator 并给出正确结果",
			[]string{"帮我计算 12 * 34"},
			[]Assertion{
				{Type: AssertToolCalled, Value: "calculator"},
				{Type: AssertContains, Value: "408"},
				{Type: AssertNoError},
			}),
		mk("case-sample-2", "时间查询链路", "验证模型会调用 current_time 工具",
			[]string{"现在几点了？"},
			[]Assertion{
				{Type: AssertToolCalled, Value: "current_time"},
				{Type: AssertNoError},
			}),
		mk("case-sample-3", "多轮上下文记忆", "第二轮问题依赖第一轮的会话历史（观察上下文拼接）",
			[]string{"深圳天气怎么样？", "谢谢，再帮我算 100 / 4"},
			[]Assertion{
				{Type: AssertToolCalled, Value: "get_weather"},
				{Type: AssertToolCalled, Value: "calculator"},
				{Type: AssertContains, Value: "25"},
				{Type: AssertMaxModelCalls, Value: "6"},
			}),
	}
}

// GenerateTemplate creates cases from built-in templates around the demo
// tools. Works offline (mock mode).
func GenerateTemplate(count int) []*TestCase {
	type tpl struct {
		name, desc string
		turns      []string
		asserts    []Assertion
	}
	pool := []tpl{
		{"加法运算", "calculator add", []string{"帮我计算 %d + %d"},
			[]Assertion{{Type: AssertToolCalled, Value: "calculator"}, {Type: AssertNoError}}},
		{"乘法运算", "calculator multiply", []string{"计算 %d * %d 等于多少"},
			[]Assertion{{Type: AssertToolCalled, Value: "calculator"}, {Type: AssertNoError}}},
		{"除法运算", "calculator divide", []string{"帮我算一下 %d / %d"},
			[]Assertion{{Type: AssertToolCalled, Value: "calculator"}, {Type: AssertNoError}}},
		{"时间查询", "current_time", []string{"现在几点了？"},
			[]Assertion{{Type: AssertToolCalled, Value: "current_time"}, {Type: AssertNoError}}},
		{"UTC 时间查询", "current_time UTC", []string{"UTC 时间现在是多少？"},
			[]Assertion{{Type: AssertToolCalled, Value: "current_time"}, {Type: AssertNoError}}},
		{"天气查询", "get_weather", []string{"%s天气怎么样？"},
			[]Assertion{{Type: AssertToolCalled, Value: "get_weather"}, {Type: AssertNoError}}},
		{"闲聊不触发工具", "no tool", []string{"你好，介绍一下你自己"},
			[]Assertion{{Type: AssertToolNotCalled, Value: "calculator"}, {Type: AssertNoError}}},
		{"多轮混合", "multi-turn", []string{"北京天气如何？", "帮我计算 %d + %d"},
			[]Assertion{{Type: AssertToolCalled, Value: "get_weather"}, {Type: AssertToolCalled, Value: "calculator"}, {Type: AssertNoError}}},
	}
	cities := []string{"北京", "上海", "深圳", "杭州", "成都"}
	if count <= 0 {
		count = 5
	}
	out := make([]*TestCase, 0, count)
	for i := 0; i < count; i++ {
		t := pool[i%len(pool)]
		a, b := 3+i*7%50, 2+i*13%40
		turns := make([]string, len(t.turns))
		for j, tt := range t.turns {
			switch strings.Count(tt, "%") {
			case 0:
				turns[j] = tt
			case 1:
				turns[j] = fmt.Sprintf(tt, cities[i%len(cities)])
			default:
				turns[j] = fmt.Sprintf(tt, a, b)
			}
		}
		out = append(out, &TestCase{
			Name:        fmt.Sprintf("%s #%d", t.name, i+1),
			Description: "模板生成: " + t.desc,
			Turns:       turns,
			Assertions:  t.asserts,
			Source:      "generated",
			Tags:        []string{"generated", "template"},
		})
	}
	return out
}

// GenerateWithLLM asks the configured model to produce test cases as JSON.
func GenerateWithLLM(ctx context.Context, m model.Model, count int, focus string) ([]*TestCase, error) {
	if count <= 0 {
		count = 5
	}
	prompt := fmt.Sprintf(`你是一个 Agent 测试工程师。被测智能体拥有这些工具：
- calculator: 数学计算(add/subtract/multiply/divide/power)
- current_time: 查询当前时间
- get_weather: 查询城市天气

请生成 %d 条测试用例，严格输出 JSON 数组（不要 markdown 代码块），每个元素形如:
{"name":"用例名","description":"说明","turns":["用户输入1","用户输入2"],"assertions":[{"type":"tool_called","value":"calculator"},{"type":"contains","value":"期望包含的文本"},{"type":"no_error"}]}
可用断言类型: contains / not_contains / regex / tool_called / tool_not_called / no_error / max_latency_ms / max_model_calls。
测试重点: %s`, count, orDefault(focus, "覆盖每个工具、多轮对话、以及不应触发工具的闲聊场景"))

	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("你只输出合法 JSON，不输出任何其他内容。"),
			model.NewUserMessage(prompt),
		},
	}
	ch, err := m.GenerateContent(ctx, req)
	if err != nil {
		return nil, err
	}
	var content string
	for rsp := range ch {
		if rsp.Error != nil {
			return nil, fmt.Errorf("LLM 生成失败: %s", rsp.Error.Message)
		}
		if len(rsp.Choices) > 0 && !rsp.IsPartial && rsp.Choices[0].Message.Content != "" {
			content = rsp.Choices[0].Message.Content
		}
	}
	list, err := parseGeneratedCases(content)
	if err != nil {
		return nil, err
	}
	return list, nil
}

func parseGeneratedCases(content string) ([]*TestCase, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	start := strings.Index(content, "[")
	end := strings.LastIndex(content, "]")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("模型输出中未找到 JSON 数组: %s", truncate(content, 200))
	}
	var list []*TestCase
	if err := json.Unmarshal([]byte(content[start:end+1]), &list); err != nil {
		return nil, fmt.Errorf("解析生成的用例失败: %w", err)
	}
	valid := list[:0]
	for _, c := range list {
		if c == nil {
			continue
		}
		c.Source = "generated"
		c.Tags = append(c.Tags, "generated", "llm")
		if err := c.Validate(); err == nil {
			valid = append(valid, c)
		}
	}
	if len(valid) == 0 {
		return nil, fmt.Errorf("生成的用例均未通过校验")
	}
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].Name < valid[j].Name })
	return valid, nil
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
