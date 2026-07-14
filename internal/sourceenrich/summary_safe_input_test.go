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

func TestRunSummarizeExtractsPrefetchedHTMLWithoutUnsupportedLocalFileCLI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/html")
		_, _ = w.Write([]byte("<html><head><title>Public article</title></head><body><article>public source body</article></body></html>"))
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
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("summarizer marker stat = %v, want prefetched HTML parsed without unsupported local-file --extract", statErr)
	}
	if result.Extract.Title != "Public article" || result.Extract.Content != "public source body" {
		t.Fatalf("in-process extract = %+v", result.Extract)
	}
	if !strings.HasPrefix(result.Extract.CanonicalURL, "http://public.test/") || result.Extract.FinalURL != "http://public.test/article.html" {
		t.Fatalf("URL provenance not restored: %+v", result.Extract)
	}
	if result.Extract.Tool != protectedFetchToolName {
		t.Fatalf("extract tool = %q, want %q", result.Extract.Tool, protectedFetchToolName)
	}
}

func TestRunSummarizeExtractsPrefetchedPlainTextWithoutUnsupportedLocalFileCLI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/plain")
		_, _ = w.Write([]byte("# Public text\n\nplain source body"))
	}))
	defer server.Close()

	policy := syntheticPublicPolicy(server.Listener.Addr().String(), map[string]string{"public.test": "8.8.8.8"})
	binary, marker := installSafeInputFakeSummarize(t)
	result, err := runSummarizeWithRedirectRetry(context.Background(), model.SourceDocument{SourceType: "web"}, Options{httpPolicy: &policy}, summarizecli.Options{
		Binary:  binary,
		Input:   "http://public.test/article.txt",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("run summarize: %v", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("summarizer marker stat = %v, want prefetched text parsed without unsupported local-file --extract", statErr)
	}
	if result.Extract.Title != "Public text" || result.Extract.Content != "# Public text\n\nplain source body" {
		t.Fatalf("in-process extract = %+v", result.Extract)
	}
	if result.Extract.CanonicalURL != "http://public.test/article.txt" || result.Extract.FinalURL != "http://public.test/article.txt" {
		t.Fatalf("URL provenance not restored: %+v", result.Extract)
	}
}

func TestRunSummarizeHonorsDeclaredHTMLBeyondSniffWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(strings.Repeat("preamble ", 80) + "<main>declared HTML body</main>"))
	}))
	defer server.Close()

	policy := syntheticPublicPolicy(server.Listener.Addr().String(), map[string]string{"public.test": "8.8.8.8"})
	binary, marker := installSafeInputFakeSummarize(t)
	result, err := runSummarizeWithRedirectRetry(context.Background(), model.SourceDocument{SourceType: "web"}, Options{httpPolicy: &policy}, summarizecli.Options{
		Binary:  binary,
		Input:   "http://public.test/article",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("run summarize: %v", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("summarizer marker stat = %v, want declared HTML parsed without subprocess", statErr)
	}
	if result.Extract.Content != "declared HTML body" {
		t.Fatalf("declared HTML extract = %q, want main content without markup or preamble", result.Extract.Content)
	}
}

func TestRunSourceIDsBlocksEmptySafeFetchedSummaryAndStopsRequeue(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.Load(root)
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
		SourceKey: "x:empty-safe-summary", SourceType: "x_bookmark", ExternalID: "empty-safe-summary",
		CanonicalURL: "https://x.com/example/status/empty-safe-summary", Title: "empty safe summary",
		ContentHash: "empty-safe-summary", NotePath: vault.NoteRelativePath("x", "2026", "empty-safe-summary"),
		RawJSON: `{}`, ImportedAt: now, UpdatedAt: now, LastSeenAt: now,
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	link, err := st.UpsertSourceLink(context.Background(), item.ItemID, model.SourceCandidate{
		SourceKey: "src:empty-safe-summary", OriginalURL: "https://example.com/empty",
		CanonicalURL: "https://example.com/empty", NormalizedURL: "https://example.com/empty",
		SourceType: "web", Domain: "example.com", NotePath: vault.SourceNoteRelativePath("web", "empty-safe-summary"),
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}

	binary, marker := installSafeInputFakeSummarize(t)
	opts := Options{
		Summarize: true, Model: "cli/test/model", Binary: binary, Timeout: time.Second,
		prepareSourceInput: fixedSourceInputPreparer(t, "https://example.com/empty", "<html><body></body></html>"),
	}
	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, opts)
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.SourcesSummarized != 0 {
		t.Fatalf("expected no successful summary, got %+v", stats)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("summarizer marker stat = %v, want empty extract blocked before subprocess", statErr)
	}
	stored, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if stored.ExtractStatus != model.SourceExtractStatusEmpty || stored.SummaryStatus != model.SourceSummaryStatusBlocked {
		t.Fatalf("stored statuses extract=%q summary=%q, want empty/blocked", stored.ExtractStatus, stored.SummaryStatus)
	}
	if stored.SummaryError != "no extracted content available for summary" {
		t.Fatalf("summary error = %q", stored.SummaryError)
	}
	pending, err := st.ListSourcesForEnrichment(context.Background(), 10, false, true, SummaryPromptVersion,
		summarizecli.SummaryToolNameForRoot(cfg.RootDir, opts.Model),
		summarizecli.SummaryToolVersionForRoot(context.Background(), cfg.RootDir, opts.Binary, opts.Model))
	if err != nil {
		t.Fatalf("list pending sources: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected blocked empty source not to requeue, got %d candidates", len(pending))
	}
}

func TestRunSummarizeSendsSafeExtractToSummaryStdin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/html")
		_, _ = w.Write([]byte("<html><head><title>Public article</title></head><body><main>public source body</main></body></html>"))
	}))
	defer server.Close()

	policy := syntheticPublicPolicy(server.Listener.Addr().String(), map[string]string{"public.test": "8.8.8.8"})
	binary, argsMarker, stdinMarker := installSafeInputStdinSummaryFake(t)
	result, err := runSummarizeWithRedirectRetry(context.Background(), model.SourceDocument{SourceType: "web"}, Options{httpPolicy: &policy}, summarizecli.Options{
		Binary:    binary,
		Input:     "http://public.test/article.html",
		Summarize: true,
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("run summarize: %v", err)
	}
	argsBody, err := os.ReadFile(argsMarker)
	if err != nil {
		t.Fatalf("read argument marker: %v", err)
	}
	argsText := string(argsBody)
	if !strings.HasSuffix(argsText, "-\n") || strings.Contains(argsText, "http://public.test") || strings.Contains(argsText, "dbrain-source-input-") {
		t.Fatalf("summary subprocess args = %q, want stdin sentinel without URL or local path", argsText)
	}
	stdinBody, err := os.ReadFile(stdinMarker)
	if err != nil {
		t.Fatalf("read stdin marker: %v", err)
	}
	if string(stdinBody) != "public source body" {
		t.Fatalf("summary stdin = %q, want safe extracted text", string(stdinBody))
	}
	if result.Extract.Title != "Public article" || result.Extract.Content != "public source body" || result.Extract.CanonicalURL != "http://public.test/article.html" {
		t.Fatalf("safe extract replaced by summarize asset envelope: %+v", result.Extract)
	}
	if result.Summary.Status != model.SourceSummaryStatusOK || result.Summary.Text != "safe summary" || result.Summary.Model != "fake-model" {
		t.Fatalf("summary = %+v", result.Summary)
	}
}

func TestRunSummarizeDelegatesSafePrefetchedPDFAsLocalFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4\nfake PDF body"))
	}))
	defer server.Close()

	policy := syntheticPublicPolicy(server.Listener.Addr().String(), map[string]string{"public.test": "8.8.8.8"})
	binary, marker := installSafeInputPDFFake(t)
	result, err := runSummarizeWithRedirectRetry(context.Background(), model.SourceDocument{SourceType: "web"}, Options{httpPolicy: &policy}, summarizecli.Options{
		Binary:  binary,
		Input:   "http://public.test/document.pdf",
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
	if inputPath == "" || inputPath == "http://public.test/document.pdf" {
		t.Fatalf("PDF subprocess input = %q, want safe local file", inputPath)
	}
	if _, statErr := os.Stat(inputPath); !os.IsNotExist(statErr) {
		t.Fatalf("temporary PDF stat after summarize = %v, want cleanup", statErr)
	}
	if result.Extract.Content != "pdf extracted text" || result.Extract.CanonicalURL != "http://public.test/document.pdf" || result.Extract.FinalURL != "http://public.test/document.pdf" {
		t.Fatalf("PDF result = %+v", result.Extract)
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
	prepared, err := preparePublicSourceInput(context.Background(), configured.URL+"/homepage", opts)
	if err != nil {
		t.Fatalf("configured source input: %v", err)
	}
	prepared.Cleanup()
	if prepared.Path == "" || configuredHits != 1 {
		t.Fatalf("configured path=%q hits=%d, want local input and one request", prepared.Path, configuredHits)
	}

	_, err = preparePublicSourceInput(context.Background(), unrelated.URL+"/homepage", opts)
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

func installSafeInputStdinSummaryFake(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	argsMarker := filepath.Join(dir, "args")
	stdinMarker := filepath.Join(dir, "stdin")
	binary := filepath.Join(dir, "summarize")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo test-1.0.0
  exit 0
fi
printf '%%s\n' "$@" > %q
cat > %q
last=""
for arg in "$@"; do last="$arg"; done
test "$last" = "-" || { echo "expected stdin input" >&2; exit 1; }
grep -q "public source body" %q || { echo "missing safe extracted stdin" >&2; exit 1; }
printf '%%s\n' '{"input":{"model":"fake-model"},"extracted":{"kind":"asset","source":"stdin","mediaType":"text/plain","filename":"stdin"},"summary":"safe summary"}'
`, argsMarker, stdinMarker, stdinMarker)
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}
	return binary, argsMarker, stdinMarker
}

func installSafeInputPDFFake(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "input")
	binary := filepath.Join(dir, "summarize")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo test-1.0.0
  exit 0
fi
last=""
for arg in "$@"; do last="$arg"; done
test -f "$last" || { echo "expected local PDF input" >&2; exit 1; }
grep -q "%%PDF-1.4" "$last" || { echo "missing PDF body" >&2; exit 1; }
printf '%%s' "$last" > %q
printf '%%s\n' '{"input":{"model":"auto"},"extracted":{"url":"","title":"Document","description":"","siteName":"","content":"pdf extracted text"},"summary":null}'
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
