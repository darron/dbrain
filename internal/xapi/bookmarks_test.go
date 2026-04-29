package xapi

import (
	"context"
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

func TestParseBookmarksResponse(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "bookmark_feed_note_tweet.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	page := parseBookmarksResponse(payload, mustParseRFC3339(t, "2026-04-25T16:00:00Z"))
	if got, want := len(page.Records), 1; got != want {
		t.Fatalf("record count = %d, want %d", got, want)
	}
	if got, want := page.NextCursor, "DAACCgACEgUT6sAAKgAA"; got != want {
		t.Fatalf("next cursor = %q, want %q", got, want)
	}

	record := page.Records[0]
	if got, want := record.TweetID, "2039805659525644595"; got != want {
		t.Fatalf("tweet id = %q, want %q", got, want)
	}
	if got, want := record.AuthorHandle, "karpathy"; got != want {
		t.Fatalf("author handle = %q, want %q", got, want)
	}
	if !strings.HasPrefix(record.Text, "LLM Knowledge Bases") {
		t.Fatalf("expected note_tweet text, got %q", record.Text)
	}
	if !strings.Contains(record.Text, "example.com/note") {
		t.Fatalf("expected note_tweet display URL replacement, got %q", record.Text)
	}
	if got, want := record.Links, []string{"https://example.com/note"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("links = %#v, want %#v", got, want)
	}
	if got, want := record.SortIndex, "1825000000000000000"; got != want {
		t.Fatalf("sort index = %q, want %q", got, want)
	}
	if got, want := record.BookmarkCount, 101751; got != want {
		t.Fatalf("bookmark count = %d, want %d", got, want)
	}
}

func TestRunBookmarksImportsAndStopsOnOverlap(t *testing.T) {
	cfg, st := testBookmarkStore(t)

	fixture, err := os.ReadFile(filepath.Join("testdata", "bookmark_feed_note_tweet.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	emptyPage := []byte(`{"data":{"bookmark_timeline_v2":{"timeline":{"instructions":[{"type":"TimelineAddEntries","entries":[]}]}}}}`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("variables") == "" {
			t.Fatalf("expected variables query param")
		}
		if strings.Contains(r.URL.RawQuery, "cursor") {
			_, _ = w.Write(emptyPage)
			return
		}
		_, _ = w.Write(fixture)
	}))
	defer server.Close()

	origBaseURL := bookmarkGraphQLBaseURL
	bookmarkGraphQLBaseURL = server.URL
	t.Cleanup(func() {
		bookmarkGraphQLBaseURL = origBaseURL
	})

	first, err := RunBookmarks(context.Background(), cfg, st, BookmarkOptions{
		CT0:            "test-ct0",
		AuthToken:      "test-auth",
		PageSize:       20,
		StalePageLimit: 1,
		Timeout:        2 * time.Second,
	})
	if err != nil {
		t.Fatalf("first RunBookmarks: %v", err)
	}
	if got, want := first.Created, 1; got != want {
		t.Fatalf("created = %d, want %d", got, want)
	}
	if got, want := first.StoppedReason, "end of bookmarks"; got != want {
		t.Fatalf("stop reason = %q, want %q", got, want)
	}

	item, err := st.GetItem(context.Background(), "x:2039805659525644595")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.SourceType != "x_bookmark" {
		t.Fatalf("source type = %q", item.SourceType)
	}
	if !strings.Contains(item.RawJSON, `"sort_index":"1825000000000000000"`) {
		t.Fatalf("expected sort_index in raw json, got %q", item.RawJSON)
	}
	if !strings.Contains(item.LinksJSON, "https://example.com/note") {
		t.Fatalf("expected note_tweet link in links_json, got %q", item.LinksJSON)
	}

	second, err := RunBookmarks(context.Background(), cfg, st, BookmarkOptions{
		CT0:            "test-ct0",
		AuthToken:      "test-auth",
		PageSize:       20,
		StalePageLimit: 1,
		Timeout:        2 * time.Second,
	})
	if err != nil {
		t.Fatalf("second RunBookmarks: %v", err)
	}
	if got, want := second.Created, 0; got != want {
		t.Fatalf("second created = %d, want %d", got, want)
	}
	if got, want := second.Unchanged, 1; got != want {
		t.Fatalf("second unchanged = %d, want %d", got, want)
	}
	if got, want := second.StoppedReason, "overlap with existing bookmarks"; got != want {
		t.Fatalf("second stop reason = %q, want %q", got, want)
	}
}

func testBookmarkStore(t *testing.T) (config.Config, *store.Store) {
	t.Helper()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return cfg, st
}

func mustParseRFC3339(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}
