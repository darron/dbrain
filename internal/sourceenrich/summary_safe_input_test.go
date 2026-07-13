package sourceenrich

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/safehttp"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
	"github.com/darron/dbrain/internal/vault"
)

func TestRunSourceIDsPersistsPrivateSourcePolicyFailuresWithoutSubprocess(t *testing.T) {
	tests := []struct {
		name      string
		sourceURL func(*httptest.Server) string
		policy    func(*httptest.Server) *safehttp.Policy
		wantHits  int
	}{
		{
			name:      "direct private",
			sourceURL: func(server *httptest.Server) string { return server.URL + "/article" },
			wantHits:  0,
		},
		{
			name:      "public redirect to private",
			sourceURL: func(*httptest.Server) string { return "http://public.test/article" },
			policy: func(server *httptest.Server) *safehttp.Policy {
				policy := syntheticPublicPolicy(server.Listener.Addr().String(), map[string]string{
					"public.test":  "8.8.8.8",
					"private.test": "127.0.0.1",
				})
				return &policy
			},
			wantHits: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hits := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.Host == "public.test" {
					http.Redirect(w, r, "http://private.test/article", http.StatusFound)
					return
				}
				_, _ = w.Write([]byte("private source"))
			}))
			defer server.Close()

			cfg, err := config.Load(t.TempDir())
			if err != nil {
				t.Fatalf("load config: %v", err)
			}
			if err := cfg.EnsureDirs(); err != nil {
				t.Fatalf("ensure dirs: %v", err)
			}
			st, err := store.Open(cfg.DBPath)
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			defer func() { _ = st.Close() }()

			now := time.Now().UTC()
			item, err := st.UpsertItem(context.Background(), model.Item{
				SourceKey: "x:safe-input-" + strings.ReplaceAll(test.name, " ", "-"), SourceType: "x_bookmark",
				ExternalID: test.name, CanonicalURL: "https://x.com/example/status/1", Title: test.name,
				ContentHash: test.name, NotePath: vault.NoteRelativePath("x", "2026", test.name), RawJSON: `{}`,
				ImportedAt: now, UpdatedAt: now, LastSeenAt: now,
			})
			if err != nil {
				t.Fatalf("upsert item: %v", err)
			}
			sourceURL := test.sourceURL(server)
			link, err := st.UpsertSourceLink(context.Background(), item.ItemID, model.SourceCandidate{
				SourceKey: "src:" + strings.ReplaceAll(test.name, " ", "-"), OriginalURL: sourceURL,
				CanonicalURL: sourceURL, NormalizedURL: sourceURL, SourceType: "web", Domain: "test",
				NotePath: vault.SourceNoteRelativePath("web", test.name),
			})
			if err != nil {
				t.Fatalf("upsert source: %v", err)
			}

			binary, marker := installSafeInputFakeSummarize(t)
			var policy *safehttp.Policy
			if test.policy != nil {
				policy = test.policy(server)
			}
			stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
				Limit: 1, Binary: binary, Timeout: time.Second, httpPolicy: policy,
				ResolveHost: func(context.Context, string) error { return nil },
			})
			if err != nil {
				t.Fatalf("RunSourceIDs: %v", err)
			}
			if stats.Errors != 1 {
				t.Fatalf("stats = %+v, want one terminal policy error", stats)
			}
			stored, err := st.GetSourceByID(context.Background(), link.SourceID)
			if err != nil {
				t.Fatalf("get source: %v", err)
			}
			if stored.ExtractStatus != model.SourceExtractStatusDead || stored.ExtractFailureCount != 1 || !strings.Contains(stored.ExtractError, "safe HTTP policy") {
				t.Fatalf("stored source = %+v, want immediate terminal policy state", stored)
			}
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatalf("summarizer marker stat = %v, want no extraction subprocess", statErr)
			}
			if hits != test.wantHits {
				t.Fatalf("server hits = %d, want %d", hits, test.wantHits)
			}
		})
	}
}

func TestRunSummarizeRejectsPrivateSourceBeforeSubprocess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("private source"))
	}))
	defer server.Close()

	binary, marker := installSafeInputFakeSummarize(t)
	_, err := runSummarizeWithRedirectRetry(context.Background(), model.SourceDocument{SourceType: "web"}, Options{}, summarizecli.Options{
		Binary:  binary,
		Input:   server.URL,
		Timeout: time.Second,
	})
	if !safehttp.IsPolicyError(err) {
		t.Fatalf("error = %v, want policy rejection", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("summarizer marker stat = %v, want subprocess not invoked", statErr)
	}
}

func TestRunSummarizeRejectsNonHTTPSourceBeforeSubprocess(t *testing.T) {
	binary, marker := installSafeInputFakeSummarize(t)
	_, err := runSummarizeWithRedirectRetry(context.Background(), model.SourceDocument{SourceType: "web"}, Options{}, summarizecli.Options{
		Binary: binary, Input: "file:///etc/passwd", Timeout: time.Second,
	})
	if !safehttp.IsPolicyError(err) {
		t.Fatalf("error = %v, want imported URL policy rejection", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("summarizer marker stat = %v, want subprocess not invoked", statErr)
	}
}

func TestRunSummarizeRejectsNonYouTubeURLForYouTubeSourceBeforeSubprocess(t *testing.T) {
	binary, marker := installSafeInputFakeSummarize(t)
	_, err := runSummarizeWithRedirectRetry(context.Background(), model.SourceDocument{SourceType: "youtube"}, Options{}, summarizecli.Options{
		Binary: binary, Input: "https://127.0.0.1/video", Timeout: time.Second,
	})
	if !safehttp.IsPolicyError(err) {
		t.Fatalf("error = %v, want YouTube sink policy rejection", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("summarizer marker stat = %v, want subprocess not invoked", statErr)
	}
}

func TestYouTubeAudioFallbackRejectsNonYouTubeURLBeforeYTDLP(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	binary, marker := installSafeInputFakeSummarize(t)
	_, err = transcribeYouTubeAudioFallback(context.Background(), cfg, model.SourceDocument{
		SourceType: "youtube", CanonicalURL: "https://127.0.0.1/video",
	}, model.ExtractResult{}, Options{YTDLPBinary: binary})
	if !safehttp.IsPolicyError(err) {
		t.Fatalf("error = %v, want yt-dlp sink policy rejection", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("yt-dlp marker stat = %v, want subprocess not invoked", statErr)
	}
}

func TestValidateYouTubeSubprocessURL(t *testing.T) {
	for _, rawURL := range []string{
		"https://youtube.com/watch?v=1",
		"https://www.youtube.com/watch?v=1",
		"https://youtu.be/1",
		"https://youtube.com:443/watch?v=1",
	} {
		if err := validateYouTubeSubprocessURL(rawURL); err != nil {
			t.Errorf("validate %q: %v", rawURL, err)
		}
	}
	for _, rawURL := range []string{
		"http://youtube.com/watch?v=1",
		"https://user@youtube.com/watch?v=1",
		"https://youtube.com:8443/watch?v=1",
		"https://evil.youtube.com/watch?v=1",
		"https://127.0.0.1/watch?v=1",
	} {
		if err := validateYouTubeSubprocessURL(rawURL); !safehttp.IsPolicyError(err) {
			t.Errorf("validate %q error = %v, want policy rejection", rawURL, err)
		}
	}
}

func TestRunSummarizeRejectsPrivateRedirectBeforeSubprocess(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Host == "public.test" {
			http.Redirect(w, r, "http://private.test/article", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("private source"))
	}))
	defer server.Close()

	policy := syntheticPublicPolicy(server.Listener.Addr().String(), map[string]string{
		"public.test":  "8.8.8.8",
		"private.test": "127.0.0.1",
	})
	binary, marker := installSafeInputFakeSummarize(t)
	_, err := runSummarizeWithRedirectRetry(context.Background(), model.SourceDocument{SourceType: "web"}, Options{httpPolicy: &policy}, summarizecli.Options{
		Binary:  binary,
		Input:   "http://public.test/article",
		Timeout: time.Second,
	})
	if !safehttp.IsPolicyError(err) {
		t.Fatalf("error = %v, want redirect policy rejection", err)
	}
	if hits != 1 {
		t.Fatalf("server hits = %d, want only public first hop", hits)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("summarizer marker stat = %v, want subprocess not invoked", statErr)
	}
}

func TestRunSummarizePassesPublicSourceToSubprocessAsLocalFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/html")
		_, _ = w.Write([]byte("<html><body>public source body</body></html>"))
	}))
	defer server.Close()

	policy := syntheticPublicPolicy(server.Listener.Addr().String(), map[string]string{"public.test": "8.8.8.8"})
	binary, marker := installSafeInputFakeSummarize(t)
	result, err := runSummarizeWithRedirectRetry(context.Background(), model.SourceDocument{SourceType: "web"}, Options{httpPolicy: &policy}, summarizecli.Options{
		Binary:  binary,
		Input:   "http://public.test/article.html",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("run summarize: %v", err)
	}
	markerBody, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read subprocess marker: %v", err)
	}
	inputPath := strings.TrimSpace(string(markerBody))
	if inputPath == "" || inputPath == "http://public.test/article.html" {
		t.Fatalf("subprocess input = %q, want local file", inputPath)
	}
	if !strings.HasPrefix(result.Extract.CanonicalURL, "http://public.test/") || result.Extract.FinalURL != "http://public.test/article.html" {
		t.Fatalf("URL provenance not restored: %+v", result.Extract)
	}
}

func TestPreparePublicSourceInputConfinesConfiguredPrivateOrigin(t *testing.T) {
	configuredHits := 0
	configured := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		configuredHits++
		_, _ = w.Write([]byte("configured service body"))
	}))
	defer configured.Close()

	unrelatedHits := 0
	unrelated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		unrelatedHits++
		_, _ = w.Write([]byte("unrelated private body"))
	}))
	defer unrelated.Close()

	opts := WithConfiguredSourceOrigin(Options{}, configured.URL)
	path, _, cleanup, err := preparePublicSourceInput(context.Background(), configured.URL+"/homepage", opts)
	if err != nil {
		t.Fatalf("configured source input: %v", err)
	}
	cleanup()
	if path == "" || configuredHits != 1 {
		t.Fatalf("configured path=%q hits=%d, want local input and one request", path, configuredHits)
	}

	_, _, _, err = preparePublicSourceInput(context.Background(), unrelated.URL+"/homepage", opts)
	if !safehttp.IsPolicyError(err) {
		t.Fatalf("unrelated error = %v, want policy rejection", err)
	}
	if unrelatedHits != 0 {
		t.Fatalf("unrelated private hits = %d, want 0", unrelatedHits)
	}
}

func installSafeInputFakeSummarize(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "invoked")
	binary := filepath.Join(dir, "summarize")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo test-1.0.0
  exit 0
fi
last=""
for arg in "$@"; do last="$arg"; done
printf '%%s' "$last" > %q
test -f "$last" || { echo "expected local input" >&2; exit 1; }
grep -q "public source body" "$last" || { echo "missing source body" >&2; exit 1; }
printf '%%s\n' '{"input":{"model":"auto"},"extracted":{"url":"","title":"Public","description":"","siteName":"Test","content":"public source body"},"summary":null}'
`, marker)
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}
	return binary, marker
}

func syntheticPublicPolicy(serverAddress string, hosts map[string]string) safehttp.Policy {
	return safehttp.Policy{
		LookupNetIP: func(_ context.Context, _ string, host string) ([]netip.Addr, error) {
			value, ok := hosts[host]
			if !ok {
				return nil, fmt.Errorf("unexpected host %q", host)
			}
			return []netip.Addr{netip.MustParseAddr(value)}, nil
		},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, serverAddress)
		},
	}
}
