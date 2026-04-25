package syncjob

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"dbrain/internal/config"
	"dbrain/internal/mediaarchive"
	"dbrain/internal/store"
	"dbrain/internal/xapi"
	"dbrain/internal/xmediatranscribe"
)

func TestRunExecutesXMediaStageAfterXHydration(t *testing.T) {
	cfg, st := testSyncStore(t)

	origBookmarks := runXBookmarkImport
	origX := runXHydrate
	origXMedia := runXMediaStage
	t.Cleanup(func() {
		runXBookmarkImport = origBookmarks
		runXHydrate = origX
		runXMediaStage = origXMedia
	})

	var calls []string
	runXBookmarkImport = func(_ context.Context, _ config.Config, _ *store.Store, opts xapi.BookmarkOptions) (xapi.BookmarkStats, error) {
		calls = append(calls, "x-bookmarks")
		if opts.Limit != 9 {
			t.Fatalf("expected x bookmark limit 9, got %d", opts.Limit)
		}
		return xapi.BookmarkStats{Created: 2, PagesFetched: 1, StoppedReason: "end of bookmarks"}, nil
	}
	runXHydrate = func(_ context.Context, _ config.Config, _ *store.Store, opts xapi.Options) (xapi.Stats, error) {
		calls = append(calls, "x")
		if opts.Limit != 7 {
			t.Fatalf("expected x limit 7, got %d", opts.Limit)
		}
		return xapi.Stats{Hydrated: 7, Rendered: 7}, nil
	}
	runXMediaStage = func(_ context.Context, _ config.Config, _ *store.Store, opts xmediatranscribe.Options) (xmediatranscribe.Stats, error) {
		calls = append(calls, "x-media")
		if opts.Limit != 7 {
			t.Fatalf("expected x media limit 7, got %d", opts.Limit)
		}
		return xmediatranscribe.Stats{ItemsProcessed: 3, ItemsUpdated: 2, MediaTranscribed: 2}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		XBookmarksEnabled: true,
		XBookmarksLimit:   9,
		XEnabled:          true,
		XLimit:            7,
		XMediaEnabled:     true,
		Progress:          &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !slices.Equal(calls, []string{"x-bookmarks", "x", "x-media"}) {
		t.Fatalf("unexpected stage order: %v", calls)
	}
	if stats.XBookmarks == nil {
		t.Fatal("expected x bookmark stage stats")
	}
	if stats.X == nil {
		t.Fatal("expected x stage stats")
	}
	if stats.XMedia == nil {
		t.Fatal("expected x media stage stats")
	}
	output := progress.String()
	if !bytes.Contains([]byte(output), []byte("==> transcribe x-media")) {
		t.Fatalf("expected progress output to contain x media stage, got %q", output)
	}
	if !bytes.Contains([]byte(output), []byte("X media transcription complete")) {
		t.Fatalf("expected completion output to contain x media summary, got %q", output)
	}
}

func TestRunSkipsXMediaStageWhenDisabled(t *testing.T) {
	cfg, st := testSyncStore(t)

	origXMedia := runXMediaStage
	t.Cleanup(func() {
		runXMediaStage = origXMedia
	})

	runXMediaStage = func(context.Context, config.Config, *store.Store, xmediatranscribe.Options) (xmediatranscribe.Stats, error) {
		t.Fatal("x media stage should not be called when disabled")
		return xmediatranscribe.Stats{}, nil
	}

	stats, err := Run(context.Background(), cfg, st, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.XMedia != nil {
		t.Fatalf("expected no x media stage stats, got %+v", stats.XMedia)
	}
}

func TestRunExecutesArchiveStageAtEndWhenEnabled(t *testing.T) {
	cfg, st := testSyncStore(t)

	origArchive := runMediaArchive
	t.Cleanup(func() {
		runMediaArchive = origArchive
	})

	var called bool
	runMediaArchive = func(_ context.Context, _ config.Config, _ *store.Store, opts mediaarchive.Options) (mediaarchive.Stats, error) {
		called = true
		if !opts.Upload || !opts.PruneLocal {
			t.Fatalf("expected archive stage to upload and prune, got %+v", opts)
		}
		if opts.Bucket != "dbrain" {
			t.Fatalf("expected bucket dbrain, got %q", opts.Bucket)
		}
		return mediaarchive.Stats{Candidates: 2, Uploaded: 2, Archived: 2, LocalFilesPruned: 2}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		ArchiveMediaEnabled: true,
		ArchiveBucket:       "dbrain",
		ArchiveEndpoint:     "https://example.invalid",
		ArchiveAccessKeyID:  "key",
		ArchiveSecretKey:    "secret",
		Progress:            &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !called {
		t.Fatal("expected archive stage to run")
	}
	if stats.MediaArchive == nil {
		t.Fatal("expected media archive stats")
	}
	output := progress.String()
	if !bytes.Contains([]byte(output), []byte("==> archive media")) {
		t.Fatalf("expected progress output to contain archive stage, got %q", output)
	}
	if !bytes.Contains([]byte(output), []byte("Media archive complete")) {
		t.Fatalf("expected archive completion output, got %q", output)
	}
}

func testSyncStore(t *testing.T) (config.Config, *store.Store) {
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
