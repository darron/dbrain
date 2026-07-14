package httpsecurity

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOriginGuardPolicy(t *testing.T) {
	t.Parallel()

	const extensionOrigin = "chrome-extension://abcdefghijklmnopabcdefghijklmnop"
	for _, tc := range []struct {
		name   string
		method string
		path   string
		origin string
		status int
	}{
		{name: "safe method ignores hostile origin", method: http.MethodGet, path: "/api/tag", origin: "https://evil.example", status: http.StatusNoContent},
		{name: "no Origin CLI write", method: http.MethodPost, path: "/api/tag", status: http.StatusNoContent},
		{name: "same-origin write", method: http.MethodPost, path: "/api/tag", origin: "https://dbrain.example.ts.net", status: http.StatusNoContent},
		{name: "hostile cross-origin write", method: http.MethodPost, path: "/api/tag", origin: "https://evil.example", status: http.StatusForbidden},
		{name: "extension link add", method: http.MethodPost, path: "/api/links", origin: extensionOrigin, status: http.StatusNoContent},
		{name: "extension other write", method: http.MethodPost, path: "/api/tag", origin: extensionOrigin, status: http.StatusForbidden},
		{name: "malformed origin", method: http.MethodPost, path: "/api/tag", origin: "://bad", status: http.StatusForbidden},
		{name: "origin with path", method: http.MethodPost, path: "/api/tag", origin: "https://dbrain.example.ts.net/not-an-origin", status: http.StatusForbidden},
		{name: "origin with query", method: http.MethodPost, path: "/api/tag", origin: "https://dbrain.example.ts.net?not=an-origin", status: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			nextCalled := false
			handler := OriginGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(tc.method, "https://dbrain.example.ts.net"+tc.path, nil)
			req.TLS = &tls.ConnectionState{}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d body=%q", rec.Code, tc.status, rec.Body.String())
			}
			if got := nextCalled; got != (tc.status == http.StatusNoContent) {
				t.Fatalf("next called = %t for status %d", got, tc.status)
			}
		})
	}
}

func TestOriginGuardDoesNotTrustForwardedOriginHeaders(t *testing.T) {
	t.Parallel()

	nextCalled := false
	handler := OriginGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8742/api/tag", nil)
	req.Header.Set("Origin", "https://public.example")
	req.Header.Set("X-Forwarded-Host", "public.example")
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 body=%q", rec.Code, rec.Body.String())
	}
	if nextCalled {
		t.Fatal("guard trusted forwarded origin headers")
	}
}
