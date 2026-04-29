package githubimport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

func TestRunImportsStarsAndHomepageSources(t *testing.T) {
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
	defer func() {
		_ = st.Close()
	}()

	server := newGitHubTestServer(t)
	defer server.Close()
	installGitHubFakeSummarize(t, root, server.URL+"/project")

	stats, err := Run(context.Background(), cfg, st, Options{
		Summarize: true,
		Model:     "cli/test/github",
		Token:     "test-token",
		APIBase:   server.URL,
		Timeout:   5 * time.Second,
		Length:    "short",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.ItemsCreated != 1 {
		t.Fatalf("expected 1 created item, got %d", stats.ItemsCreated)
	}
	if stats.SourcesSummarized != 2 {
		t.Fatalf("expected 2 summarized sources, got %d", stats.SourcesSummarized)
	}

	repoSource, err := st.GetSource(context.Background(), "https://github.com/example/project")
	if err != nil {
		t.Fatalf("get repo source: %v", err)
	}
	if repoSource.ExtractTool != "github-api" {
		t.Fatalf("expected github-api extract tool, got %q", repoSource.ExtractTool)
	}
	if !strings.Contains(repoSource.ExtractedText, "README CONTENT FROM GITHUB API") {
		t.Fatalf("expected README content in extract, got %q", repoSource.ExtractedText)
	}
	if repoSource.SummaryText != "github repo summary" {
		t.Fatalf("unexpected repo summary: %q", repoSource.SummaryText)
	}

	homepageSource, err := st.GetSource(context.Background(), server.URL+"/project")
	if err != nil {
		t.Fatalf("get homepage source: %v", err)
	}
	if homepageSource.SourceType != "web" {
		t.Fatalf("expected homepage source type web, got %q", homepageSource.SourceType)
	}
	if homepageSource.SummaryText != "website summary" {
		t.Fatalf("unexpected homepage summary: %q", homepageSource.SummaryText)
	}
}

func TestRunStopsAtFirstExistingStar(t *testing.T) {
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
	defer func() {
		_ = st.Close()
	}()

	server := newGitHubTestServer(t)
	defer server.Close()
	installGitHubFakeSummarize(t, root, server.URL+"/project")

	first, err := Run(context.Background(), cfg, st, Options{
		Summarize: true,
		Model:     "cli/test/github",
		Token:     "test-token",
		APIBase:   server.URL,
		Timeout:   5 * time.Second,
		Length:    "short",
	})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.ItemsCreated != 1 {
		t.Fatalf("expected first run to create 1 item, got %d", first.ItemsCreated)
	}

	second, err := Run(context.Background(), cfg, st, Options{
		Summarize: true,
		Model:     "cli/test/github",
		Token:     "test-token",
		APIBase:   server.URL,
		Timeout:   5 * time.Second,
		Length:    "short",
	})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.ItemsCreated != 0 {
		t.Fatalf("expected second run to create 0 items, got %d", second.ItemsCreated)
	}
	if second.StarsProcessed != 1 {
		t.Fatalf("expected second run to stop after first existing star, got %d processed", second.StarsProcessed)
	}
}

func TestRunLoadsTokenFromEnvrc(t *testing.T) {
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
	defer func() {
		_ = st.Close()
	}()

	server := newGitHubTestServer(t)
	defer server.Close()
	installGitHubFakeSummarize(t, root, server.URL+"/project")

	t.Setenv("GITHUB_TOKEN", "")
	envrcPath := filepath.Join(root, ".envrc")
	if err := os.WriteFile(envrcPath, []byte("export GITHUB_TOKEN=test-token\n"), 0o644); err != nil {
		t.Fatalf("write envrc: %v", err)
	}

	stats, err := Run(context.Background(), cfg, st, Options{
		Summarize: true,
		Model:     "cli/test/github",
		APIBase:   server.URL,
		Timeout:   5 * time.Second,
		Length:    "short",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.ItemsCreated != 1 {
		t.Fatalf("expected 1 created item, got %d", stats.ItemsCreated)
	}
}

func newGitHubTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"login": "darron"})
	})
	mux.HandleFunc("/user/starred", func(w http.ResponseWriter, r *http.Request) {
		star := []map[string]any{
			{
				"starred_at": "2026-04-20T12:00:00Z",
				"repo": map[string]any{
					"id":             1,
					"name":           "project",
					"full_name":      "example/project",
					"html_url":       "https://github.com/example/project",
					"description":    "A useful project",
					"homepage":       serverProjectURL(r),
					"language":       "Go",
					"topics":         []string{"agents", "search"},
					"default_branch": "main",
					"private":        false,
					"archived":       false,
					"disabled":       false,
					"fork":           false,
					"created_at":     "2026-01-01T00:00:00Z",
					"updated_at":     "2026-04-18T00:00:00Z",
					"pushed_at":      "2026-04-19T00:00:00Z",
					"owner": map[string]any{
						"login": "example",
						"type":  "Organization",
					},
					"license": map[string]any{
						"key":     "mit",
						"name":    "MIT License",
						"spdx_id": "MIT",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(star)
	})
	mux.HandleFunc("/repos/example/project/readme", func(w http.ResponseWriter, r *http.Request) {
		content := base64.StdEncoding.EncodeToString([]byte("README CONTENT FROM GITHUB API"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":     "README.md",
			"path":     "README.md",
			"encoding": "base64",
			"content":  content,
			"html_url": "https://github.com/example/project/blob/main/README.md",
		})
	})

	return httptest.NewServer(mux)
}

func serverProjectURL(r *http.Request) string {
	return "http://" + r.Host + "/project"
}

func installGitHubFakeSummarize(t *testing.T, root string, homepageURL string) {
	t.Helper()

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}

	scriptPath := filepath.Join(binDir, "summarize")
	script := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "version" ]; then
  echo "test-1.0.0"
  exit 0
fi
last=""
for arg in "$@"; do
  last="$arg"
done
if [ -f "$last" ]; then
  input="$(cat "$last")"
  case "$input" in
    *"README CONTENT FROM GITHUB API"*) ;;
    *)
      echo "expected github README in summary file" >&2
      exit 1
      ;;
  esac
  printf '%s\n' '{"input":{"model":"cli/test/github"},"extracted":{"url":"","title":"","description":"","siteName":"","content":"README CONTENT FROM GITHUB API"},"summary":"github repo summary"}'
  exit 0
fi
if [ "$last" = "` + homepageURL + `" ]; then
  printf '%s\n' '{"input":{"model":"cli/test/github"},"extracted":{"url":"` + homepageURL + `","title":"Project Website","description":"Homepage description","siteName":"Example","content":"WEBSITE HOME TEXT"},"summary":"website summary"}'
  exit 0
fi
echo "unexpected summarize input: $last" >&2
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
