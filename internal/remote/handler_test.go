package remote

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemoteHandlerDispatchesMCPBeforeWebSPA(t *testing.T) {
	t.Parallel()

	webHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>spa</html>"))
	})
	mcpHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mcp":true}`))
	})
	handler, err := NewHandler(HandlerOptions{
		Web:        true,
		MCP:        true,
		MCPPath:    "/mcp",
		WebHandler: webHandler,
		MCPHandler: mcpHandler,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://dbrain.test/mcp", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Body.String() != `{"mcp":true}` {
		t.Fatalf("/mcp body = %q", rec.Body.String())
	}

	for _, path := range []string{"/mcp/", "/mcp/foo"} {
		req := httptest.NewRequest(http.MethodPost, "http://dbrain.test"+path, strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<html>") {
			t.Fatalf("%s fell through to web SPA: %q", path, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
			t.Fatalf("%s Content-Type = %q, want JSON", path, got)
		}
	}
}

func TestRemoteHandlerOriginGuard(t *testing.T) {
	t.Parallel()

	handler, err := NewHandler(HandlerOptions{
		Web:     true,
		MCP:     false,
		MCPPath: "/mcp",
		TLS:     true,
		WebHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}),
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	for _, tc := range []struct {
		name   string
		method string
		origin string
		status int
	}{
		{name: "get ignores origin", method: http.MethodGet, origin: "https://evil.test", status: http.StatusOK},
		{name: "post no origin", method: http.MethodPost, origin: "", status: http.StatusOK},
		{name: "post same origin", method: http.MethodPost, origin: "https://dbrain.example.ts.net", status: http.StatusOK},
		{name: "post mismatch", method: http.MethodPost, origin: "https://evil.test", status: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tc.method, "https://dbrain.example.ts.net/api/tag", strings.NewReader(`{}`))
			req.TLS = &tls.ConnectionState{}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d body=%q", rec.Code, tc.status, rec.Body.String())
			}
		})
	}
}

func TestRemoteHandlerSecurityHeaders(t *testing.T) {
	t.Parallel()

	handler, err := NewHandler(HandlerOptions{
		Web:     true,
		MCPPath: "/mcp",
		TLS:     true,
		WebHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}),
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "https://dbrain.example.ts.net/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	for _, header := range []string{"Strict-Transport-Security", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if rec.Header().Get(header) == "" {
			t.Fatalf("missing %s header", header)
		}
	}
}

func TestRemoteHandlerRequiresEnabledHandlers(t *testing.T) {
	t.Parallel()

	if _, err := NewHandler(HandlerOptions{Web: true, MCPPath: "/mcp"}); err == nil {
		t.Fatalf("expected missing web handler error")
	}
	if _, err := NewHandler(HandlerOptions{MCP: true, MCPPath: "/mcp"}); err == nil {
		t.Fatalf("expected missing MCP handler error")
	}
}
