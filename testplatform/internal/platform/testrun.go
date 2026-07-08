package platform

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// AssertionResult is the outcome of one assertion.
type AssertionResult struct {
	Type    string `json:"type"`
	Value   string `json:"value,omitempty"`
	Passed  bool   `json:"passed"`
	Actual  string `json:"actual,omitempty"`
	Message string `json:"message"`
}

// CaseResult is the outcome of one executed test case.
type CaseResult struct {
	CaseID      string            `json:"case_id"`
	CaseName    string            `json:"case_name"`
	Status      string            `json:"status"` // passed | failed | error
	SessionID   string            `json:"session_id"`
	RunIDs      []string          `json:"run_ids"`
	Turns       []string          `json:"turns"`
	Outputs     []string          `json:"outputs"`
	FinalOutput string            `json:"final_output"`
	Error       string            `json:"error,omitempty"`
	DurationMS  int64             `json:"duration_ms"`
	Usage       UsageStat         `json:"usage"`
	Assertions  []AssertionResult `json:"assertions"`
	StartedAt   time.Time         `json:"started_at"`
}

// TestRun is one batch execution over selected cases.
type TestRun struct {
	ID         string       `json:"id"`
	Status     string       `json:"status"` // running | completed | cancelled
	Total      int          `json:"total"`
	Done       int          `json:"done"`
	Passed     int          `json:"passed"`
	Failed     int          `json:"failed"`
	Errored    int          `json:"errored"`
	StartedAt  time.Time    `json:"started_at"`
	EndedAt    *time.Time   `json:"ended_at,omitempty"`
	DurationMS int64        `json:"duration_ms"`
	Results    []CaseResult `json:"results"`
}

// TestRunManager executes batches sequentially and stores results.
type TestRunManager struct {
	p  *Platform
	mu sync.Mutex

	runs      map[string]*TestRun
	order     []string
	cancelled map[string]*atomic.Bool
}

// NewTestRunManager creates the manager.
func NewTestRunManager(p *Platform) *TestRunManager {
	return &TestRunManager{
		p:         p,
		runs:      make(map[string]*TestRun),
		cancelled: make(map[string]*atomic.Bool),
	}
}

// List returns test runs, newest first.
func (m *TestRunManager) List() []*TestRun {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*TestRun, 0, len(m.order))
	for i := len(m.order) - 1; i >= 0; i-- {
		if tr, ok := m.runs[m.order[i]]; ok {
			cp := *tr
			out = append(out, &cp)
		}
	}
	return out
}

// Get returns one test run.
func (m *TestRunManager) Get(id string) (*TestRun, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tr, ok := m.runs[id]
	if !ok {
		return nil, false
	}
	cp := *tr
	cp.Results = append([]CaseResult(nil), tr.Results...)
	return &cp, true
}

// Cancel marks a running batch as cancelled (takes effect between cases).
func (m *TestRunManager) Cancel(id string) bool {
	m.mu.Lock()
	flag, ok := m.cancelled[id]
	m.mu.Unlock()
	if ok {
		flag.Store(true)
	}
	return ok
}

// Start launches a batch over the given case ids (all cases when empty).
func (m *TestRunManager) Start(caseIDs []string) (*TestRun, error) {
	var cases []*TestCase
	if len(caseIDs) == 0 {
		cases = m.p.Cases.List()
	} else {
		for _, id := range caseIDs {
			c, ok := m.p.Cases.Get(id)
			if !ok {
				return nil, fmt.Errorf("用例 %s 不存在", id)
			}
			cases = append(cases, c)
		}
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("没有可运行的测试用例")
	}

	tr := &TestRun{
		ID:        "testrun-" + uuid.NewString()[:8],
		Status:    StatusRunning,
		Total:     len(cases),
		StartedAt: time.Now(),
	}
	flag := &atomic.Bool{}
	m.mu.Lock()
	m.runs[tr.ID] = tr
	m.order = append(m.order, tr.ID)
	if len(m.order) > 100 {
		old := m.order[0]
		m.order = m.order[1:]
		delete(m.runs, old)
		delete(m.cancelled, old)
	}
	m.cancelled[tr.ID] = flag
	m.mu.Unlock()

	m.p.Logger.Stepf(CatTestcase, "", map[string]any{"testrun": tr.ID, "total": tr.Total},
		"🧪 批量测试启动 [%s]：共 %d 条用例", tr.ID, tr.Total)
	m.publish(tr.ID)

	go m.execute(tr.ID, cases, flag)
	cp := *tr
	return &cp, nil
}

func (m *TestRunManager) publish(id string) {
	if tr, ok := m.Get(id); ok {
		m.p.Hub.Publish("testrun_update", tr)
	}
}

func (m *TestRunManager) update(id string, fn func(*TestRun)) {
	m.mu.Lock()
	if tr, ok := m.runs[id]; ok {
		fn(tr)
	}
	m.mu.Unlock()
}

func (m *TestRunManager) execute(trID string, cases []*TestCase, cancelled *atomic.Bool) {
	for i, c := range cases {
		if cancelled.Load() {
			m.p.Logger.Warnf(CatTestcase, "", "批量测试 [%s] 已取消（%d/%d 完成）", trID, i, len(cases))
			break
		}
		m.p.Logger.Stepf(CatTestcase, "", nil, "▶ [%s] 运行用例 %d/%d: %s", trID, i+1, len(cases), c.Name)
		res := m.runCase(c)
		m.update(trID, func(tr *TestRun) {
			tr.Results = append(tr.Results, res)
			tr.Done++
			switch res.Status {
			case "passed":
				tr.Passed++
			case "failed":
				tr.Failed++
			default:
				tr.Errored++
			}
		})
		m.p.Logger.Stepf(CatTestcase, "", map[string]any{
			"case": c.ID, "status": res.Status, "duration_ms": res.DurationMS,
		}, "◀ 用例 [%s] %s → %s", c.Name, c.ID, statusIcon(res.Status))
		m.publish(trID)
	}
	now := time.Now()
	m.update(trID, func(tr *TestRun) {
		if cancelled.Load() {
			tr.Status = StatusCancelled
		} else {
			tr.Status = StatusCompleted
		}
		tr.EndedAt = &now
		tr.DurationMS = now.Sub(tr.StartedAt).Milliseconds()
	})
	tr, _ := m.Get(trID)
	m.p.Logger.Stepf(CatTestcase, "", map[string]any{
		"passed": tr.Passed, "failed": tr.Failed, "errored": tr.Errored,
	}, "🏁 批量测试完成 [%s]：通过 %d / 失败 %d / 错误 %d（共 %d）", trID, tr.Passed, tr.Failed, tr.Errored, tr.Total)
	m.publish(trID)
}

func statusIcon(s string) string {
	switch s {
	case "passed":
		return "✅ 通过"
	case "failed":
		return "❌ 失败"
	default:
		return "💥 错误"
	}
}

// runCase executes one case in a fresh session, waiting for each turn.
func (m *TestRunManager) runCase(c *TestCase) CaseResult {
	res := CaseResult{
		CaseID:    c.ID,
		CaseName:  c.Name,
		SessionID: fmt.Sprintf("case-%s-%s", c.ID, uuid.NewString()[:6]),
		Turns:     c.Turns,
		StartedAt: time.Now(),
	}
	start := time.Now()
	var lastErr string
	toolNames := map[string]bool{}
	modelCalls := 0
	hadError := false

	for _, turn := range c.Turns {
		runID, done, err := m.p.StartRun(RunRequest{
			Source:    "testcase",
			CaseID:    c.ID,
			CaseName:  c.Name,
			UserID:    "case-runner",
			SessionID: res.SessionID,
			Input:     turn,
		})
		if err != nil {
			res.Status = "error"
			res.Error = err.Error()
			res.DurationMS = time.Since(start).Milliseconds()
			return res
		}
		res.RunIDs = append(res.RunIDs, runID)
		select {
		case <-done:
		case <-time.After(10 * time.Minute):
			res.Status = "error"
			res.Error = "运行超时(10分钟)"
			res.DurationMS = time.Since(start).Milliseconds()
			return res
		}
		run, ok := m.p.Store.GetRun(runID)
		if !ok {
			res.Status = "error"
			res.Error = "运行记录丢失"
			res.DurationMS = time.Since(start).Milliseconds()
			return res
		}
		res.Outputs = append(res.Outputs, run.FinalOutput)
		res.FinalOutput = run.FinalOutput
		res.Usage.PromptTokens += run.Usage.PromptTokens
		res.Usage.CompletionTokens += run.Usage.CompletionTokens
		res.Usage.TotalTokens += run.Usage.TotalTokens
		modelCalls += run.ModelCalls
		for _, tn := range run.ToolNames {
			toolNames[tn] = true
		}
		if run.Status == StatusError {
			hadError = true
			lastErr = run.Error
		}
	}
	res.DurationMS = time.Since(start).Milliseconds()

	// Evaluate assertions against the collected facts.
	allOutput := strings.Join(res.Outputs, "\n")
	passedAll := true
	for _, a := range c.Assertions {
		ar := evalAssertion(a, allOutput, res.FinalOutput, toolNames, res.DurationMS, modelCalls, hadError, lastErr)
		if !ar.Passed {
			passedAll = false
		}
		res.Assertions = append(res.Assertions, ar)
		m.p.Logger.Stepf(CatTestcase, "", nil, "   断言 %s(%s) → %v %s", a.Type, a.Value, ar.Passed, ar.Message)
	}
	if hadError && res.Error == "" {
		res.Error = lastErr
	}
	if passedAll {
		res.Status = "passed"
	} else {
		res.Status = "failed"
	}
	return res
}

func evalAssertion(a Assertion, allOutput, finalOutput string, tools map[string]bool, durationMS int64, modelCalls int, hadError bool, lastErr string) AssertionResult {
	ar := AssertionResult{Type: a.Type, Value: a.Value}
	switch a.Type {
	case AssertContains:
		ar.Passed = strings.Contains(allOutput, a.Value)
		ar.Actual = truncate(finalOutput, 160)
		ar.Message = fmt.Sprintf("输出%s包含 %q", boolWord(ar.Passed), a.Value)
	case AssertNotContains:
		ar.Passed = !strings.Contains(allOutput, a.Value)
		ar.Actual = truncate(finalOutput, 160)
		if ar.Passed {
			ar.Message = fmt.Sprintf("输出未包含 %q（符合期望）", a.Value)
		} else {
			ar.Message = fmt.Sprintf("输出包含 %q（期望不包含）", a.Value)
		}
	case AssertRegex:
		re, err := regexp.Compile(a.Value)
		if err != nil {
			ar.Passed = false
			ar.Message = "正则不合法: " + err.Error()
			break
		}
		ar.Passed = re.MatchString(allOutput)
		ar.Actual = truncate(finalOutput, 160)
		ar.Message = fmt.Sprintf("输出%s匹配 /%s/", boolWord(ar.Passed), a.Value)
	case AssertToolCalled:
		ar.Passed = tools[a.Value]
		ar.Actual = toolList(tools)
		ar.Message = fmt.Sprintf("工具 %s %s被调用", a.Value, boolWord(ar.Passed))
	case AssertToolNotCalled:
		ar.Passed = !tools[a.Value]
		ar.Actual = toolList(tools)
		if ar.Passed {
			ar.Message = fmt.Sprintf("工具 %s 未被调用（符合期望）", a.Value)
		} else {
			ar.Message = fmt.Sprintf("工具 %s 被调用（期望不调用）", a.Value)
		}
	case AssertMaxLatencyMS:
		limit, _ := parsePositiveInt(a.Value)
		ar.Passed = durationMS <= int64(limit)
		ar.Actual = fmt.Sprintf("%dms", durationMS)
		ar.Message = fmt.Sprintf("耗时 %dms（上限 %dms）", durationMS, limit)
	case AssertMaxModelCalls:
		limit, _ := parsePositiveInt(a.Value)
		ar.Passed = modelCalls <= limit
		ar.Actual = fmt.Sprintf("%d", modelCalls)
		ar.Message = fmt.Sprintf("模型调用 %d 次（上限 %d）", modelCalls, limit)
	case AssertNoError:
		ar.Passed = !hadError
		ar.Actual = lastErr
		ar.Message = fmt.Sprintf("运行%s出错", boolWord(hadError))
	default:
		ar.Passed = false
		ar.Message = "未知断言类型"
	}
	return ar
}

func boolWord(b bool) string {
	if b {
		return ""
	}
	return "未"
}

func toolList(tools map[string]bool) string {
	if len(tools) == 0 {
		return "(无工具调用)"
	}
	names := make([]string, 0, len(tools))
	for n := range tools {
		names = append(names, n)
	}
	return strings.Join(names, ",")
}
