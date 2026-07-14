package feedimport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/safehttp"
	"github.com/darron/dbrain/internal/store"
)

var sanitizedFeedTextSink string

func TestHTTPFetcherBlocksLocalhostByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel><title>Local</title></channel></rss>`))
	}))
	defer server.Close()

	_, err := NewHTTPFetcher(nil).Fetch(context.Background(), store.Feed{
		URL:           server.URL + "/feed.xml",
		NormalizedURL: server.URL + "/feed.xml",
		ResolvedURL:   server.URL + "/feed.xml",
	}, Options{MaxBodyBytes: DefaultMaxBodyBytes})
	if err == nil {
		t.Fatal("expected localhost fetch to be blocked by default")
	}
	if !safehttp.IsPolicyError(err) {
		t.Fatalf("expected shared HTTP policy error, got %v", err)
	}
}

func TestHTTPFetcherCanAllowLocalhostForTesting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/rss+xml")
		_, _ = w.Write([]byte(`<rss><channel><title>Local</title></channel></rss>`))
	}))
	defer server.Close()

	fetch, err := NewHTTPFetcherWithOptions(nil, HTTPFetcherOptions{AllowPrivateNetwork: true}).Fetch(context.Background(), store.Feed{
		URL:           server.URL + "/feed.xml",
		NormalizedURL: server.URL + "/feed.xml",
		ResolvedURL:   server.URL + "/feed.xml",
	}, Options{MaxBodyBytes: DefaultMaxBodyBytes})
	if err != nil {
		t.Fatalf("Fetch with private-network opt-in: %v", err)
	}
	if fetch.HTTPStatus != http.StatusOK {
		t.Fatalf("HTTPStatus = %d, want 200", fetch.HTTPStatus)
	}
	if !strings.Contains(string(fetch.DecodedBody), "Local") {
		t.Fatalf("expected decoded body, got %q", string(fetch.DecodedBody))
	}
}

func TestHTTPFetcherMovesBasicAuthOutOfRequestURL(t *testing.T) {
	var username, password string
	var authenticated bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, authenticated = r.BasicAuth()
		w.Header().Set("content-type", "application/atom+xml")
		_, _ = w.Write([]byte(`<feed><title>Private</title></feed>`))
	}))
	defer server.Close()

	target := strings.Replace(server.URL, "://", "://feed:secret@", 1) + "/feed.atom"
	result, err := NewHTTPFetcherWithOptions(nil, HTTPFetcherOptions{AllowPrivateNetwork: true}).Fetch(
		context.Background(),
		store.Feed{URL: target, NormalizedURL: target, ResolvedURL: target},
		Options{MaxBodyBytes: DefaultMaxBodyBytes},
	)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !authenticated || username != "feed" || password != "secret" {
		t.Fatalf("BasicAuth = (%q, %q, %t)", username, password, authenticated)
	}
	if strings.Contains(result.RequestURL, "@") || strings.Contains(result.FinalURL, "@") ||
		strings.Contains(result.RequestURL, "secret") || strings.Contains(result.FinalURL, "secret") {
		t.Fatalf("credential-bearing result URLs: request=%q final=%q", result.RequestURL, result.FinalURL)
	}
}

func TestHTTPFetcherSanitizesMalformedCredentialURLInError(t *testing.T) {
	target := "https://credential-user:credential-password@example.com:bad/private.atom"
	_, err := NewHTTPFetcher(nil).Fetch(
		context.Background(),
		store.Feed{URL: target, NormalizedURL: target, ResolvedURL: target},
		Options{MaxBodyBytes: DefaultMaxBodyBytes},
	)
	if err == nil {
		t.Fatal("Fetch error = nil, want malformed URL error")
	}
	for _, credential := range []string{"credential-user", "credential-password"} {
		if strings.Contains(err.Error(), credential) {
			t.Fatalf("Fetch error contains %q: %v", credential, err)
		}
	}
}

func TestHTTPFetcherSanitizesMalformedInitialNetworkPathURLInError(t *testing.T) {
	target := "//credential-user:credential-password@example.com:bad/feed.atom"
	_, err := NewHTTPFetcher(nil).Fetch(
		context.Background(),
		store.Feed{URL: target, NormalizedURL: target, ResolvedURL: target},
		Options{MaxBodyBytes: DefaultMaxBodyBytes},
	)
	if err == nil {
		t.Fatal("Fetch error = nil, want malformed URL error")
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		t.Fatalf("Fetch error = %T %v, want *url.Error in chain", err, err)
	}
	for _, credential := range []string{"credential-user", "credential-password"} {
		if strings.Contains(err.Error(), credential) || strings.Contains(urlErr.URL, credential) {
			t.Fatalf("Fetch error contains %q: err=%v url=%q", credential, err, urlErr.URL)
		}
	}
}

func TestHTTPFetcherRetainsBasicAuthForSameOriginRedirect(t *testing.T) {
	var finalAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/feed.atom", http.StatusFound)
			return
		}
		finalAuthorization = r.Header.Get("Authorization")
		w.Header().Set("content-type", "application/atom+xml")
		_, _ = w.Write([]byte(`<feed><title>Private</title></feed>`))
	}))
	defer server.Close()

	target := strings.Replace(server.URL, "://", "://feed:secret@", 1) + "/start"
	_, err := NewHTTPFetcherWithOptions(nil, HTTPFetcherOptions{AllowPrivateNetwork: true}).Fetch(
		context.Background(),
		store.Feed{URL: target, NormalizedURL: target, ResolvedURL: target},
		Options{MaxBodyBytes: DefaultMaxBodyBytes},
	)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if finalAuthorization == "" {
		t.Fatal("same-origin redirect lost Authorization header")
	}
}

func TestHTTPFetcherPreservesDefaultRedirectLimitForAuthenticatedInjectedClient(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits > 12 {
			http.Error(w, "redirect safety cutoff", http.StatusLoopDetected)
			return
		}
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer server.Close()

	target := strings.Replace(server.URL, "://", "://feed:secret@", 1) + "/loop"
	_, err := NewHTTPFetcher(&http.Client{}).Fetch(
		context.Background(),
		store.Feed{URL: target, NormalizedURL: target, ResolvedURL: target},
		Options{MaxBodyBytes: DefaultMaxBodyBytes},
	)
	if err == nil || !strings.Contains(err.Error(), "stopped after 10 redirects") {
		t.Fatalf("Fetch error = %v, want stopped after 10 redirects", err)
	}
	if hits != 10 {
		t.Fatalf("redirect hits = %d, want 10", hits)
	}
}

func TestHTTPFetcherStripsBasicAuthFromCrossOriginRedirect(t *testing.T) {
	var destinationAuthorization string
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationAuthorization = r.Header.Get("Authorization")
		w.Header().Set("content-type", "application/atom+xml")
		_, _ = w.Write([]byte(`<feed><title>Redirected</title></feed>`))
	}))
	defer destination.Close()

	var originAuthorization string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originAuthorization = r.Header.Get("Authorization")
		http.Redirect(w, r, destination.URL+"/feed.atom", http.StatusFound)
	}))
	defer origin.Close()

	target := strings.Replace(origin.URL, "://", "://feed:secret@", 1) + "/start"
	_, err := NewHTTPFetcherWithOptions(nil, HTTPFetcherOptions{AllowPrivateNetwork: true}).Fetch(
		context.Background(),
		store.Feed{URL: target, NormalizedURL: target, ResolvedURL: target},
		Options{MaxBodyBytes: DefaultMaxBodyBytes},
	)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if originAuthorization == "" {
		t.Fatal("origin did not receive Authorization header")
	}
	if destinationAuthorization != "" {
		t.Fatalf("cross-origin Authorization = %q, want empty", destinationAuthorization)
	}
}

func TestHTTPFetcherStillRejectsUserInfoIntroducedByRedirect(t *testing.T) {
	destinationHits := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationHits++
		_, _ = w.Write([]byte(`<feed/>`))
	}))
	defer destination.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		location := strings.Replace(destination.URL, "://", "://redirect-user:redirect-password@", 1) + "/feed.atom"
		http.Redirect(w, r, location, http.StatusFound)
	}))
	defer origin.Close()

	target := strings.Replace(origin.URL, "://", "://origin-user:origin-password@", 1) + "/start"
	_, err := NewHTTPFetcherWithOptions(nil, HTTPFetcherOptions{AllowPrivateNetwork: true}).Fetch(
		context.Background(),
		store.Feed{URL: target, NormalizedURL: target, ResolvedURL: target},
		Options{MaxBodyBytes: DefaultMaxBodyBytes},
	)
	if err == nil || !safehttp.IsPolicyError(err) {
		t.Fatalf("Fetch error = %v, want safe HTTP policy error", err)
	}
	for _, credential := range []string{"origin-user", "origin-password", "redirect-user", "redirect-password"} {
		if strings.Contains(err.Error(), credential) {
			t.Fatalf("Fetch error contains %q: %v", credential, err)
		}
	}
	if destinationHits != 0 {
		t.Fatalf("credential-bearing redirect reached destination %d times", destinationHits)
	}
}

func TestHTTPFetcherSanitizesMalformedRedirectURLInError(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("location", "http://reflected-user:reflected-password@example.com:bad/feed.atom")
		w.WriteHeader(http.StatusFound)
	}))
	defer origin.Close()

	target := strings.Replace(origin.URL, "://", "://reflected-user:reflected-password@", 1) + "/start"
	_, err := NewHTTPFetcherWithOptions(nil, HTTPFetcherOptions{AllowPrivateNetwork: true}).Fetch(
		context.Background(),
		store.Feed{URL: target, NormalizedURL: target, ResolvedURL: target},
		Options{MaxBodyBytes: DefaultMaxBodyBytes},
	)
	if err == nil {
		t.Fatal("Fetch error = nil, want malformed redirect URL error")
	}
	if !strings.Contains(err.Error(), "invalid port") {
		t.Fatalf("Fetch error = %v, want invalid port diagnostic", err)
	}
	for _, credential := range []string{"reflected-user", "reflected-password"} {
		if strings.Contains(err.Error(), credential) {
			t.Fatalf("Fetch error contains %q: %v", credential, err)
		}
	}
}

func TestHTTPFetcherSanitizesMalformedNetworkPathRedirectInError(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("location", "//reflected-user:reflected-password@example.com:bad/feed.atom")
		w.WriteHeader(http.StatusFound)
	}))
	defer origin.Close()

	target := strings.Replace(origin.URL, "://", "://reflected-user:reflected-password@", 1) + "/start"
	_, err := NewHTTPFetcherWithOptions(nil, HTTPFetcherOptions{AllowPrivateNetwork: true}).Fetch(
		context.Background(),
		store.Feed{URL: target, NormalizedURL: target, ResolvedURL: target},
		Options{MaxBodyBytes: DefaultMaxBodyBytes},
	)
	if err == nil {
		t.Fatal("Fetch error = nil, want malformed redirect URL error")
	}
	if !strings.Contains(err.Error(), "invalid port") {
		t.Fatalf("Fetch error = %v, want invalid port diagnostic", err)
	}
	for _, credential := range []string{"reflected-user", "reflected-password"} {
		if strings.Contains(err.Error(), credential) {
			t.Fatalf("Fetch error contains %q: %v", credential, err)
		}
	}
}

func TestHTTPFetcherSanitizesFinalURLWithInjectedClient(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			location := strings.Replace(server.URL, "://", "://redirect-user:redirect-password@", 1) + "/feed.atom"
			http.Redirect(w, r, location, http.StatusFound)
			return
		}
		w.Header().Set("content-type", "application/atom+xml")
		_, _ = w.Write([]byte(`<feed><title>Injected</title></feed>`))
	}))
	defer server.Close()

	target := strings.Replace(server.URL, "://", "://origin-user:origin-password@", 1) + "/start"
	result, err := NewHTTPFetcher(&http.Client{}).Fetch(
		context.Background(),
		store.Feed{URL: target, NormalizedURL: target, ResolvedURL: target},
		Options{MaxBodyBytes: DefaultMaxBodyBytes},
	)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	for _, credential := range []string{"origin-user", "origin-password", "redirect-user", "redirect-password"} {
		if strings.Contains(result.RequestURL, credential) || strings.Contains(result.FinalURL, credential) {
			t.Fatalf("result URL contains %q: request=%q final=%q", credential, result.RequestURL, result.FinalURL)
		}
	}
	if strings.Contains(result.RequestURL, "@") || strings.Contains(result.FinalURL, "@") {
		t.Fatalf("credential-bearing result URLs: request=%q final=%q", result.RequestURL, result.FinalURL)
	}
}

func TestHTTPFetcherDoesNotRecoverStoredBasicAuthAcrossOrigins(t *testing.T) {
	var destinationAuthorization string
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationAuthorization = r.Header.Get("Authorization")
		w.Header().Set("content-type", "application/atom+xml")
		_, _ = w.Write([]byte(`<feed><title>Destination</title></feed>`))
	}))
	defer destination.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer origin.Close()
	credentialURL := strings.Replace(origin.URL, "://", "://feed:secret@", 1) + "/feed.atom"
	resolvedURL := destination.URL + "/feed.atom"
	result, err := NewHTTPFetcherWithOptions(nil, HTTPFetcherOptions{AllowPrivateNetwork: true}).Fetch(
		context.Background(),
		store.Feed{URL: credentialURL, NormalizedURL: credentialURL, ResolvedURL: resolvedURL},
		Options{MaxBodyBytes: DefaultMaxBodyBytes},
	)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if destinationAuthorization != "" {
		t.Fatalf("cross-origin Authorization = %q, want empty", destinationAuthorization)
	}
	if result.RequestURL != resolvedURL || result.FinalURL != resolvedURL {
		t.Fatalf("result URLs = (%q, %q), want %q", result.RequestURL, result.FinalURL, resolvedURL)
	}
}

func TestSanitizeFeedCredentialURLsInText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "absolute malformed port",
			input: `parse "http://user:password@example.com:bad/feed" failed`,
			want:  `parse "http://example.com:bad/feed" failed`,
		},
		{
			name:  "network path",
			input: `parse "//user:password@example.com:bad/feed" failed`,
			want:  `parse "//example.com:bad/feed" failed`,
		},
		{
			name:  "uppercase scheme",
			input: `parse "HTTPS://user:password@Example.com/feed" failed`,
			want:  `parse "HTTPS://Example.com/feed" failed`,
		},
		{
			name:  "multiple URLs",
			input: `first HTTP://a:b@one.example/a second //c:d@two.example/b`,
			want:  `first HTTP://one.example/a second //two.example/b`,
		},
		{
			name:  "last userinfo delimiter",
			input: `parse "http://first@second@example.com/feed" failed`,
			want:  `parse "http://example.com/feed" failed`,
		},
		{
			name:  "unchanged",
			input: `plain diagnostic http://example.com/feed and user@example.com`,
			want:  `plain diagnostic http://example.com/feed and user@example.com`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeFeedCredentialURLsInText(tt.input); got != tt.want {
				t.Fatalf("sanitizeFeedCredentialURLsInText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitizeFeedCredentialURLsInTextHasBoundedAllocations(t *testing.T) {
	var input strings.Builder
	input.Grow(256 * 100)
	for range 256 {
		input.WriteString(`failed HTTP://user:password@example.com:bad/feed then //other:secret@other.example:bad/path; `)
	}
	value := input.String()
	allocations := testing.AllocsPerRun(5, func() {
		sanitizedFeedTextSink = sanitizeFeedCredentialURLsInText(value)
	})
	if allocations > 8 {
		t.Fatalf("sanitize allocations = %.0f, want <= 8", allocations)
	}
	if strings.Contains(sanitizedFeedTextSink, "user:password@") || strings.Contains(sanitizedFeedTextSink, "other:secret@") {
		t.Fatalf("sanitized output retained credentials")
	}
}

func TestHTTPFetcherPreservesInjectedRedirectCallback(t *testing.T) {
	sentinel := errors.New("redirect sentinel")
	tests := []struct {
		name                  string
		crossOrigin           bool
		wantAuthorizationSeen bool
	}{
		{name: "same origin retains authorization", wantAuthorizationSeen: true},
		{name: "cross origin strips authorization", crossOrigin: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			destinationHits := 0
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				destinationHits++
				_, _ = w.Write([]byte(`<feed/>`))
			}))
			defer destination.Close()

			var origin *httptest.Server
			origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				location := origin.URL + "/feed.atom"
				if tt.crossOrigin {
					location = destination.URL + "/feed.atom"
				}
				http.Redirect(w, r, location, http.StatusFound)
			}))
			defer origin.Close()

			callbackCalls := 0
			authorizationSeen := false
			client := &http.Client{CheckRedirect: func(req *http.Request, _ []*http.Request) error {
				callbackCalls++
				authorizationSeen = req.Header.Get("Authorization") != ""
				return sentinel
			}}
			target := strings.Replace(origin.URL, "://", "://feed:secret@", 1) + "/start"
			_, err := NewHTTPFetcher(client).Fetch(
				context.Background(),
				store.Feed{URL: target, NormalizedURL: target, ResolvedURL: target},
				Options{MaxBodyBytes: DefaultMaxBodyBytes},
			)
			if !errors.Is(err, sentinel) {
				t.Fatalf("Fetch error = %v, want sentinel", err)
			}
			if callbackCalls != 1 {
				t.Fatalf("CheckRedirect calls = %d, want 1", callbackCalls)
			}
			if authorizationSeen != tt.wantAuthorizationSeen {
				t.Fatalf("CheckRedirect Authorization present = %t, want %t", authorizationSeen, tt.wantAuthorizationSeen)
			}
			if destinationHits != 0 {
				t.Fatalf("redirect destination reached %d times", destinationHits)
			}
		})
	}
}

func TestSameFeedHTTPOrigin(t *testing.T) {
	parse := func(raw string) *url.URL {
		t.Helper()
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("url.Parse(%q): %v", raw, err)
		}
		return parsed
	}
	tests := []struct {
		name  string
		left  *url.URL
		right *url.URL
		want  bool
	}{
		{name: "http default port", left: parse("http://Example.com/path"), right: parse("http://example.com:80/next"), want: true},
		{name: "https default port", left: parse("https://example.com/path"), right: parse("https://example.com:443/next"), want: true},
		{name: "trailing dot", left: parse("https://example.com./path"), right: parse("https://example.com/next"), want: true},
		{name: "scheme differs", left: parse("http://example.com/path"), right: parse("https://example.com/path")},
		{name: "host differs", left: parse("https://example.com/path"), right: parse("https://other.example/path")},
		{name: "port differs", left: parse("https://example.com:443/path"), right: parse("https://example.com:444/path")},
		{name: "nil target", left: nil, right: parse("https://example.com/path")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameFeedHTTPOrigin(tt.left, tt.right); got != tt.want {
				t.Fatalf("sameFeedHTTPOrigin(%v, %v) = %t, want %t", tt.left, tt.right, got, tt.want)
			}
		})
	}
}

func TestHTTPFetcherUsesVersionedDefaultUserAgent(t *testing.T) {
	var userAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.UserAgent()
		w.Header().Set("content-type", "application/rss+xml")
		_, _ = w.Write([]byte(`<rss><channel><title>Local</title></channel></rss>`))
	}))
	defer server.Close()

	_, err := NewHTTPFetcherWithOptions(nil, HTTPFetcherOptions{AllowPrivateNetwork: true}).Fetch(context.Background(), store.Feed{
		URL:           server.URL + "/feed.xml",
		NormalizedURL: server.URL + "/feed.xml",
		ResolvedURL:   server.URL + "/feed.xml",
	}, normalizeOptions(Options{MaxBodyBytes: DefaultMaxBodyBytes}))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.HasPrefix(userAgent, "dbrain/") || strings.Contains(userAgent, "feed importer") {
		t.Fatalf("unexpected user-agent: %q", userAgent)
	}
}

func TestHTTPFetcherForceSkipsConditionalHeaders(t *testing.T) {
	var ifNoneMatch string
	var ifModifiedSince string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ifNoneMatch = r.Header.Get("if-none-match")
		ifModifiedSince = r.Header.Get("if-modified-since")
		w.Header().Set("content-type", "application/rss+xml")
		_, _ = w.Write([]byte(`<rss><channel><title>Forced</title></channel></rss>`))
	}))
	defer server.Close()

	_, err := NewHTTPFetcherWithOptions(nil, HTTPFetcherOptions{AllowPrivateNetwork: true}).Fetch(context.Background(), store.Feed{
		URL:               server.URL + "/feed.xml",
		NormalizedURL:     server.URL + "/feed.xml",
		ResolvedURL:       server.URL + "/feed.xml",
		FetchETag:         `"v1"`,
		FetchLastModified: "Sat, 09 May 2026 00:00:00 GMT",
	}, Options{Force: true, MaxBodyBytes: DefaultMaxBodyBytes})
	if err != nil {
		t.Fatalf("Fetch force: %v", err)
	}
	if ifNoneMatch != "" || ifModifiedSince != "" {
		t.Fatalf("force should not send validators, got if-none-match=%q if-modified-since=%q", ifNoneMatch, ifModifiedSince)
	}
}
