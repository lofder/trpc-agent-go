package platform

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// NewHTTPHandler builds the platform's HTTP mux. webFS serves the embedded
// single-page UI.
func (p *Platform) NewHTTPHandler(webFS fs.FS) http.Handler {
	mux := http.NewServeMux()

	// --- Observability -----------------------------------------------------
	mux.HandleFunc("GET /api/status", p.handleStatus)
	mux.HandleFunc("GET /api/stream", p.handleStream)
	mux.HandleFunc("GET /api/logs", p.handleLogs)
	mux.HandleFunc("GET /api/runs", p.handleRuns)
	mux.HandleFunc("GET /api/runs/{id}", p.handleRunDetail)
	mux.HandleFunc("POST /api/runs/{id}/cancel", p.handleRunCancel)
	mux.HandleFunc("GET /api/context-rules", p.handleContextRules)

	// --- Chat / playground --------------------------------------------------
	mux.HandleFunc("POST /api/chat", p.handleChat)
	mux.HandleFunc("GET /api/sessions", p.handleSessions)
	mux.HandleFunc("GET /api/sessions/{id}", p.handleSessionDetail)

	// --- Settings -----------------------------------------------------------
	mux.HandleFunc("GET /api/settings", p.handleGetSettings)
	mux.HandleFunc("POST /api/settings", p.handleUpdateSettings)

	// --- Breakpoints / interventions ---------------------------------------
	mux.HandleFunc("GET /api/breakpoints", p.handleGetBreakpoints)
	mux.HandleFunc("POST /api/breakpoints", p.handleSetBreakpoints)
	mux.HandleFunc("GET /api/interventions", p.handleInterventions)
	mux.HandleFunc("POST /api/interventions/{id}", p.handleResolveIntervention)

	// --- Test cases ----------------------------------------------------------
	mux.HandleFunc("GET /api/testcases", p.handleListCases)
	mux.HandleFunc("POST /api/testcases", p.handleCreateCase)
	mux.HandleFunc("PUT /api/testcases/{id}", p.handleUpdateCase)
	mux.HandleFunc("DELETE /api/testcases/{id}", p.handleDeleteCase)
	mux.HandleFunc("POST /api/testcases/import", p.handleImportCases)
	mux.HandleFunc("GET /api/testcases/export", p.handleExportCases)
	mux.HandleFunc("POST /api/testcases/generate", p.handleGenerateCases)

	// --- Test runs -----------------------------------------------------------
	mux.HandleFunc("POST /api/testruns", p.handleStartTestRun)
	mux.HandleFunc("GET /api/testruns", p.handleListTestRuns)
	mux.HandleFunc("GET /api/testruns/{id}", p.handleTestRunDetail)
	mux.HandleFunc("POST /api/testruns/{id}/cancel", p.handleCancelTestRun)

	// --- Static UI -----------------------------------------------------------
	fileServer := http.FileServer(http.FS(webFS))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			if _, err := fs.Stat(webFS, strings.TrimPrefix(r.URL.Path, "/")); err != nil {
				http.NotFound(w, r)
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	})

	return p.accessLog(mux)
}

// accessLog prints one line per HTTP request (every step is logged).
func (p *Platform) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		// SSE requests block until disconnect; log them on entry only.
		if r.URL.Path == "/api/stream" {
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			p.Logger.Infof(CatHTTP, "", "%s %s (%.1fms)", r.Method, r.URL.Path, float64(time.Since(start).Microseconds())/1000)
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return fmt.Errorf("empty request body")
	}
	return json.Unmarshal(body, v)
}

// ---------------------------------------------------------------------------
// Observability handlers
// ---------------------------------------------------------------------------

func (p *Platform) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, p.Status())
}

func (p *Platform) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, cancel := p.Hub.Subscribe()
	defer cancel()
	p.Logger.Infof(CatHTTP, "", "SSE 客户端接入 (当前 %d 个订阅)", p.Hub.SubscriberCount())

	// Initial snapshot so the UI can render immediately.
	snapshot, _ := json.Marshal(HubMessage{Type: "hello", Time: time.Now(), Data: p.Status()})
	fmt.Fprintf(w, "data: %s\n\n", snapshot)
	flusher.Flush()

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

func (p *Platform) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 500
	}
	writeJSON(w, http.StatusOK, p.Logger.Recent(limit, q.Get("level"), q.Get("category"), q.Get("run_id")))
}

func (p *Platform) handleRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	writeJSON(w, http.StatusOK, p.Store.ListRuns(limit))
}

func (p *Platform) handleRunDetail(w http.ResponseWriter, r *http.Request) {
	run, ok := p.Store.GetRun(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, fmt.Errorf("run not found"))
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (p *Platform) handleRunCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !p.CancelRun(id) {
		writeErr(w, http.StatusNotFound, fmt.Errorf("run %s 不在运行中", id))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
}

func (p *Platform) handleContextRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ContextRules())
}

// ---------------------------------------------------------------------------
// Chat handlers
// ---------------------------------------------------------------------------

func (p *Platform) handleChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message   string `json:"message"`
		SessionID string `json:"session_id"`
		UserID    string `json:"user_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	runID, _, err := p.StartRun(RunRequest{
		Source:    "playground",
		UserID:    req.UserID,
		SessionID: req.SessionID,
		Input:     req.Message,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sum, _ := p.Store.SummaryFor(runID)
	writeJSON(w, http.StatusOK, map[string]any{
		"run_id":     runID,
		"session_id": sum.SessionID,
	})
}

func (p *Platform) handleSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, p.Sessions())
}

func (p *Platform) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		userID = DefaultUserID
		for _, sm := range p.Sessions() {
			if sm.ID == id {
				userID = sm.UserID
				break
			}
		}
	}
	transcript, err := p.SessionTranscript(userID, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session_id": id, "user_id": userID, "events": transcript})
}

// ---------------------------------------------------------------------------
// Settings handlers
// ---------------------------------------------------------------------------

func (p *Platform) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, p.Settings())
}

func (p *Platform) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelSettings
		APIKey string `json:"api_key"`
	}
	cur := p.Settings()
	req.ModelSettings = cur
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ns := req.ModelSettings
	ns.APIKey = req.APIKey // empty => keep old (handled in UpdateSettings)
	if err := p.UpdateSettings(ns); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, p.Settings())
}

// ---------------------------------------------------------------------------
// Breakpoint handlers
// ---------------------------------------------------------------------------

func (p *Platform) handleGetBreakpoints(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, p.Breaks.State())
}

func (p *Platform) handleSetBreakpoints(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelEnabled bool `json:"model_enabled"`
		ToolEnabled  bool `json:"tool_enabled"`
		TimeoutSec   int  `json:"timeout_sec"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p.Breaks.Configure(req.ModelEnabled, req.ToolEnabled, req.TimeoutSec)
	writeJSON(w, http.StatusOK, p.Breaks.State())
}

func (p *Platform) handleInterventions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, p.Breaks.Pending())
}

func (p *Platform) handleResolveIntervention(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var res Resolution
	if err := readJSON(r, &res); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := p.Breaks.Resolve(id, res); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resolved": true, "action": res.Action})
}

// ---------------------------------------------------------------------------
// Test case handlers
// ---------------------------------------------------------------------------

func (p *Platform) handleListCases(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, p.Cases.List())
}

func (p *Platform) handleCreateCase(w http.ResponseWriter, r *http.Request) {
	var c TestCase
	if err := readJSON(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	created, err := p.Cases.Create(&c)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, created)
}

func (p *Platform) handleUpdateCase(w http.ResponseWriter, r *http.Request) {
	var c TestCase
	if err := readJSON(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	updated, err := p.Cases.Update(r.PathValue("id"), &c)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (p *Platform) handleDeleteCase(w http.ResponseWriter, r *http.Request) {
	if err := p.Cases.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (p *Platform) handleImportCases(w http.ResponseWriter, r *http.Request) {
	var list []*TestCase
	if err := readJSON(r, &list); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("导入格式应为用例 JSON 数组: %w", err))
		return
	}
	n, err := p.Cases.Import(list, "imported")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": n})
}

func (p *Platform) handleExportCases(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Disposition", "attachment; filename=testcases.json")
	writeJSON(w, http.StatusOK, p.Cases.List())
}

func (p *Platform) handleGenerateCases(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Count int    `json:"count"`
		Mode  string `json:"mode"` // auto | llm | template
		Focus string `json:"focus"`
		Save  *bool  `json:"save"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	m, settings := p.CurrentModel()
	mode := req.Mode
	if mode == "" || mode == "auto" {
		if settings.Provider == "mock" {
			mode = "template"
		} else {
			mode = "llm"
		}
	}
	p.Logger.Stepf(CatTestcase, "", nil, "生成测试用例: mode=%s count=%d focus=%q", mode, req.Count, req.Focus)

	var list []*TestCase
	var err error
	if mode == "llm" {
		list, err = GenerateWithLLM(r.Context(), m, req.Count, req.Focus)
		if err != nil {
			p.Logger.Warnf(CatTestcase, "", "LLM 生成失败(%v)，回退到模板生成", err)
			list = GenerateTemplate(req.Count)
			mode = "template(llm fallback)"
		}
	} else {
		list = GenerateTemplate(req.Count)
	}

	saved := 0
	if req.Save == nil || *req.Save {
		saved, err = p.Cases.Import(list, "generated")
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"mode": mode, "generated": len(list), "saved": saved, "cases": list})
}

// ---------------------------------------------------------------------------
// Test run handlers
// ---------------------------------------------------------------------------

func (p *Platform) handleStartTestRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CaseIDs []string `json:"case_ids"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	tr, err := p.Tests.Start(req.CaseIDs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, tr)
}

func (p *Platform) handleListTestRuns(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, p.Tests.List())
}

func (p *Platform) handleTestRunDetail(w http.ResponseWriter, r *http.Request) {
	tr, ok := p.Tests.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, fmt.Errorf("test run not found"))
		return
	}
	writeJSON(w, http.StatusOK, tr)
}

func (p *Platform) handleCancelTestRun(w http.ResponseWriter, r *http.Request) {
	if !p.Tests.Cancel(r.PathValue("id")) {
		writeErr(w, http.StatusNotFound, fmt.Errorf("test run not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
}
