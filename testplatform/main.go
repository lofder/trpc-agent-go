// Command testplatform starts the trpc-agent-go agent test & observability
// platform: a web UI that monitors context assembly, traces model/tool call
// chains, supports human-in-the-loop context editing (breakpoints) and
// manages test cases (manual / imported / generated).
//
// Usage:
//
//	go run ./testplatform            # from the repository root
//	go run .                        # from the testplatform directory
//
// Without OPENAI_API_KEY it runs against a built-in deterministic mock model
// so every feature works offline. Set OPENAI_API_KEY / OPENAI_BASE_URL /
// MODEL_NAME (or use the settings page) to test a real model.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/testplatform/internal/platform"
	"trpc.group/trpc-go/trpc-agent-go/testplatform/web"
)

var (
	addr    = flag.String("addr", ":8080", "HTTP listen address")
	dataDir = flag.String("data", "testplatform-data", "directory for persisted test cases")
	maxRuns = flag.Int("max-runs", 300, "maximum run traces kept in memory")
)

func main() {
	flag.Parse()

	p, err := platform.New(*dataDir, *maxRuns)
	if err != nil {
		log.Fatalf("platform init failed: %v", err)
	}

	p.Logger.Infof(platform.CatServer, "", "==============================================")
	p.Logger.Infof(platform.CatServer, "", " trpc-agent-go Agent 测试监控平台")
	p.Logger.Infof(platform.CatServer, "", " Web UI:  http://localhost%s/", normalizeAddr(*addr))
	p.Logger.Infof(platform.CatServer, "", " API:     http://localhost%s/api/status", normalizeAddr(*addr))
	s := p.Settings()
	p.Logger.Infof(platform.CatServer, "", " 模型:    provider=%s model=%s (可在\"设置\"页切换)", s.Provider, s.Model)
	if s.Provider == "mock" {
		p.Logger.Infof(platform.CatServer, "", " 提示:    未检测到 OPENAI_API_KEY，使用内置 Mock 模型（离线可用，支持工具调用链路）")
	}
	p.Logger.Infof(platform.CatServer, "", "==============================================")

	server := &http.Server{
		Addr:              *addr,
		Handler:           p.NewHTTPHandler(web.FS()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	p.Logger.Infof(platform.CatServer, "", "HTTP 服务启动，监听 %s", *addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("http server: %v", err)
	}
}

func normalizeAddr(a string) string {
	if a == "" {
		return ":8080"
	}
	if a[0] == ':' {
		return a
	}
	for i := len(a) - 1; i >= 0; i-- {
		if a[i] == ':' {
			return a[i:]
		}
	}
	return fmt.Sprintf(":%s", a)
}
