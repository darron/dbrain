package audit

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/safehttp"
)

func TestWebhookConfigurationRejectsUnsafeDestinations(t *testing.T) {
	tests := []struct {
		name string
		cfg  WebhookConfig
	}{
		{"userinfo", WebhookConfig{URL: "https://user:secret@example.com/hook"}},
		{"query", WebhookConfig{URL: "https://example.com/hook?token=secret"}},
		{"fragment", WebhookConfig{URL: "https://example.com/hook#secret"}},
		{"opaque", WebhookConfig{URL: "https:opaque"}},
		{"non http", WebhookConfig{URL: "file:///tmp/hook"}},
		{"public http", WebhookConfig{URL: "http://example.com/hook", AllowPrivateOrigin: true}},
		{"private without opt in", WebhookConfig{URL: "http://127.0.0.1:8080/hook"}},
		{"admin path", WebhookConfig{URL: "https://example.com/hook", AdminOrigin: "https://admin.example.com/private"}},
		{"plain bearer", WebhookConfig{URL: "https://example.com/hook", BearerTokenRef: "plain-secret"}},
		{"empty typed bearer", WebhookConfig{URL: "https://example.com/hook", BearerTokenRef: "env: "}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewWebhook(test.cfg); err == nil {
				t.Fatal("expected configuration rejection")
			}
		})
	}
}

func TestWebhookAllowsExactHTTPSPrivateDNSOnlyWithExplicitOptIn(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	serverTransport := server.Client().Transport.(*http.Transport)
	tlsConfig := serverTransport.TLSClientConfig.Clone()
	tlsConfig.ServerName = "example.com"
	policy := safehttp.Policy{
		LookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
		TLSClientConfig: tlsConfig,
	}
	report := Report{Status: StatusFail, CompletedAt: time.Now().UTC()}
	decision := AlertDecision{Notify: true, Profile: ProfileFast, Changes: []AlertChange{{CheckID: CheckBoundaryConfig, Status: StatusFail}}}
	blocked, err := newWebhookWithPolicy(WebhookConfig{URL: "https://private.internal/hook"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := blocked.Deliver(context.Background(), report, decision); err == nil {
		t.Fatal("private DNS was allowed without exact-origin opt-in")
	}
	allowed, err := newWebhookWithPolicy(WebhookConfig{URL: "https://private.internal/hook", AllowPrivateOrigin: true}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := allowed.Deliver(context.Background(), report, decision); err != nil {
		t.Fatalf("exact HTTPS private origin rejected: %v", err)
	}
}

func TestWebhookUsesBoundedContentFreeBodySecretRefAndNoProxy(t *testing.T) {
	t.Setenv("AUDIT_WEBHOOK_TOKEN", "resolved-secret")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	var gotBody []byte
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	webhook, err := NewWebhook(WebhookConfig{URL: server.URL + "/audit", BearerTokenRef: "env:AUDIT_WEBHOOK_TOKEN", AllowPrivateOrigin: true, AdminOrigin: "https://dbrain.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	report := Report{Status: StatusFail, CompletedAt: now, Boundary: Boundary{Version: "v1.2.3", Commit: "abcdef0", Platform: "darwin/arm64", SecurityBaseline: "v0.6.0-security-pass", SecurityBaselineEpoch: 1}}
	decision := AlertDecision{Notify: true, Profile: ProfileFast, Changes: []AlertChange{{CheckID: CheckBoundaryConfig, Status: StatusFail, Summary: fixedSummary(CheckBoundaryConfig)}}}
	if err := webhook.Deliver(context.Background(), report, decision); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer resolved-secret" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	var body map[string]any
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"audit_id", "evidence", "path", "url", "credential", "provider", "transcript", "ocr", "resolved-secret"} {
		if strings.Contains(strings.ToLower(string(gotBody)), forbidden) {
			t.Fatalf("webhook contains %q: %s", forbidden, gotBody)
		}
	}
	if body["admin_origin"] != "https://dbrain.example.test:443" || int64(len(gotBody)) > MaxWebhookBytes {
		t.Fatalf("webhook body = %s", gotBody)
	}
}

func TestWebhookDoesNotFollowRedirectAndBoundsResponse(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/secret", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("secret response"))
	}))
	defer server.Close()
	webhook, err := NewWebhook(WebhookConfig{URL: server.URL + "/redirect", AllowPrivateOrigin: true})
	if err != nil {
		t.Fatal(err)
	}
	err = webhook.Deliver(context.Background(), Report{Status: StatusFail, CompletedAt: time.Now().UTC()}, AlertDecision{Notify: true, Profile: ProfileFast, Changes: []AlertChange{{CheckID: CheckBoundaryConfig, Status: StatusFail, Summary: fixedSummary(CheckBoundaryConfig)}}})
	if err == nil || requests != 1 || strings.Contains(err.Error(), "secret response") {
		t.Fatalf("redirect error=%v requests=%d", err, requests)
	}

	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.CopyN(w, strings.NewReader(strings.Repeat("x", int(MaxWebhookBytes+1))), MaxWebhookBytes+1)
	}))
	defer large.Close()
	webhook, err = NewWebhook(WebhookConfig{URL: large.URL, AllowPrivateOrigin: true})
	if err != nil {
		t.Fatal(err)
	}
	err = webhook.Deliver(context.Background(), Report{Status: StatusFail, CompletedAt: time.Now().UTC()}, AlertDecision{Notify: true, Profile: ProfileFast, Changes: []AlertChange{{CheckID: CheckBoundaryConfig, Status: StatusFail, Summary: fixedSummary(CheckBoundaryConfig)}}})
	if err == nil {
		t.Fatal("oversized response accepted")
	}
}

func TestWebhookRejectsOversizedRequestBeforeNetwork(t *testing.T) {
	webhook, err := NewWebhook(WebhookConfig{URL: "https://example.com/hook"})
	if err != nil {
		t.Fatal(err)
	}
	changes := make([]AlertChange, 0, 5000)
	for index := 0; index < 5000; index++ {
		changes = append(changes, AlertChange{CheckID: CheckBoundaryConfig, Status: StatusFail, Summary: strings.Repeat("x", 100)})
	}
	err = webhook.Deliver(context.Background(), Report{Status: StatusFail, CompletedAt: time.Now().UTC()}, AlertDecision{Notify: true, Profile: ProfileFast, Changes: changes})
	if err == nil || !strings.Contains(err.Error(), "request") {
		t.Fatalf("oversized request error = %v", err)
	}
	if strings.Contains(err.Error(), os.Getenv("HOME")) {
		t.Fatal("error leaked local path")
	}
}

func TestWebhookRejectsOversizedResolvedTokenAndTargetBeforeNetwork(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	report := Report{Status: StatusFail, CompletedAt: time.Now().UTC()}
	decision := AlertDecision{Notify: true, Changes: []AlertChange{{CheckID: CheckBoundaryConfig, Status: StatusFail}}}

	t.Setenv("AUDIT_OVERSIZED_TOKEN", strings.Repeat("sensitive-token", int(MaxWebhookBytes)))
	withToken, err := NewWebhook(WebhookConfig{URL: server.URL, AllowPrivateOrigin: true, BearerTokenRef: "env:AUDIT_OVERSIZED_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if err := withToken.Deliver(context.Background(), report, decision); err == nil || strings.Contains(err.Error(), "sensitive-token") {
		t.Fatalf("oversized token error = %v", err)
	}

	withTarget, err := NewWebhook(WebhookConfig{URL: server.URL + "/" + strings.Repeat("x", int(MaxWebhookBytes)), AllowPrivateOrigin: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := withTarget.Deliver(context.Background(), report, decision); err == nil {
		t.Fatal("oversized webhook target accepted")
	}
	if requests != 0 {
		t.Fatalf("oversized requests reached network: %d", requests)
	}
}
