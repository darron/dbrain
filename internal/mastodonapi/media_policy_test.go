package mastodonapi

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/safehttp"
)

func TestMediaHTTPPolicyReconstructsExactCanonicalOrigin(t *testing.T) {
	base := &safehttp.Policy{
		AllowPrivateNetwork:             true,
		AllowedOrigins:                  []string{"https://wrong.example:443"},
		RejectCredentialQueryOnRedirect: false,
		DisableCompression:              true,
	}

	policy, err := MediaHTTPPolicy("HTTPS://Media.Example.COM.:443/path/image.jpg?signature=ordinary", base)
	if err != nil {
		t.Fatalf("MediaHTTPPolicy: %v", err)
	}
	if len(policy.AllowedOrigins) != 1 || policy.AllowedOrigins[0] != "https://media.example.com:443" {
		t.Fatalf("allowed origins = %#v", policy.AllowedOrigins)
	}
	if !policy.RejectCredentialQueryOnRedirect || !policy.AllowPrivateNetwork || !policy.DisableCompression {
		t.Fatalf("base safety settings were not preserved: %+v", policy)
	}
	if base.AllowedOrigins[0] != "https://wrong.example:443" || base.RejectCredentialQueryOnRedirect {
		t.Fatalf("builder mutated base policy: %+v", base)
	}
}

func TestMediaHTTPPolicyFailsClosedWhenOriginCannotBeReconstructed(t *testing.T) {
	for _, rawURL := range []string{
		"",
		"://broken",
		"file:///tmp/image.jpg",
		"http://media.example/image.jpg",
		"https://user:secret@media.example/image.jpg",
	} {
		t.Run(rawURL, func(t *testing.T) {
			if _, err := MediaHTTPPolicy(rawURL, nil); err == nil {
				t.Fatalf("MediaHTTPPolicy(%q) succeeded", rawURL)
			}
		})
	}
}

func TestMediaHTTPPolicyRejectsCrossOriginAndCredentialQueryRedirects(t *testing.T) {
	hits := make(map[string]int)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.Host+r.URL.Path]++
		switch r.URL.Path {
		case "/cross-origin":
			http.Redirect(w, r, "https://other.example.com/final", http.StatusFound)
		case "/credential-query":
			http.Redirect(w, r, "https://media.example.com/final?access_token=secret", http.StatusFound)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	base := &safehttp.Policy{
		AllowPrivateNetwork: true,
		TLSClientConfig:     server.Client().Transport.(*http.Transport).TLSClientConfig,
		LookupNetIP: func(_ context.Context, _ string, _ string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, server.Listener.Addr().String())
		},
	}

	for _, path := range []string{"/cross-origin", "/credential-query"} {
		policy, err := MediaHTTPPolicy("https://media.example.com"+path, base)
		if err != nil {
			t.Fatalf("MediaHTTPPolicy(%s): %v", path, err)
		}
		response, err := safehttp.NewClient(policy).Get("https://media.example.com" + path)
		if response != nil {
			_ = response.Body.Close()
		}
		if err == nil || !safehttp.IsPolicyError(err) {
			t.Fatalf("redirect %s error = %v, want policy error", path, err)
		}
	}
	if hits["other.example.com/final"] != 0 || hits["media.example.com/final"] != 0 {
		t.Fatalf("blocked redirect reached final destination: %#v", hits)
	}
	if hits["media.example.com/cross-origin"] != 1 || hits["media.example.com/credential-query"] != 1 {
		t.Fatalf("initial requests missing or repeated: %#v", hits)
	}
	for target := range hits {
		if strings.Contains(target, "secret") {
			t.Fatalf("credential query reached server: %q", target)
		}
	}
}
