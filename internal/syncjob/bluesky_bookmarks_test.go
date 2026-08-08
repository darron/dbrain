package syncjob

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/bskyapi"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

func TestExecuteBlueskyBookmarksStagePassesBrowserProfileAndLimits(t *testing.T) {
	cfg, st := testSyncStore(t)
	orig := runBlueskyBookmarkImport
	t.Cleanup(func() { runBlueskyBookmarkImport = orig })

	var progress bytes.Buffer
	var got bskyapi.BookmarkOptions
	runBlueskyBookmarkImport = func(_ context.Context, _ config.Config, _ *store.Store, opts bskyapi.BookmarkOptions) (bskyapi.BookmarkStats, error) {
		got = opts
		return bskyapi.BookmarkStats{PagesFetched: 2, Seen: 3, Processed: 2, Skipped: 1, SkippedBlocked: 1, Created: 2, Rendered: 2, StoppedReason: "end of bookmarks"}, nil
	}

	stage, err := executeBlueskyBookmarksStage(context.Background(), cfg, st, stageOptions{
		Common: commonStageOptions{
			Browser:  "chrome",
			Profile:  "Profile 7",
			Progress: &progress,
		},
		BlueskyBookmarks: blueskyBookmarksStageOptions{
			Limit:   25,
			Timeout: 17 * time.Second,
		},
	})
	if err != nil {
		t.Fatalf("executeBlueskyBookmarksStage: %v", err)
	}
	if got.Browser != "chrome" || got.Profile != "Profile 7" || got.Limit != 25 || got.Timeout != 17*time.Second {
		t.Fatalf("unexpected bookmark options: %+v", got)
	}
	if stage == nil || stage.Stats.Created != 2 {
		t.Fatalf("unexpected stage stats: %+v", stage)
	}
	for _, want := range []string{"==> import bluesky-bookmarks", "Bluesky bookmarks import complete: pages=2 seen=3 processed=2 skipped=1 blocked=1 not_found=0 unsupported=0 malformed=0 created=2"} {
		if !bytes.Contains(progress.Bytes(), []byte(want)) {
			t.Fatalf("progress missing %q: %q", want, progress.String())
		}
	}
}

func TestNewStageOptionsUsesDedicatedBlueskyTimeout(t *testing.T) {
	opts := newStageOptions(Options{
		Timeout:                 5 * time.Minute,
		BlueskyBookmarksTimeout: 17 * time.Second,
	})
	if got, want := opts.BlueskyBookmarks.Timeout, 17*time.Second; got != want {
		t.Fatalf("Bluesky timeout = %s, want %s", got, want)
	}
	if got, want := opts.Common.Timeout, 5*time.Minute; got != want {
		t.Fatalf("common timeout = %s, want %s", got, want)
	}
}
