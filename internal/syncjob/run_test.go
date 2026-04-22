package syncjob

import (
	"context"
	"reflect"
	"testing"
	"time"

	"dbrain/internal/config"
	"dbrain/internal/ftimport"
	"dbrain/internal/githubimport"
	"dbrain/internal/linkextract"
	"dbrain/internal/store"
	"dbrain/internal/worker"
	"dbrain/internal/xapi"
	"dbrain/internal/youtubeimport"
)

func TestRunStagesInOrder(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	originalFT := runFTImport
	originalX := runXHydrate
	originalLinks := runLinkExtract
	originalGitHub := runGitHubImport
	originalYouTube := runYouTubeImport
	originalSources := runSourceWorker
	originalVersion := summarizeVersion
	defer func() {
		runFTImport = originalFT
		runXHydrate = originalX
		runLinkExtract = originalLinks
		runGitHubImport = originalGitHub
		runYouTubeImport = originalYouTube
		runSourceWorker = originalSources
		summarizeVersion = originalVersion
	}()

	var order []string
	runFTImport = func(ctx context.Context, cfg config.Config, st *store.Store, opts ftimport.Options) (ftimport.Stats, error) {
		order = append(order, "ft")
		return ftimport.Stats{Created: 2}, nil
	}
	runXHydrate = func(ctx context.Context, cfg config.Config, st *store.Store, opts xapi.Options) (xapi.Stats, error) {
		order = append(order, "x")
		return xapi.Stats{Hydrated: 3}, nil
	}
	runLinkExtract = func(ctx context.Context, cfg config.Config, st *store.Store, opts linkextract.Options) (linkextract.Stats, error) {
		order = append(order, "links")
		return linkextract.Stats{SourcesSummarized: 4}, nil
	}
	runGitHubImport = func(ctx context.Context, cfg config.Config, st *store.Store, opts githubimport.Options) (githubimport.Stats, error) {
		order = append(order, "github")
		return githubimport.Stats{StarsProcessed: 5}, nil
	}
	runYouTubeImport = func(ctx context.Context, cfg config.Config, st *store.Store, opts youtubeimport.Options) (youtubeimport.Stats, error) {
		order = append(order, "youtube")
		return youtubeimport.Stats{ItemsProcessed: 6}, nil
	}
	runSourceWorker = func(ctx context.Context, backlogFn worker.SourceBacklogFunc, runFn worker.SourceRunFunc, opts worker.SourceOptions) (worker.SourceStats, error) {
		order = append(order, "sources")
		return worker.SourceStats{WorkCycles: 1, StoppedReason: "queue_drained"}, nil
	}
	summarizeVersion = func(ctx context.Context, binary string) string {
		return "test"
	}

	stats, err := Run(context.Background(), cfg, &store.Store{}, Options{
		FTEnabled:         true,
		XEnabled:          true,
		LinksEnabled:      true,
		GitHubEnabled:     true,
		YouTubeEnabled:    true,
		SourcesEnabled:    true,
		WatchLater:        true,
		Liked:             true,
		Browser:           "chrome",
		Length:            "short",
		Timeout:           time.Second,
		XTimeout:          time.Second,
		LinkDiscoverLimit: 10,
		LinkLimit:         10,
		LinkConcurrency:   2,
		SourceLimit:       10,
		SourceConcurrency: 2,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantOrder := []string{"ft", "x", "links", "github", "youtube", "sources"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("unexpected order: got %v want %v", order, wantOrder)
	}
	if stats.FT == nil || stats.FT.Stats.Created != 2 {
		t.Fatalf("expected ft stats to be recorded, got %+v", stats.FT)
	}
	if stats.X == nil || stats.X.Stats.Hydrated != 3 {
		t.Fatalf("expected x stats to be recorded, got %+v", stats.X)
	}
	if stats.Links == nil || stats.Links.Stats.SourcesSummarized != 4 {
		t.Fatalf("expected link stats to be recorded, got %+v", stats.Links)
	}
	if stats.GitHub == nil || stats.GitHub.Stats.StarsProcessed != 5 {
		t.Fatalf("expected github stats to be recorded, got %+v", stats.GitHub)
	}
	if stats.YouTube == nil || stats.YouTube.Stats.ItemsProcessed != 6 {
		t.Fatalf("expected youtube stats to be recorded, got %+v", stats.YouTube)
	}
	if stats.Sources == nil || stats.Sources.Stats.WorkCycles != 1 {
		t.Fatalf("expected source worker stats to be recorded, got %+v", stats.Sources)
	}
}
