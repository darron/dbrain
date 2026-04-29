package remote

import (
	"bytes"
	"strings"
	"testing"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

func TestRemoteUserLoggerTeesAndDedupesAuthURLs(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	logf := newUserLogger(&out)

	logf("open https://login.tailscale.com/a")
	logf("open https://login.tailscale.com/a")
	logf("again https://login.tailscale.com/a.")

	output := out.String()
	if strings.Count(output, "Visit this URL to authenticate dbrain:") != 1 {
		t.Fatalf("expected one deduped auth hint, got %q", output)
	}
	if strings.Count(output, "open https://login.tailscale.com/a") != 1 {
		t.Fatalf("expected duplicate raw tsnet log lines to be suppressed, got %q", output)
	}
	if strings.Count(output, "again https://login.tailscale.com/a.") != 1 {
		t.Fatalf("expected distinct raw tsnet log lines to still be teed, got %q", output)
	}
}

func TestRemoteURLs(t *testing.T) {
	t.Parallel()

	status := &ipnstate.Status{CertDomains: []string{"dbrain.example.ts.net."}}
	result := URLs(status, Options{
		Web:     true,
		MCP:     true,
		MCPPath: "/mcp",
		TLS:     true,
	})
	if result.WebURL != "https://dbrain.example.ts.net/" {
		t.Fatalf("WebURL = %q", result.WebURL)
	}
	if result.MCPURL != "https://dbrain.example.ts.net/mcp" {
		t.Fatalf("MCPURL = %q", result.MCPURL)
	}

	status = &ipnstate.Status{Self: &ipnstate.PeerStatus{DNSName: "dbrain.tailnet.ts.net."}}
	result = URLs(status, Options{MCP: true, MCPPath: "/brain", TLS: false})
	if result.MCPURL != "http://dbrain.tailnet.ts.net/brain" {
		t.Fatalf("MCPURL = %q", result.MCPURL)
	}
}

func TestWhoIsLabelPrefersUserThenNodeThenFallback(t *testing.T) {
	t.Parallel()

	if got := whoIsLabel(nil, "100.64.0.1:1234"); got != "100.64.0.1:1234" {
		t.Fatalf("nil whois label = %q", got)
	}
	if got := whoIsLabel(&apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"},
		Node:        &tailcfg.Node{Name: "node.example.ts.net."},
	}, "fallback"); got != "alice@example.com" {
		t.Fatalf("user whois label = %q", got)
	}
	if got := whoIsLabel(&apitype.WhoIsResponse{
		Node: &tailcfg.Node{Name: "node.example.ts.net."},
	}, "fallback"); got != "node.example.ts.net" {
		t.Fatalf("node whois label = %q", got)
	}
}
