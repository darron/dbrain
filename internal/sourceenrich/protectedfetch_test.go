package sourceenrich

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/store"
	"dbrain/internal/vault"
)

func TestSolveSucuriChallengeCookie(t *testing.T) {
	challengeHTML := sucuriChallengeHTML("sucuri_cloudproxy_uuid_test", "allowme")

	cookie, err := solveSucuriChallengeCookie(challengeHTML)
	if err != nil {
		t.Fatalf("solveSucuriChallengeCookie: %v", err)
	}
	if cookie.Name != "sucuri_cloudproxy_uuid_test" {
		t.Fatalf("unexpected cookie name: %q", cookie.Name)
	}
	if cookie.Value != "allowme" {
		t.Fatalf("unexpected cookie value: %q", cookie.Value)
	}
	if cookie.Path != "/" {
		t.Fatalf("unexpected cookie path: %q", cookie.Path)
	}
}

func TestRunSourceIDsRecoversSucuriProtectedSource(t *testing.T) {
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

	const (
		cookieName  = "sucuri_cloudproxy_uuid_test"
		cookieValue = "allowme"
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/post" {
			http.NotFound(w, r)
			return
		}
		cookie, _ := r.Cookie(cookieName)
		if cookie != nil && cookie.Value == cookieValue {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<!doctype html>
<html>
<head>
  <title>Protected Article</title>
  <meta property="og:site_name" content="Example Site">
  <meta name="description" content="Protected article description">
</head>
<body>
  <article>
    <h1>Protected Article</h1>
    <p>First paragraph.</p>
    <p>Second paragraph.</p>
  </article>
</body>
</html>`)
			return
		}

		w.Header().Set("Server", "Sucuri/Cloudproxy")
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = fmt.Fprint(w, sucuriChallengeHTML(cookieName, cookieValue))
	}))
	defer server.Close()

	installSourceEnrichProtectedFetchFakeSummarize(t, root, server.URL+"/post")

	now := time.Now().UTC()
	item := model.Item{
		SourceKey:    "x:test-protected-fetch",
		SourceType:   "x_bookmark",
		ExternalID:   "test-protected-fetch",
		CanonicalURL: "https://x.com/example/status/test-protected-fetch",
		Title:        "protected fetch",
		ContentHash:  "item-hash-protected-fetch",
		NotePath:     vault.NoteRelativePath("x", "2026", "test-protected-fetch"),
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}
	upserted, err := st.UpsertItem(context.Background(), item)
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	link, err := st.UpsertSourceLink(context.Background(), upserted.ItemID, model.SourceCandidate{
		SourceKey:     "src:protected-fetch-test",
		OriginalURL:   server.URL + "/post",
		CanonicalURL:  server.URL + "/post",
		NormalizedURL: server.URL + "/post",
		SourceType:    "web",
		Domain:        "127.0.0.1",
		NotePath:      vault.SourceNoteRelativePath("web", "protected-fetch-test"),
	})
	if err != nil {
		t.Fatalf("upsert source link: %v", err)
	}

	stats, _, err := RunSourceIDs(context.Background(), cfg, st, []int64{link.SourceID}, Options{
		Limit:     10,
		Summarize: true,
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("RunSourceIDs: %v", err)
	}
	if stats.SourcesExtracted != 1 {
		t.Fatalf("expected 1 extracted source, got %+v", stats)
	}
	if stats.SourcesSummarized != 1 {
		t.Fatalf("expected 1 summarized source, got %+v", stats)
	}
	if stats.Errors != 0 {
		t.Fatalf("expected no errors, got %+v", stats)
	}

	source, err := st.GetSourceByID(context.Background(), link.SourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if source.ExtractStatus != "ok" {
		t.Fatalf("expected extract status ok, got %q", source.ExtractStatus)
	}
	if source.ExtractTool != protectedFetchToolName {
		t.Fatalf("expected extract tool %q, got %q", protectedFetchToolName, source.ExtractTool)
	}
	if source.Title != "Protected Article" {
		t.Fatalf("unexpected title: %q", source.Title)
	}
	if source.Description != "Protected article description" {
		t.Fatalf("unexpected description: %q", source.Description)
	}
	if !strings.Contains(source.ExtractedText, "First paragraph.") || !strings.Contains(source.ExtractedText, "Second paragraph.") {
		t.Fatalf("unexpected extracted text: %q", source.ExtractedText)
	}
	if source.SummaryStatus != "ok" {
		t.Fatalf("expected summary status ok, got %q", source.SummaryStatus)
	}
	if source.SummaryText != "summary from protected fetch" {
		t.Fatalf("unexpected summary text: %q", source.SummaryText)
	}
}

func installSourceEnrichProtectedFetchFakeSummarize(t *testing.T, root string, sourceURL string) {
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
if [ "$last" = "` + sourceURL + `" ]; then
  echo "Failed to fetch HTML document (status 307)" >&2
  exit 1
fi
printf '%s\n' '{"input":{"model":"auto"},"extracted":{"url":"","title":"","description":"","siteName":"","content":"Protected Article\nFirst paragraph.\nSecond paragraph."},"summary":"summary from protected fetch"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func sucuriChallengeHTML(cookieName string, cookieValue string) string {
	decoded := fmt.Sprintf(`a=%q;document.cookie=%q + a + ';path=/;max-age=86400'; location.reload();`, cookieValue, cookieName+"=")
	encoded := base64.StdEncoding.EncodeToString([]byte(decoded))
	return fmt.Sprintf(`<html><title>You are being redirected...</title><noscript>Javascript is required. Please enable javascript before you are allowed to see this page.</noscript><script>var s={},u,c,U,r,i,l=0,a,e=eval,w=String.fromCharCode,sucuri_cloudproxy_js='',S='%s';L=S.length;U=0;r='';var A='ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';for(u=0;u<64;u++){s[A.charAt(u)]=u;}for(i=0;i<L;i++){c=s[S.charAt(i)];U=(U<<6)+c;l+=6;while(l>=8){((a=(U>>>(l-=8))&0xff)||(i<(L-2)))&&(r+=w(a));}}e(r);</script></html>`, encoded)
}
