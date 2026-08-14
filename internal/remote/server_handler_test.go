package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mcpserver"
	"github.com/darron/dbrain/internal/store"
)

func TestRemoteMCPAuditCapabilityFollowsFinalizedBearerAuth(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	report := remoteAuditTestReport(t, now)
	dependencies := mcpserver.ServerDependencies{Audit: mcpserver.AuditDependencies{
		RunFast: func(context.Context) (audit.Report, error) { return report, nil },
		Now:     func() time.Time { return now },
	}}

	t.Run("auth disabled omits and rejects despite injected dependencies", func(t *testing.T) {
		t.Setenv("DBRAIN_MCP_AUTH_ENABLED", "false")
		cfg := remoteMCPTestConfig(t)
		opts := Options{MCP: true, MCPPath: "/mcp"}
		SetMCPDependencies(&opts, dependencies)
		handler, cleanup, err := buildHandler(t.Context(), cfg, opts, nil, io.Discard)
		if err != nil {
			t.Fatalf("buildHandler: %v", err)
		}
		defer func() { _ = cleanup() }()
		listed := remoteMCPPost(t, handler, "", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
		if remoteMCPHasAuditTool(t, listed) {
			t.Fatalf("auth-disabled remote advertised dbrain_audit: %#v", listed)
		}
		called := remoteMCPPost(t, handler, "", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"dbrain_audit","arguments":{}}}`)
		if !strings.Contains(fmt.Sprint(called), "unknown tool") {
			t.Fatalf("auth-disabled remote dispatched dbrain_audit: %#v", called)
		}
	})

	t.Run("bearer auth advertises and calls after token acceptance", func(t *testing.T) {
		t.Setenv("DBRAIN_MCP_AUTH_ENABLED", "true")
		cfg := remoteMCPTestConfig(t)
		st, err := store.Open(cfg.DBPath)
		if err != nil {
			t.Fatalf("open token store: %v", err)
		}
		created, err := st.CreateMCPBearerToken(t.Context(), "remote-audit-test")
		if err != nil {
			_ = st.Close()
			t.Fatalf("CreateMCPBearerToken: %v", err)
		}
		if err := st.Close(); err != nil {
			t.Fatalf("close token store: %v", err)
		}
		opts := Options{MCP: true, MCPPath: "/mcp"}
		SetMCPDependencies(&opts, dependencies)
		handler, cleanup, err := buildHandler(t.Context(), cfg, opts, nil, io.Discard)
		if err != nil {
			t.Fatalf("buildHandler: %v", err)
		}
		defer func() { _ = cleanup() }()
		listed := remoteMCPPost(t, handler, created.Token, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
		if !remoteMCPHasAuditTool(t, listed) {
			t.Fatalf("authenticated remote omitted dbrain_audit: %#v", listed)
		}
		called := remoteMCPPost(t, handler, created.Token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"dbrain_audit","arguments":{}}}`)
		result := called["result"].(map[string]interface{})
		if result["isError"] != false {
			t.Fatalf("authenticated remote audit failed: %#v", called)
		}
	})
}

func remoteMCPTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("initialize store: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close initialized store: %v", err)
	}
	return cfg
}

func remoteAuditTestReport(t *testing.T, now time.Time) audit.Report {
	t.Helper()
	report, err := audit.Run(t.Context(), audit.Request{Profile: audit.ProfileFast}, audit.Dependencies{
		Features: audit.Features{
			Layout: "xdg", ConfigSource: "default", ConfigVerified: true, DatabaseOpenedQueryOnly: true,
			Sources: map[audit.Source]bool{}, Stages: map[audit.PipelineStage]bool{},
		},
		Runtime: audit.RuntimeVersion{
			ReleaseVersion: "v0.6.0", Commit: "abcdef1", GitStatus: "clean", Platform: "linux/amd64",
			SecurityBaselineID: "v0.6.0-security-pass", SecurityBaselineEpoch: 1,
		},
		Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("audit.Run: %v", err)
	}
	return report
}

func remoteMCPPost(t *testing.T, handler http.Handler, token, body string) map[string]interface{} {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://dbrain.test/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("MCP status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode MCP response: %v", err)
	}
	return response
}

func remoteMCPHasAuditTool(t *testing.T, response map[string]interface{}) bool {
	t.Helper()
	tools := response["result"].(map[string]interface{})["tools"].([]interface{})
	for _, raw := range tools {
		if raw.(map[string]interface{})["name"] == "dbrain_audit" {
			return true
		}
	}
	return false
}
