package githubimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/audit"
)

func newGitHubAuditTestServer(t *testing.T, handler http.Handler) (*httptest.Server, githubAuditHTTPInjections) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	tlsConfig := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	tlsConfig.InsecureSkipVerify = true //nolint:gosec // test-only TLS server beneath the fixed safehttp origin policy
	return server, githubAuditHTTPInjections{
		LookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
		TLSClientConfig: tlsConfig,
	}
}

func TestGitHubStarSourceKeyIsSharedWithNormalImporter(t *testing.T) {
	t.Parallel()

	const want = "gh-star:Viewer:owner/repo"
	got, err := githubStarSourceKey(" Viewer ", " Owner/Repo ")
	if err != nil || got != want {
		t.Fatalf("githubStarSourceKey() = %q, %v; want %q", got, err, want)
	}
	item, err := toItem(" Viewer ", starRecord{Repo: repository{FullName: " Owner/Repo "}}, time.Unix(0, 0).UTC())
	if err != nil || item.SourceKey != want {
		t.Fatalf("toItem source key = %q, %v; want %q", item.SourceKey, err, want)
	}
	for _, input := range [][2]string{{"", "owner/repo"}, {"viewer", ""}, {"viewer", "owner"}, {"viewer", "owner/repo/extra"}, {"viewer", "/repo"}, {"viewer", "owner/"}} {
		if _, err := githubStarSourceKey(input[0], input[1]); err == nil {
			t.Fatalf("githubStarSourceKey(%q, %q) succeeded", input[0], input[1])
		}
	}
}

func TestGitHubAuditInventoryUsesExactAPIAndObservesEndAfterFullPage(t *testing.T) {
	t.Parallel()

	stars := make([]starRecord, 100)
	for i := range stars {
		stars[i].Repo.FullName = "owner/repo-" + strings.Repeat("x", i%3) + time.Unix(int64(i), 0).UTC().Format("150405")
	}
	var requests []*http.Request
	_, injected := newGitHubAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, req.Clone(req.Context()))
		switch {
		case req.URL.Path == "/user":
			_ = json.NewEncoder(w).Encode(viewer{Login: "Viewer"})
		case req.URL.Path == "/user/starred" && req.URL.Query().Get("page") == "1":
			_ = json.NewEncoder(w).Encode(stars)
		case req.URL.Path == "/user/starred" && req.URL.Query().Get("page") == "2":
			_ = json.NewEncoder(w).Encode([]starRecord{})
		default:
			http.Error(w, "must-not-leak", http.StatusNotFound)
		}
	}))
	resolveCalls := 0
	inventory := newGitHubAuditInventory("/unused", "test-agent", func(context.Context, string) (string, error) {
		resolveCalls++
		return "secret-token", nil
	}, injected)
	if resolveCalls != 0 {
		t.Fatal("token resolved before Inventory")
	}
	got, err := inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if resolveCalls != 1 || !got.Complete || got.PageCount != 2 || len(got.IdentityHashes) != 100 {
		t.Fatalf("result = %#v, resolver calls = %d", got, resolveCalls)
	}
	if len(requests) != 3 {
		t.Fatalf("requests = %d, want viewer plus full and terminal-empty star pages", len(requests))
	}
	for _, req := range requests {
		if req.TLS == nil || req.Host != "api.github.com" {
			t.Fatalf("request escaped exact HTTPS GitHub origin: host=%q tls=%t path=%s", req.Host, req.TLS != nil, req.URL)
		}
		if req.Header.Get("Authorization") != "Bearer secret-token" || req.Header.Get("X-GitHub-Api-Version") != apiVersion || req.Header.Get("User-Agent") != "test-agent" {
			t.Fatalf("fixed request headers missing: %#v", req.Header)
		}
	}
	if requests[0].Header.Get("Accept") != "application/vnd.github+json" || requests[1].Header.Get("Accept") != "application/vnd.github.star+json" {
		t.Fatalf("unexpected Accept headers: viewer=%q stars=%q", requests[0].Header.Get("Accept"), requests[1].Header.Get("Accept"))
	}
	starred := requests[1]
	if starred.URL.Query().Get("sort") != "created" || starred.URL.Query().Get("direction") != "desc" || starred.URL.Query().Get("per_page") != "100" {
		t.Fatalf("unexpected starred query: %s", starred.URL.RawQuery)
	}
}

func TestGitHubAuditInventoryTreatsShortPageAsNaturalCompletion(t *testing.T) {
	t.Parallel()

	starPages := 0
	_, injected := newGitHubAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/user":
			_ = json.NewEncoder(w).Encode(viewer{Login: "viewer"})
		case "/user/starred":
			starPages++
			_ = json.NewEncoder(w).Encode([]starRecord{{Repo: repository{FullName: "owner/repo"}}})
		default:
			http.NotFound(w, req)
		}
	}))
	inventory := newGitHubAuditInventory("/unused", "ua", func(context.Context, string) (string, error) { return "token", nil }, injected)
	got, err := inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
	if err != nil || !got.Complete || got.PageCount != 1 || len(got.IdentityHashes) != 1 {
		t.Fatalf("short-page result = %#v, err=%v", got, err)
	}
	if starPages != 1 {
		t.Fatalf("star page requests = %d, want 1", starPages)
	}
}

func TestGitHubAuditInventoryDeduplicatesAndEnforcesBudgets(t *testing.T) {
	t.Parallel()

	t.Run("deduplicates before identity cap", func(t *testing.T) {
		_, injected := newGitHubAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/user" {
				_ = json.NewEncoder(w).Encode(viewer{Login: "viewer"})
				return
			}
			_ = json.NewEncoder(w).Encode([]starRecord{{Repo: repository{FullName: "owner/repo"}}, {Repo: repository{FullName: "OWNER/REPO"}}})
		}))
		inventory := newGitHubAuditInventory("/unused", "ua", func(context.Context, string) (string, error) { return "token", nil }, injected)
		got, err := inventory.Inventory(t.Context(), audit.InventoryBudget{MaxIdentities: 1, MaxPages: 1})
		if err != nil || !got.Complete || len(got.IdentityHashes) != 1 {
			t.Fatalf("deduplicated result = %#v, err=%v", got, err)
		}
	})

	t.Run("page cap requires observed end", func(t *testing.T) {
		stars := make([]starRecord, githubAuditPageSize)
		for index := range stars {
			stars[index].Repo.FullName = fmt.Sprintf("owner/repo-%d", index)
		}
		_, injected := newGitHubAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/user" {
				_ = json.NewEncoder(w).Encode(viewer{Login: "viewer"})
				return
			}
			_ = json.NewEncoder(w).Encode(stars)
		}))
		inventory := newGitHubAuditInventory("/unused", "ua", func(context.Context, string) (string, error) { return "token", nil }, injected)
		got, err := inventory.Inventory(t.Context(), audit.InventoryBudget{MaxIdentities: 100, MaxPages: 1})
		if !errors.Is(err, audit.ErrInventoryBudget) || got.Complete || got.PageCount != 1 {
			t.Fatalf("page-cap result = %#v, err=%v", got, err)
		}
	})

	t.Run("unique identity cap", func(t *testing.T) {
		_, injected := newGitHubAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/user" {
				_ = json.NewEncoder(w).Encode(viewer{Login: "viewer"})
				return
			}
			_ = json.NewEncoder(w).Encode([]starRecord{{Repo: repository{FullName: "owner/one"}}, {Repo: repository{FullName: "owner/two"}}})
		}))
		inventory := newGitHubAuditInventory("/unused", "ua", func(context.Context, string) (string, error) { return "token", nil }, injected)
		got, err := inventory.Inventory(t.Context(), audit.InventoryBudget{MaxIdentities: 1, MaxPages: 1})
		if !errors.Is(err, audit.ErrInventoryBudget) || got.Complete || len(got.IdentityHashes) != 1 {
			t.Fatalf("identity-cap result = %#v, err=%v", got, err)
		}
	})

	t.Run("invalid caller budget fails before secrets or network", func(t *testing.T) {
		resolveCalls := 0
		inventory := newGitHubAuditInventory("/unused", "ua", func(context.Context, string) (string, error) {
			resolveCalls++
			return "token", nil
		}, githubAuditHTTPInjections{})
		for _, budget := range []audit.InventoryBudget{{}, {MaxIdentities: audit.InventoryMaxIdentities + 1, MaxPages: 1}, {MaxIdentities: 1, MaxPages: audit.InventoryMaxPages + 1}} {
			if _, err := inventory.Inventory(t.Context(), budget); !errors.Is(err, audit.ErrInventoryInvalid) {
				t.Fatalf("budget %#v error = %v, want invalid", budget, err)
			}
		}
		if resolveCalls != 0 {
			t.Fatalf("resolver called %d times for invalid budgets", resolveCalls)
		}
	})
}

func TestGitHubAuditInventoryFailsClosedWithoutLeakingSourceData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{name: "empty viewer", handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"login":""}`) }},
		{name: "trailing JSON", handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"login":"viewer"} body-secret`)
		}},
		{name: "oversized body", handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat("x", githubAuditResponseMaxBytes+1))
		}},
		{name: "non success", handler: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "body-secret", http.StatusForbidden) }},
		{name: "redirect", handler: func(w http.ResponseWriter, req *http.Request) {
			http.Redirect(w, req, "https://evil.example/private-owner", http.StatusFound)
		}},
		{name: "null starred list", handler: func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/user" {
				_ = json.NewEncoder(w).Encode(viewer{Login: "viewer"})
				return
			}
			_, _ = io.WriteString(w, "null")
		}},
		{name: "malformed repository", handler: func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/user" {
				_ = json.NewEncoder(w).Encode(viewer{Login: "viewer"})
				return
			}
			_ = json.NewEncoder(w).Encode([]starRecord{{Repo: repository{FullName: "private-owner/"}}})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, injected := newGitHubAuditTestServer(t, test.handler)
			inventory := newGitHubAuditInventory("/unused", "ua", func(context.Context, string) (string, error) { return "secret-token", nil }, injected)
			got, err := inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
			if err == nil || got.Complete {
				t.Fatalf("result = %#v, err=%v; want incomplete error", got, err)
			}
			message := err.Error()
			for _, secret := range []string{"secret-token", "body-secret", "private-owner", "evil.example"} {
				if strings.Contains(message, secret) {
					t.Fatalf("error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func TestGitHubAuditInventoryScrubsResolverErrorsBeforeNetwork(t *testing.T) {
	t.Parallel()

	networkCalls := 0
	injected := githubAuditHTTPInjections{
		LookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) {
			networkCalls++
			return nil, errors.New("unexpected network")
		},
	}
	inventory := newGitHubAuditInventory("/unused", "ua", func(context.Context, string) (string, error) {
		return "", errors.New("resolver leaked secret-token")
	}, injected)
	got, err := inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
	if err == nil || got.Complete || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("resolver failure result = %#v, err=%v", got, err)
	}
	if networkCalls != 0 {
		t.Fatalf("network calls = %d, want zero", networkCalls)
	}
}

func TestGitHubAuditInventoryCancellationIsIncomplete(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	_, injected := newGitHubAuditTestServer(t, http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		close(started)
		<-req.Context().Done()
	}))
	inventory := newGitHubAuditInventory("/unused", "ua", func(context.Context, string) (string, error) { return "token", nil }, injected)
	ctx, cancel := context.WithCancel(t.Context())
	type outcome struct {
		result audit.InventoryResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := inventory.Inventory(ctx, audit.DefaultInventoryBudget())
		done <- outcome{result: result, err: err}
	}()
	<-started
	cancel()
	got := <-done
	if !errors.Is(got.err, context.Canceled) || got.result.Complete {
		t.Fatalf("cancel result = %#v, err=%v", got.result, got.err)
	}
}

func TestGitHubAuditInventoryIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("https_proxy", "http://127.0.0.1:1")

	_, injected := newGitHubAuditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/user" {
			_ = json.NewEncoder(w).Encode(viewer{Login: "viewer"})
			return
		}
		_ = json.NewEncoder(w).Encode([]starRecord{})
	}))
	inventory := newGitHubAuditInventory("/unused", "ua", func(context.Context, string) (string, error) { return "token", nil }, injected)
	got, err := inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
	if err != nil || !got.Complete {
		t.Fatalf("proxy-free inventory = %#v, err=%v", got, err)
	}
}
