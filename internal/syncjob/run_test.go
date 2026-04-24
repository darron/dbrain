package syncjob

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"dbrain/internal/config"
	"dbrain/internal/store"
	"dbrain/internal/xapi"
	"dbrain/internal/xmediatranscribe"
)

func TestRunExecutesXMediaStageAfterXHydration(t *testing.T) {
	cfg, st := testSyncStore(t)

	origX := runXHydrate
	origXMedia := runXMediaStage
	t.Cleanup(func() {
		runXHydrate = origX
		runXMediaStage = origXMedia
	})

	var calls []string
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
		XEnabled:      true,
		XLimit:        7,
		XMediaEnabled: true,
		Progress:      &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !slices.Equal(calls, []string{"x", "x-media"}) {
		t.Fatalf("unexpected stage order: %v", calls)
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
