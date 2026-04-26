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
		if opts.Limit != 7 {
			t.Fatalf("expected x limit 7, got %d", opts.Limit)
		}
		if opts.QuoteOnly {
			calls = append(calls, "x-quote")
			return xapi.Stats{}, nil
		}
		calls = append(calls, "x")
		return xapi.Stats{Candidates: 7, Hydrated: 7, Rendered: 7}, nil
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

	if !slices.Equal(calls, []string{"x-bookmarks", "x", "x-quote", "x-media"}) {
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

func TestRunDrainsQuoteHydrationTailBeforeXMediaStage(t *testing.T) {
	cfg, st := testSyncStore(t)

	origX := runXHydrate
	origXMedia := runXMediaStage
	t.Cleanup(func() {
		runXHydrate = origX
		runXMediaStage = origXMedia
	})

	var calls []string
	runXHydrate = func(_ context.Context, _ config.Config, _ *store.Store, opts xapi.Options) (xapi.Stats, error) {
		if opts.QuoteOnly {
			calls = append(calls, "x-quote")
			if len(calls) == 2 {
				return xapi.Stats{Candidates: 2, Hydrated: 2, Requested: 1, Rendered: 1}, nil
			}
			return xapi.Stats{}, nil
		}
		calls = append(calls, "x")
		return xapi.Stats{Candidates: 4, Hydrated: 4, Requested: 2, Rendered: 3}, nil
	}
	runXMediaStage = func(_ context.Context, _ config.Config, _ *store.Store, _ xmediatranscribe.Options) (xmediatranscribe.Stats, error) {
		calls = append(calls, "x-media")
		return xmediatranscribe.Stats{}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		XEnabled:      true,
		XMediaEnabled: true,
		Progress:      &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !slices.Equal(calls, []string{"x", "x-quote", "x-quote", "x-media"}) {
		t.Fatalf("unexpected stage order: %v", calls)
	}
	if stats.X == nil {
		t.Fatal("expected x stage stats")
	}
	if stats.X.Stats.Hydrated != 6 {
		t.Fatalf("expected aggregated hydrated count 6, got %d", stats.X.Stats.Hydrated)
	}
	if stats.X.Stats.Requested != 3 {
		t.Fatalf("expected aggregated requested count 3, got %d", stats.X.Stats.Requested)
	}
	output := progress.String()
	if !bytes.Contains([]byte(output), []byte("X quote hydration pass 1 complete")) {
		t.Fatalf("expected progress output to contain quote drain pass, got %q", output)
	}
}

func TestRunSkipsQuoteDrainWhenForceEnabled(t *testing.T) {
	cfg, st := testSyncStore(t)

	origX := runXHydrate
	t.Cleanup(func() {
		runXHydrate = origX
	})

	var calls []string
	runXHydrate = func(_ context.Context, _ config.Config, _ *store.Store, opts xapi.Options) (xapi.Stats, error) {
		if opts.QuoteOnly {
			t.Fatal("quote-only drain should not run during force sync")
		}
		calls = append(calls, "x")
		return xapi.Stats{Candidates: 4, Hydrated: 4}, nil
	}

	_, err := Run(context.Background(), cfg, st, Options{
		XEnabled: true,
		Force:    true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.Equal(calls, []string{"x"}) {
		t.Fatalf("unexpected hydrate calls: %v", calls)
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
