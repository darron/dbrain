package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/darron/dbrain/internal/applenotes"
	"github.com/darron/dbrain/internal/bskyapi"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/itemcategorize"
	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/mastodonapi"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/safaritabs"
	"github.com/darron/dbrain/internal/semanticgc"
	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
	"github.com/darron/dbrain/internal/worker"
	"github.com/darron/dbrain/internal/xapi"
	"github.com/darron/dbrain/internal/xmediatranscribe"
	"github.com/darron/dbrain/internal/xphotoocr"
)

// This catches an unbounded table: removing its explicit width lets the long
// production-shaped counters expand past narrow terminals.
func TestWriteSyncStatsWithSemanticBoundsProductionSummaryAtTerminalWidths(t *testing.T) {
	stats, semantic, gc := widthBoundedSyncSummaryFixture()
	requiredStages := []string{
		"Apple Notes", "Safari Tabs", "Bluesky Bookmarks", "Mastodon Bookmarks",
		"X Hydration", "Social Media Transcription", "Social Media Photo OCR", "Links", "Sources", "Categorize",
		"Media Archive", "Semantic Embedding", "Semantic Readiness", "Semantic GC",
	}
	requiredTokens := []string{
		"imported=8", "device=This is",
		"media_discovered=9", "media_linked=8", "media_unavailable=7", "media_downloaded=6",
		"media_gone=5", "media_errors=4", "media_blocked=3", "quote_linked=2", "quote_skipped=1",
		"media_candidates=12", "media_with_audio=7", "summary_cands=9", "photo_candidates=12",
		"hosted_attempts=7", "hosted_fallbacks=2", "links_found=14", "work_cycles=3", "idle_polls=2",
		"prune_skipped=2", "pruned_rows=5", "item_applied=6", "source_applied=5",
		"status=error", "generations=4", "segments=2", "members=19", "error=filesystem_",
	}

	for _, width := range []int{80, 100, 120, 160} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			got := stripSyncSummaryANSI(renderSyncStatsWithSemantic(width, stats, semantic, nil, gc))
			secondGot := stripSyncSummaryANSI(renderSyncStatsWithSemantic(width, stats, semantic, nil, gc))
			if got != secondGot {
				t.Fatalf("width %d output was nondeterministic:\nfirst:\n%s\nsecond:\n%s", width, got, secondGot)
			}
			for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
				if lineWidth := lipgloss.Width(line); lineWidth > width {
					t.Fatalf("width %d line has display width %d:\n%s", width, lineWidth, line)
				}
			}
			stages := normalizeSyncSummaryStages(got)
			for _, want := range requiredStages {
				if !strings.Contains(stages, want) {
					t.Fatalf("width %d output omitted stage %q:\n%s", width, want, got)
				}
			}
			normalized := normalizeSyncSummaryTokens(got)
			for _, want := range requiredTokens {
				if !strings.Contains(normalized, want) {
					t.Fatalf("width %d output omitted %q:\n%s", width, want, got)
				}
			}
		})
	}
}

func TestSyncAllCommandUsesWidthAwareHumanSummary(t *testing.T) {
	stats, semantic, gc := widthBoundedSyncSummaryFixture()
	originalRunSyncAll := runSyncAll
	t.Cleanup(func() { runSyncAll = originalRunSyncAll })
	runSyncAll = func(context.Context, config.Config, *store.Store, syncjob.Options) (syncjob.Stats, error) {
		return stats, nil
	}

	for _, width := range []int{80, 160} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("sync_all:\n  semantic_gc: true\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "categories.yaml"), []byte("aliases: {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("COLUMNS", strconv.Itoa(width))

			deps := successfulSyncSemanticDeps(func(
				context.Context,
				semanticrefresh.RunLedger,
				semanticrefresh.StageExecutor,
				semanticrefresh.Request,
			) (semanticrefresh.Result, error) {
				return semantic, nil
			})
			deps.semanticGC = syncSemanticGCDeps{
				now: time.Now,
				run: func(context.Context, config.Config, semanticgc.Options) (semanticgc.Result, syncSemanticGCFailurePhase, error) {
					return semanticgc.Result{
						Applied: true,
						Catalog: store.RetrievalSemanticGCPlan{
							PrunableGenerations: make([]store.RetrievalSemanticGCArtifact, gc.GenerationsPruned),
							PrunableSegments:    make([]store.RetrievalSemanticGCArtifact, gc.SegmentsPruned),
							PrunableMemberRows:  gc.MemberRowsPruned,
						},
						FilesystemArtifacts:    make([]semanticgc.Artifact, gc.FilesystemDeleted),
						DeletedFilesystemDirs:  gc.FilesystemDeleted,
						DeletedFilesystemBytes: gc.DeletedBytes,
					}, syncSemanticGCPhaseFilesystemUnlink, errors.New("injected unlink failure")
				},
			}

			cmd := newSyncSemanticTestCommand(t, &rootOptions{root: root}, deps)
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SetArgs(syncSemanticTestArgs(false))
			if err := cmd.ExecuteContext(t.Context()); err != nil {
				t.Fatalf("sync all: %v", err)
			}

			got := stripSyncSummaryANSI(stdout.String())
			for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
				if lineWidth := lipgloss.Width(line); lineWidth > width {
					t.Fatalf("width %d command output line has display width %d:\n%s", width, lineWidth, line)
				}
			}
			if !strings.Contains(got, "Sync Summary") {
				t.Fatalf("width %d command output omitted sync header:\n%s", width, got)
			}
			for _, want := range []string{"Bluesky Bookmarks", "Mastodon Bookmarks", "Social Media Transcription", "Social Media Photo OCR", "Semantic GC"} {
				if !strings.Contains(normalizeSyncSummaryStages(got)+" "+normalizeSyncSummaryTokens(got), want) {
					t.Fatalf("width %d command output omitted %q:\n%s", width, want, got)
				}
			}
			if !strings.Contains(normalizeSyncSummaryTokens(got), "status=error") {
				t.Fatalf("width %d command output omitted semantic GC error status:\n%s", width, got)
			}
		})
	}
}

func TestSyncSummaryWidthChangeLeavesJSONStructureUnchanged(t *testing.T) {
	stats, semantic, gc := widthBoundedSyncSummaryFixture()
	var output bytes.Buffer
	if err := writeSyncSemanticResultJSON(&output, stats, semantic, gc); err != nil {
		t.Fatal(err)
	}

	var document map[string]json.RawMessage
	if err := json.Unmarshal(output.Bytes(), &document); err != nil {
		t.Fatalf("decode sync JSON: %v\n%s", err, output.String())
	}
	encodedStats, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	var sourceFields map[string]json.RawMessage
	if err := json.Unmarshal(encodedStats, &sourceFields); err != nil {
		t.Fatal(err)
	}
	for key, want := range sourceFields {
		got, ok := document[key]
		if !ok {
			t.Fatalf("JSON omitted sync field %q", key)
		}
		var gotValue, wantValue any
		if err := json.Unmarshal(got, &gotValue); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(want, &wantValue); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotValue, wantValue) {
			t.Fatalf("JSON field %q changed: got %#v want %#v", key, gotValue, wantValue)
		}
	}
	if _, ok := document["semantic"]; !ok {
		t.Fatal("JSON omitted semantic result")
	}
	if _, ok := document["semantic_gc"]; !ok {
		t.Fatal("JSON omitted semantic GC result")
	}
	if strings.Contains(output.String(), "Sync Summary") {
		t.Fatalf("JSON output included human summary: %s", output.String())
	}
}

func stripSyncSummaryANSI(value string) string { return ansi.Strip(value) }

func normalizeSyncSummaryTokens(value string) string {
	var columns []string
	for _, line := range strings.Split(value, "\n") {
		if !strings.Contains(line, "│") {
			continue
		}
		cells := strings.Split(line, "│")
		if len(cells) < 3 {
			continue
		}
		if len(columns) < len(cells)-2 {
			columns = append(columns, make([]string, len(cells)-2-len(columns))...)
		}
		for column := 1; column < len(cells)-1; column++ {
			columns[column-1] += " " + strings.TrimSpace(cells[column])
		}
	}
	value = strings.Join(columns, " ")
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ReplaceAll(value, " =", "=")
	return strings.ReplaceAll(value, "= ", "=")
}

func normalizeSyncSummaryStages(value string) string {
	var stages []string
	for _, line := range strings.Split(value, "\n") {
		if !strings.HasPrefix(line, "│") {
			continue
		}
		cells := strings.Split(line, "│")
		if len(cells) < 2 {
			continue
		}
		if stage := strings.TrimSpace(cells[1]); stage != "" {
			stages = append(stages, stage)
		}
	}
	return strings.Join(stages, " ")
}

func widthBoundedSyncSummaryFixture() (syncjob.Stats, semanticrefresh.Result, *syncSemanticGCResult) {
	started := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	stats := syncjob.Stats{
		StartedAt: started, CompletedAt: started.Add(3 * time.Minute), Duration: 3 * time.Minute,
		AppleNotes:        &syncjob.AppleNotesStage{Duration: time.Second, Stats: applenotes.Stats{NotesImported: 8, NotesRendered: 6, NotesSkipped: 2, NotesBlocked: 1, AttachmentsIndexed: 4, AttachmentsExtracted: 3, SummariesCreated: 5, Errors: 1}},
		SafariTabs:        &syncjob.SafariTabsStage{Duration: 2 * time.Second, Stats: safaritabs.Stats{TabsCreated: 1, TabsUpdated: 2, TabsUnchanged: 495, TabsRendered: 498, TabsSkipped: 2, LinksFound: 492, DeviceName: "This is an unusually long Safari device name used to verify deterministic cell wrapping"}},
		BlueskyBookmarks:  &syncjob.BlueskyBookmarksStage{Duration: 3 * time.Second, Stats: bskyapi.BookmarkStats{Created: 3, Updated: 1, Unchanged: 4, Skipped: 2, SkippedBlocked: 1, PagesFetched: 2, StoppedReason: "the persisted cursor was rejected after an unusually long remote synchronization explanation that must be bounded", MediaDiscovered: 9, MediaLinked: 8, MediaUnavailable: 7, MediaDownloaded: 6, MediaGone: 5, MediaErrors: 4, MediaBlocked: 3, QuoteLinked: 2, QuoteSkipped: 1}},
		MastodonBookmarks: &syncjob.MastodonBookmarksStage{Duration: 4 * time.Second, Stats: mastodonapi.BookmarkStats{AccountKey: "mastodon.example/@very-long-account-name", PagesFetched: 2, Created: 3, Updated: 1, Unchanged: 4, Skipped: 2, StoppedReason: "the remote server returned a detailed reason that needs deterministic clipping at every width", MediaDiscovered: 9, MediaLinked: 8, MediaUnavailable: 7, MediaDownloaded: 6, MediaGone: 5, MediaErrors: 4, MediaBlocked: 3, APIErrors: 2, RateLimits: 1, Retries: 2}},
		X:                 &syncjob.XStage{Duration: 5 * time.Second, Stats: xapi.Stats{Hydrated: 4, Rendered: 4, MediaDownloaded: 3, MediaBlocked: 2, Missing: 1, APIErrors: 1, MediaErrors: 1}},
		XMedia:            &syncjob.XMediaStage{Duration: 6 * time.Second, Stats: xmediatranscribe.Stats{ItemsQueued: 11, ItemsProcessed: 10, ItemsUpdated: 8, ItemsUnchanged: 1, ItemsSkipped: 4, MediaCandidates: 12, MediaWithAudio: 7, MediaTranscribed: 6, SummaryCandidates: 9, ItemsSummarized: 5, Errors: 1, SummaryErrors: 2}},
		XPhotoOCR:         &syncjob.XPhotoOCRStage{Duration: 7 * time.Second, Stats: xphotoocr.Stats{ItemsQueued: 11, ItemsProcessed: 10, ItemsUpdated: 5, ItemsUnchanged: 1, ItemsSkipped: 4, PhotoCandidates: 12, PhotosOCRed: 6, HostedAttempts: 7, HostedFallbacks: 2, Errors: 3}},
		Links:             &syncjob.LinksStage{Duration: 8 * time.Second, Stats: linkextract.Stats{ItemsScanned: 12, LinksFound: 14, SourcesQueued: 11, SourcesExtracted: 10, SourcesSummarized: 9, SourcesRendered: 8, SourcesUnchanged: 7, Errors: 1}},
		Sources:           &syncjob.SourcesStage{Duration: 9 * time.Second, Stats: worker.SourceStats{Cycles: 4, WorkCycles: 3, IdlePolls: 2, SourcesQueued: 12, SourcesExtracted: 11, SourcesSummarized: 10, SourcesRendered: 9, SourcesUnchanged: 8, StoppedReason: "no work remained after a deliberately long idle-stop explanation that exercises wrapping", Errors: 1}},
		Categorize:        &syncjob.CategorizeStage{Duration: 10 * time.Second, Stats: itemcategorize.Stats{Succeeded: 7, Skipped: 2, Errors: 1}, ItemStats: itemcategorize.Stats{Applied: 6, Queued: 7}, SourceStats: itemcategorize.Stats{Applied: 5, Queued: 6}},
		MediaArchive:      &syncjob.MediaArchiveStage{Duration: 11 * time.Second, Stats: mediaarchive.Stats{Candidates: 10, Uploaded: 4, Archived: 3, LocalFilesPruned: 2, LocalRowsPruned: 5, PruneSkipped: 2, Unchanged: 1, Errors: 1}},
	}
	semantic := semanticrefresh.Result{Outcome: semanticrefresh.OutcomeCompleted, Stages: []semanticrefresh.StageStats{
		{Stage: store.SemanticRefreshEmbedding, Duration: 12 * time.Second, Units: 3, Status: "ok", Counters: store.SemanticRefreshCounters{EmbeddedChunks: 17}, RemainingDebt: semanticrefresh.Debt{PendingEmbeddings: 2, DueRetries: 3, BlockedEmbeddings: 4, FailedEmbeddings: 5}},
		{Stage: store.SemanticRefreshReadiness, Duration: 13 * time.Second, Status: "error", RemainingDebt: semanticrefresh.Debt{Indexed: 15, DirtyParents: 4, PendingEmbeddings: 3, FailedEmbeddings: 2}},
	}}
	gc := &syncSemanticGCResult{Status: syncSemanticGCStatusError, Duration: 14 * time.Second, GenerationsPruned: 4, SegmentsPruned: 2, MemberRowsPruned: 19, FilesystemDeleted: 6, DeletedBytes: 2048, Error: "filesystem_unlink"}
	return stats, semantic, gc
}
