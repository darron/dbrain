package feedimport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/store"
)

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
	if !strings.Contains(err.Error(), "no public IPs resolved") {
		t.Fatalf("expected public-IP guard error, got %v", err)
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
