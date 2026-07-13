package sourceenrich

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/safehttp"
)

func TestFirstMarkdownTitleReadsReaderTitleLine(t *testing.T) {
	t.Parallel()

	content := "Title: Government of Canada announces renewed funding\n\nURL Source: https://canada.ca/example\n\nMarkdown Content:\n# Government of Canada announces renewed funding"
	if got := firstMarkdownTitle(content); got != "Government of Canada announces renewed funding" {
		t.Fatalf("expected reader title line, got %q", got)
	}
}

func TestExtractHTTPReadableSourceRejectsPrivateDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/html")
		_, _ = w.Write([]byte("<html><body>private source</body></html>"))
	}))
	defer server.Close()

	_, _, err := extractHTTPReadableSource(context.Background(), server.URL)
	if !safehttp.IsPolicyError(err) {
		t.Fatalf("error = %v, want shared HTTP policy rejection", err)
	}
}

func TestExtractHTTPReadableSourceRejectsRedirectToPrivateDestination(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Host == "public.test" {
			http.Redirect(w, r, "http://private.test/article", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("private article"))
	}))
	defer server.Close()

	policy := safehttp.Policy{
		LookupNetIP: func(_ context.Context, _ string, host string) ([]netip.Addr, error) {
			if host == "public.test" {
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, server.Listener.Addr().String())
		},
	}
	_, _, err := extractHTTPReadableSource(context.Background(), "http://public.test/article", Options{httpPolicy: &policy})
	if !safehttp.IsPolicyError(err) {
		t.Fatalf("error = %v, want redirect policy rejection", err)
	}
	if hits != 1 {
		t.Fatalf("server hits = %d, want only initial public request", hits)
	}
}

func TestFetchWordPressJSONExtractRejectsPrivateResponseDerivedURL(t *testing.T) {
	client := safehttp.NewClient(safehttp.Policy{
		LookupNetIP: func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
	})

	_, _, err := fetchWordPressJSONExtract(context.Background(), client, "https://public.test/article", "http://private.test/wp-json/post")
	if !safehttp.IsPolicyError(err) {
		t.Fatalf("error = %v, want response-derived URL policy rejection", err)
	}
}

func TestDefaultResolveRedirectURLRejectsPrivateDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("private redirect target"))
	}))
	defer server.Close()

	_, err := defaultResolveRedirectURL(context.Background(), server.URL)
	if !safehttp.IsPolicyError(err) {
		t.Fatalf("error = %v, want shared HTTP policy rejection", err)
	}
}

func TestFetchMakerWorldAPITextRejectsPrivateDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer server.Close()

	_, err := fetchMakerWorldAPIText(context.Background(), server.URL)
	if !safehttp.IsPolicyError(err) {
		t.Fatalf("error = %v, want shared HTTP policy rejection", err)
	}
}

func TestExtractWaybackArchivedSourceRejectsPrivateSnapshotURL(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/available") {
			w.Header().Set("content-type", "application/json")
			_, _ = fmt.Fprintf(w, `{"archived_snapshots":{"closest":{"available":true,"url":%q,"timestamp":"20260712000000","status":"200"}}}`, server.URL+"/web/20260712000000/https://example.com/article")
			return
		}
		w.Header().Set("content-type", "text/html")
		_, _ = w.Write([]byte("<html><body>private snapshot</body></html>"))
	}))
	defer server.Close()

	_, _, err := extractWaybackArchivedSource(context.Background(), "https://example.com/article", Options{
		WaybackFallbackEnabled: true,
		WaybackAvailabilityURL: server.URL + "/available?url={escaped_url}",
	})
	if !safehttp.IsPolicyError(err) {
		t.Fatalf("error = %v, want private response-derived snapshot rejection", err)
	}
}
