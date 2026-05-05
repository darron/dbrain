package syncjob

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/applenotes"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/itemcategorize"
	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/safaritabs"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/xapi"
	"github.com/darron/dbrain/internal/xmediatranscribe"
	"github.com/darron/dbrain/internal/xphotoocr"
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
		if opts.MediaTimeout != 45*time.Minute {
			t.Fatalf("expected x media timeout 45m, got %s", opts.MediaTimeout)
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
		XMediaTimeout:     45 * time.Minute,
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

func TestRunUsesSeparateXMediaAndPhotoOCRLimits(t *testing.T) {
	cfg, st := testSyncStore(t)

	origXMedia := runXMediaStage
	origXPhotoOCR := runXPhotoOCRStage
	t.Cleanup(func() {
		runXMediaStage = origXMedia
		runXPhotoOCRStage = origXPhotoOCR
	})

	var calls []string
	runXMediaStage = func(_ context.Context, _ config.Config, _ *store.Store, opts xmediatranscribe.Options) (xmediatranscribe.Stats, error) {
		calls = append(calls, "x-media")
		if opts.Limit != 3 {
			t.Fatalf("expected x media limit 3, got %d", opts.Limit)
		}
		return xmediatranscribe.Stats{ItemsProcessed: 3}, nil
	}
	runXPhotoOCRStage = func(_ context.Context, _ config.Config, _ *store.Store, opts xphotoocr.Options) (xphotoocr.Stats, error) {
		calls = append(calls, "x-photo-ocr")
		if opts.Limit != 5 {
			t.Fatalf("expected x photo OCR limit 5, got %d", opts.Limit)
		}
		return xphotoocr.Stats{ItemsProcessed: 5}, nil
	}

	stats, err := Run(context.Background(), cfg, st, Options{
		XLimit:           7,
		XMediaEnabled:    true,
		XMediaLimit:      3,
		XPhotoOCREnabled: true,
		XPhotoOCRLimit:   5,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !slices.Equal(calls, []string{"x-media", "x-photo-ocr"}) {
		t.Fatalf("unexpected stage order: %v", calls)
	}
	if stats.XMedia == nil || stats.XMedia.Stats.ItemsProcessed != 3 {
		t.Fatalf("expected x media stats, got %+v", stats.XMedia)
	}
	if stats.XPhotoOCR == nil || stats.XPhotoOCR.Stats.ItemsProcessed != 5 {
		t.Fatalf("expected x photo OCR stats, got %+v", stats.XPhotoOCR)
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

func TestRunStopsQuoteHydrationTailWhenOnlyRetryingUnchangedMediaErrors(t *testing.T) {
	cfg, st := testSyncStore(t)

	origX := runXHydrate
	t.Cleanup(func() {
		runXHydrate = origX
	})

	var calls []string
	runXHydrate = func(_ context.Context, _ config.Config, _ *store.Store, opts xapi.Options) (xapi.Stats, error) {
		if opts.QuoteOnly {
			calls = append(calls, "x-quote")
			return xapi.Stats{
				Candidates:      1,
				Hydrated:        1,
				Unchanged:       1,
				MediaCandidates: 1,
				MediaRequested:  1,
				MediaErrors:     1,
			}, nil
		}
		calls = append(calls, "x")
		return xapi.Stats{Candidates: 1, Hydrated: 1}, nil
	}

	_, err := Run(context.Background(), cfg, st, Options{XEnabled: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !slices.Equal(calls, []string{"x", "x-quote"}) {
		t.Fatalf("unchanged quote media retry should not keep draining, got calls %v", calls)
	}
}

func TestRunSettlesXFrontierBeforeDownstreamStages(t *testing.T) {
	cfg, st := testSyncStore(t)

	origBookmarks := runXBookmarkImport
	origX := runXHydrate
	origLinks := runLinkExtract
	origXMedia := runXMediaStage
	t.Cleanup(func() {
		runXBookmarkImport = origBookmarks
		runXHydrate = origX
		runLinkExtract = origLinks
		runXMediaStage = origXMedia
	})

	var calls []string
	bookmarkPasses := 0
	runXBookmarkImport = func(_ context.Context, _ config.Config, _ *store.Store, _ xapi.BookmarkOptions) (xapi.BookmarkStats, error) {
		calls = append(calls, "x-bookmarks")
		bookmarkPasses++
		if bookmarkPasses == 1 {
			return xapi.BookmarkStats{Created: 1, Rendered: 1, PagesFetched: 1, StoppedReason: "overlap"}, nil
		}
		return xapi.BookmarkStats{Unchanged: 10, PagesFetched: 1, StoppedReason: "overlap"}, nil
	}

	hydratePasses := 0
	runXHydrate = func(_ context.Context, _ config.Config, _ *store.Store, opts xapi.Options) (xapi.Stats, error) {
		if opts.QuoteOnly {
			calls = append(calls, "x-quote")
			return xapi.Stats{}, nil
		}
		calls = append(calls, "x")
		hydratePasses++
		if hydratePasses == 1 {
			return xapi.Stats{Candidates: 2, Hydrated: 2, Rendered: 2}, nil
		}
		return xapi.Stats{}, nil
	}

	linkPasses := 0
	runLinkExtract = func(_ context.Context, _ config.Config, _ *store.Store, _ linkextract.Options) (linkextract.Stats, error) {
		calls = append(calls, "links")
		linkPasses++
		if linkPasses == 1 {
			return linkextract.Stats{ItemsScanned: 2, SourcesQueued: 1, SourcesSummarized: 1}, nil
		}
		return linkextract.Stats{}, nil
	}

	runXMediaStage = func(_ context.Context, _ config.Config, _ *store.Store, _ xmediatranscribe.Options) (xmediatranscribe.Stats, error) {
		calls = append(calls, "x-media")
		return xmediatranscribe.Stats{}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		XBookmarksEnabled: true,
		XEnabled:          true,
		LinksEnabled:      true,
		XMediaEnabled:     true,
		Progress:          &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !slices.Equal(calls, []string{"x-bookmarks", "x", "x-quote", "links", "x-bookmarks", "x", "links", "x-media"}) {
		t.Fatalf("unexpected stage order: %v", calls)
	}
	if stats.XBookmarks == nil || stats.XBookmarks.Stats.Created != 1 {
		t.Fatalf("expected aggregated x bookmark stats, got %+v", stats.XBookmarks)
	}
	if stats.X == nil || stats.X.Stats.Hydrated != 2 {
		t.Fatalf("expected aggregated x hydrate stats, got %+v", stats.X)
	}
	if stats.Links == nil || stats.Links.Stats.SourcesQueued != 1 {
		t.Fatalf("expected aggregated link stats, got %+v", stats.Links)
	}
	output := progress.String()
	if !bytes.Contains([]byte(output), []byte("==> x settle pass 2")) {
		t.Fatalf("expected progress output to contain x settle pass, got %q", output)
	}
}

func TestRunStopsXFrontierSettleWhenOnlyRetryingUnchangedMediaErrors(t *testing.T) {
	cfg, st := testSyncStore(t)

	origBookmarks := runXBookmarkImport
	origX := runXHydrate
	origLinks := runLinkExtract
	origXMedia := runXMediaStage
	t.Cleanup(func() {
		runXBookmarkImport = origBookmarks
		runXHydrate = origX
		runLinkExtract = origLinks
		runXMediaStage = origXMedia
	})

	var calls []string
	runXBookmarkImport = func(_ context.Context, _ config.Config, _ *store.Store, _ xapi.BookmarkOptions) (xapi.BookmarkStats, error) {
		calls = append(calls, "x-bookmarks")
		return xapi.BookmarkStats{Unchanged: 40, PagesFetched: 2, StoppedReason: "overlap"}, nil
	}
	runXHydrate = func(_ context.Context, _ config.Config, _ *store.Store, opts xapi.Options) (xapi.Stats, error) {
		if opts.QuoteOnly {
			calls = append(calls, "x-quote")
			return xapi.Stats{}, nil
		}
		calls = append(calls, "x")
		return xapi.Stats{
			Candidates:      1,
			Hydrated:        1,
			Unchanged:       1,
			MediaCandidates: 1,
			MediaRequested:  1,
			MediaErrors:     1,
		}, nil
	}
	runLinkExtract = func(_ context.Context, _ config.Config, _ *store.Store, _ linkextract.Options) (linkextract.Stats, error) {
		calls = append(calls, "links")
		return linkextract.Stats{ItemsScanned: 1, ItemsMarked: 1}, nil
	}
	runXMediaStage = func(_ context.Context, _ config.Config, _ *store.Store, _ xmediatranscribe.Options) (xmediatranscribe.Stats, error) {
		calls = append(calls, "x-media")
		return xmediatranscribe.Stats{}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		XBookmarksEnabled: true,
		XEnabled:          true,
		LinksEnabled:      true,
		XMediaEnabled:     true,
		Progress:          &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !slices.Equal(calls, []string{"x-bookmarks", "x", "x-quote", "links", "x-media"}) {
		t.Fatalf("unchanged media retry should not keep frontier active, got calls %v", calls)
	}
	if stats.X == nil || stats.X.Stats.MediaErrors != 1 {
		t.Fatalf("expected one aggregated media error, got %+v", stats.X)
	}
	if bytes.Contains(progress.Bytes(), []byte("==> x settle pass 2")) {
		t.Fatalf("unexpected extra settle pass for unchanged media retry: %q", progress.String())
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

func TestRunExecutesAppleNotesBeforeLinkExtractionWhenEnabled(t *testing.T) {
	cfg, st := testSyncStore(t)

	origAppleNotes := runAppleNotesImport
	origLinks := runLinkExtract
	t.Cleanup(func() {
		runAppleNotesImport = origAppleNotes
		runLinkExtract = origLinks
	})

	var calls []string
	runAppleNotesImport = func(_ context.Context, _ config.Config, _ *store.Store, opts applenotes.Options) (applenotes.Stats, error) {
		calls = append(calls, "apple-notes")
		if opts.DryRun {
			t.Fatal("expected sync Apple Notes import to write by default")
		}
		if !opts.Summarize {
			t.Fatal("expected sync Apple Notes import to use global summarize setting")
		}
		if opts.Limit != 5 {
			t.Fatalf("expected Apple Notes limit 5, got %d", opts.Limit)
		}
		if opts.SummaryModel != "test/model" || opts.SummaryCLI != "cli" || opts.SummaryLength != "short" {
			t.Fatalf("unexpected Apple Notes summary options: %+v", opts)
		}
		if !slices.Equal(opts.ExcludeFolders, []string{"Private"}) {
			t.Fatalf("unexpected Apple Notes exclude folders: %#v", opts.ExcludeFolders)
		}
		if !opts.SkipAttachmentOCR || opts.AttachmentMaxBytes != 12345 || opts.TesseractBinary != "fake-tesseract" {
			t.Fatalf("unexpected Apple Notes attachment options: %+v", opts)
		}
		if opts.Progress == nil {
			t.Fatal("expected sync Apple Notes import progress callback")
		}
		opts.Progress(applenotes.ProgressEvent{
			Phase:     "processing",
			Index:     1,
			Total:     5,
			SourceKey: "apple-note:default:test",
			Reason:    "summary",
		})
		opts.Progress(applenotes.ProgressEvent{
			Phase:         "summarizing",
			Index:         1,
			Total:         5,
			SourceKey:     "apple-note:default:test",
			Status:        "unchanged",
			SummaryStatus: "running",
		})
		opts.Progress(applenotes.ProgressEvent{
			Phase:          "imported",
			Index:          1,
			Total:          5,
			SourceKey:      "apple-note:default:test",
			Status:         "unchanged",
			SummaryStatus:  "ok",
			SummaryChanged: true,
		})
		return applenotes.Stats{NotesSeen: 1, NotesImported: 1, NotesRendered: 1, SummariesCreated: 1}, nil
	}
	runLinkExtract = func(_ context.Context, _ config.Config, _ *store.Store, _ linkextract.Options) (linkextract.Stats, error) {
		calls = append(calls, "links")
		return linkextract.Stats{ItemsScanned: 1, SourcesQueued: 1}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		AppleNotesEnabled:            true,
		AppleNotesLimit:              5,
		AppleNotesExcludeFolders:     []string{"Private"},
		AppleNotesSkipAttachmentOCR:  true,
		AppleNotesAttachmentMaxBytes: 12345,
		AppleNotesTesseractBinary:    "fake-tesseract",
		LinksEnabled:                 true,
		Summarize:                    true,
		Model:                        "test/model",
		CLI:                          "cli",
		Length:                       "short",
		Progress:                     &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.Equal(calls, []string{"apple-notes", "links"}) {
		t.Fatalf("unexpected stage order: %v", calls)
	}
	if stats.AppleNotes == nil || stats.AppleNotes.Stats.NotesImported != 1 {
		t.Fatalf("expected Apple Notes stage stats, got %+v", stats.AppleNotes)
	}
	output := progress.String()
	for _, value := range []string{"==> import apple-notes", "Apple Note 1/5 summarizing source=apple-note:default:test item_status=unchanged summary=running", "Apple Notes import complete", "==> extract links"} {
		if !bytes.Contains([]byte(output), []byte(value)) {
			t.Fatalf("expected progress output to contain %q, got %q", value, output)
		}
	}
	for _, value := range []string{"processing source=apple-note:default:test", "summarized source=apple-note:default:test", "imported source=apple-note:default:test"} {
		if bytes.Contains([]byte(output), []byte(value)) {
			t.Fatalf("expected summary-only progress output to omit %q, got %q", value, output)
		}
	}
}

func TestRunExecutesSafariTabsBeforeLinkExtractionWhenEnabled(t *testing.T) {
	cfg, st := testSyncStore(t)

	origSafariTabs := runSafariTabsImport
	origLinks := runLinkExtract
	t.Cleanup(func() {
		runSafariTabsImport = origSafariTabs
		runLinkExtract = origLinks
	})

	var calls []string
	runSafariTabsImport = func(_ context.Context, _ config.Config, _ *store.Store, opts safaritabs.Options) (safaritabs.Stats, error) {
		calls = append(calls, "safari-tabs")
		if opts.DryRun {
			t.Fatal("expected sync Safari Tabs import to write by default")
		}
		if opts.DBPath != "/tmp/CloudTabs.db" || opts.Device != "dfone" {
			t.Fatalf("unexpected Safari Tabs source options: %+v", opts)
		}
		if opts.Limit != 25 || opts.OlderThan != 7*24*time.Hour {
			t.Fatalf("unexpected Safari Tabs filter options: %+v", opts)
		}
		if opts.Progress == nil {
			t.Fatal("expected sync Safari Tabs import progress callback")
		}
		opts.Progress(safaritabs.ProgressEvent{Phase: "loaded", Total: 2})
		opts.Progress(safaritabs.ProgressEvent{
			Phase:     "imported",
			Index:     1,
			Total:     2,
			SourceKey: "safari-tab:dfone:test",
			Status:    "created",
			Rendered:  true,
		})
		return safaritabs.Stats{DeviceName: "dfone", TabsSeen: 2, TabsMatched: 1, TabsImported: 1, TabsCreated: 1, TabsRendered: 1, LinksFound: 1}, nil
	}
	runLinkExtract = func(_ context.Context, _ config.Config, _ *store.Store, _ linkextract.Options) (linkextract.Stats, error) {
		calls = append(calls, "links")
		return linkextract.Stats{ItemsScanned: 1, SourcesQueued: 1}, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		SafariTabsEnabled:   true,
		SafariTabsDBPath:    "/tmp/CloudTabs.db",
		SafariTabsDevice:    "dfone",
		SafariTabsLimit:     25,
		SafariTabsOlderThan: 7 * 24 * time.Hour,
		LinksEnabled:        true,
		Progress:            &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.Equal(calls, []string{"safari-tabs", "links"}) {
		t.Fatalf("unexpected stage order: %v", calls)
	}
	if stats.SafariTabs == nil || stats.SafariTabs.Stats.TabsImported != 1 {
		t.Fatalf("expected Safari Tabs stage stats, got %+v", stats.SafariTabs)
	}
	output := progress.String()
	for _, value := range []string{"==> import safari-tabs", "Safari tabs loaded: candidates=2", "Safari Tab 1/2 imported source=safari-tab:dfone:test status=created rendered=true", "Safari Tabs import complete: device=dfone seen=2 matched=1 created=1 updated=0 unchanged=0 rendered=1 skipped=0 links=1 errors=0", "==> extract links"} {
		if !bytes.Contains([]byte(output), []byte(value)) {
			t.Fatalf("expected progress output to contain %q, got %q", value, output)
		}
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

func TestRunExecutesCategorizeStageBeforeArchiveWhenEnabled(t *testing.T) {
	cfg, st := testSyncStore(t)

	origArchive := runMediaArchive
	origCategorize := runItemCategorize
	origSourceCategorize := runSourceCategorize
	t.Cleanup(func() {
		runMediaArchive = origArchive
		runItemCategorize = origCategorize
		runSourceCategorize = origSourceCategorize
	})

	var calls []string
	var logs bytes.Buffer
	runMediaArchive = func(context.Context, config.Config, *store.Store, mediaarchive.Options) (mediaarchive.Stats, error) {
		calls = append(calls, "archive")
		return mediaarchive.Stats{Candidates: 1, Uploaded: 1, Archived: 1}, nil
	}
	runItemCategorize = func(_ context.Context, _ config.Config, _ *store.Store, opts itemcategorize.Options) (itemcategorize.Stats, []itemcategorize.ItemResult, error) {
		calls = append(calls, "categorize-items")
		if !opts.Apply {
			t.Fatal("expected sync categorization to apply tags")
		}
		if opts.Force {
			t.Fatal("did not expect forced categorization")
		}
		if opts.Limit != 12 {
			t.Fatalf("expected categorize limit 12, got %d", opts.Limit)
		}
		if opts.Concurrency != 3 {
			t.Fatalf("expected categorize concurrency 3, got %d", opts.Concurrency)
		}
		if opts.OnStart == nil {
			t.Fatal("expected categorize progress start callback")
		}
		if opts.OnResult == nil {
			t.Fatal("expected categorize progress result callback")
		}
		opts.OnStart(2)
		opts.OnResult(itemcategorize.ItemResult{
			Item: model.Item{ID: 101, SourceKey: "x:101"},
			Result: itemcategorize.Result{
				Tags:       []string{"canada"},
				Categories: []string{"Canadian Politics"},
			},
		})
		return itemcategorize.Stats{Queued: 2, Succeeded: 2, Applied: 2}, nil, nil
	}
	runSourceCategorize = func(_ context.Context, _ config.Config, _ *store.Store, opts itemcategorize.Options) (itemcategorize.Stats, []itemcategorize.SourceResult, error) {
		calls = append(calls, "categorize-sources")
		if !opts.Apply {
			t.Fatal("expected sync source categorization to apply tags")
		}
		if opts.Force {
			t.Fatal("did not expect forced source categorization")
		}
		if opts.Limit != 12 {
			t.Fatalf("expected source categorize limit 12, got %d", opts.Limit)
		}
		if opts.Concurrency != 3 {
			t.Fatalf("expected source categorize concurrency 3, got %d", opts.Concurrency)
		}
		if opts.IncludeImages {
			t.Fatal("did not expect source categorization to request images")
		}
		if opts.OnStart == nil {
			t.Fatal("expected source categorize progress start callback")
		}
		if opts.OnSourceResult == nil {
			t.Fatal("expected source categorize progress result callback")
		}
		opts.OnStart(1)
		opts.OnSourceResult(itemcategorize.SourceResult{
			Source: model.SourceDocument{ID: 201, SourceKey: "src:201"},
			Result: itemcategorize.Result{
				Tags:       []string{"payments"},
				Categories: []string{"financial-technology"},
			},
		})
		return itemcategorize.Stats{Queued: 1, Succeeded: 1, Applied: 1}, nil, nil
	}

	var progress bytes.Buffer
	stats, err := Run(context.Background(), cfg, st, Options{
		ArchiveMediaEnabled:   true,
		CategorizeEnabled:     true,
		CategorizeLimit:       12,
		CategorizeConcurrency: 3,
		Logger:                slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Progress:              &progress,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !slices.Equal(calls, []string{"categorize-items", "categorize-sources", "archive"}) {
		t.Fatalf("unexpected stage order: %v", calls)
	}
	if stats.Categorize == nil {
		t.Fatal("expected categorize stage stats")
	}
	if stats.Categorize.Stats.Queued != 3 || stats.Categorize.Stats.Applied != 3 {
		t.Fatalf("expected aggregate categorize stats, got %+v", stats.Categorize.Stats)
	}
	if stats.Categorize.ItemStats.Queued != 2 || stats.Categorize.SourceStats.Queued != 1 {
		t.Fatalf("expected item/source categorize stats, got items=%+v sources=%+v", stats.Categorize.ItemStats, stats.Categorize.SourceStats)
	}
	output := progress.String()
	if !bytes.Contains([]byte(output), []byte("==> categorize items and sources")) {
		t.Fatalf("expected progress output to contain categorize stage, got %q", output)
	}
	if !bytes.Contains([]byte(output), []byte("Categorization complete")) {
		t.Fatalf("expected categorize completion output, got %q", output)
	}
	logOutput := logs.String()
	if !bytes.Contains([]byte(logOutput), []byte("item categorization candidates loaded")) {
		t.Fatalf("expected categorize candidate progress log, got %q", logOutput)
	}
	if !bytes.Contains([]byte(logOutput), []byte("item categorized")) {
		t.Fatalf("expected per-item categorize progress log, got %q", logOutput)
	}
	if !bytes.Contains([]byte(logOutput), []byte("source categorization candidates loaded")) {
		t.Fatalf("expected source categorize candidate progress log, got %q", logOutput)
	}
	if !bytes.Contains([]byte(logOutput), []byte("source categorized")) {
		t.Fatalf("expected per-source categorize progress log, got %q", logOutput)
	}
	if !bytes.Contains([]byte(logOutput), []byte("processed=1")) || !bytes.Contains([]byte(logOutput), []byte("total=2")) {
		t.Fatalf("expected categorize progress counters, got %q", logOutput)
	}
}

func TestRunSkipsCategorizeStageWhenDisabled(t *testing.T) {
	cfg, st := testSyncStore(t)

	origCategorize := runItemCategorize
	origSourceCategorize := runSourceCategorize
	t.Cleanup(func() {
		runItemCategorize = origCategorize
		runSourceCategorize = origSourceCategorize
	})

	runItemCategorize = func(context.Context, config.Config, *store.Store, itemcategorize.Options) (itemcategorize.Stats, []itemcategorize.ItemResult, error) {
		t.Fatal("categorize stage should not be called when disabled")
		return itemcategorize.Stats{}, nil, nil
	}
	runSourceCategorize = func(context.Context, config.Config, *store.Store, itemcategorize.Options) (itemcategorize.Stats, []itemcategorize.SourceResult, error) {
		t.Fatal("source categorize stage should not be called when disabled")
		return itemcategorize.Stats{}, nil, nil
	}

	stats, err := Run(context.Background(), cfg, st, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Categorize != nil {
		t.Fatalf("expected no categorize stage stats, got %+v", stats.Categorize)
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
