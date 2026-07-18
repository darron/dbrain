package applenotes

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrievalchunk"
	"github.com/darron/dbrain/internal/store"
)

func TestRunDryRunDoesNotMaterialize(t *testing.T) {
	t.Parallel()

	cfg, st, dbPath := newRunFixture(t)
	stats, err := Run(context.Background(), cfg, st, Options{
		DBPath:     dbPath,
		DryRun:     true,
		ShowTitles: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !stats.DryRun || stats.Applied {
		t.Fatalf("expected dry-run stats, got dry_run=%v applied=%v", stats.DryRun, stats.Applied)
	}
	if stats.NotesMatched != 1 {
		t.Fatalf("NotesMatched = %d, want 1", stats.NotesMatched)
	}
	if len(stats.SampleTitles) != 1 || stats.SampleTitles[0] != "Reader Note" {
		t.Fatalf("SampleTitles = %#v", stats.SampleTitles)
	}
	if _, err := st.GetItem(context.Background(), "apple-note:default:note-1"); err == nil {
		t.Fatalf("dry-run materialized item")
	}
}

func TestRunMaterializesAndRendersByDefault(t *testing.T) {
	t.Parallel()

	cfg, st, dbPath := newRunFixture(t)
	stats, err := Run(context.Background(), cfg, st, Options{
		DBPath: dbPath,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.NotesCreated != 1 || stats.NotesRendered != 1 || stats.AttachmentsIndexed != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	item, err := st.GetItem(context.Background(), "apple-note:default:note-1")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.SourceType != "apple_note" {
		t.Fatalf("SourceType = %q", item.SourceType)
	}
	if !strings.Contains(item.Text, "Decoded body") {
		t.Fatalf("Text = %q", item.Text)
	}
	if !strings.Contains(item.LinksJSON, "https://example.com/page") {
		t.Fatalf("LinksJSON = %q", item.LinksJSON)
	}
	if !strings.Contains(item.RawJSON, "apple_note_tags") || !strings.Contains(item.RawJSON, "attachments") {
		t.Fatalf("RawJSON missing Apple Notes metadata: %s", item.RawJSON)
	}
	if item.ArticleTitle != "Apple Notes Attachment Text" ||
		!strings.Contains(item.ArticleText, "Attachment OCR text") ||
		!strings.Contains(item.ArticleText, "PDF extracted text") ||
		!strings.Contains(item.ArticleText, "https://attachment.example/file.pdf") {
		t.Fatalf("attachment text not materialized: title=%q text=%q", item.ArticleTitle, item.ArticleText)
	}
	renderedPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(item.NotePath))
	body, err := os.ReadFile(renderedPath)
	if err != nil {
		t.Fatalf("read rendered note: %v", err)
	}
	if !strings.Contains(string(body), "## Apple Note Body") {
		t.Fatalf("rendered note missing Apple Note body heading:\n%s", body)
	}
}

func TestRunReportsUnchanged(t *testing.T) {
	t.Parallel()

	cfg, st, dbPath := newRunFixture(t)
	if _, err := Run(context.Background(), cfg, st, Options{DBPath: dbPath}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	stats, err := Run(context.Background(), cfg, st, Options{DBPath: dbPath})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if stats.NotesUnchanged != 1 {
		t.Fatalf("NotesUnchanged = %d, want 1 (stats=%+v)", stats.NotesUnchanged, stats)
	}
	if stats.NotesImported != 0 {
		t.Fatalf("NotesImported = %d, want 0 for unchanged-current note (stats=%+v)", stats.NotesImported, stats)
	}
}

func TestRunLimitSkipsUnchangedAndAdvances(t *testing.T) {
	t.Parallel()

	cfg, st, dbPath := newRunFixture(t)
	first, err := Run(context.Background(), cfg, st, Options{DBPath: dbPath, Limit: 1})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.NotesCreated != 1 {
		t.Fatalf("first NotesCreated = %d, want 1 (stats=%+v)", first.NotesCreated, first)
	}
	insertFixtureNote(t, dbPath, 4, "note-2", "Second Note", "Second decoded body")

	second, err := Run(context.Background(), cfg, st, Options{DBPath: dbPath, Limit: 1})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.NotesCreated != 1 {
		t.Fatalf("second NotesCreated = %d, want 1 (stats=%+v)", second.NotesCreated, second)
	}
	if second.NotesUnchanged != 1 {
		t.Fatalf("second NotesUnchanged = %d, want 1 unchanged note skipped before next import (stats=%+v)", second.NotesUnchanged, second)
	}
	if _, err := st.GetItem(context.Background(), "apple-note:default:note-2"); err != nil {
		t.Fatalf("second note was not imported: %v", err)
	}
}

func TestRunEmitsProgressForLoadedAndNotes(t *testing.T) {
	t.Parallel()

	cfg, st, dbPath := newRunFixture(t)
	var events []ProgressEvent
	_, err := Run(context.Background(), cfg, st, Options{
		DBPath: dbPath,
		Progress: func(event ProgressEvent) {
			events = append(events, event)
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected loaded and item events, got %#v", events)
	}
	if events[0].Phase != "snapshotting" {
		t.Fatalf("first progress event = %#v, want snapshotting", events[0])
	}
	if !hasProgressEvent(events, "loaded") {
		t.Fatalf("missing loaded progress event: %#v", events)
	}
	last := events[len(events)-1]
	if last.Phase != "imported" || last.Status != string(model.UpsertCreated) || last.Links != 2 || last.Attachments != 1 {
		t.Fatalf("last progress event = %#v", last)
	}
}

func TestRunSummarizesWhenEnabled(t *testing.T) {
	cfg, st, dbPath := newRunFixture(t)
	installFakeSummarize(t)
	stats, err := Run(context.Background(), cfg, st, Options{
		DBPath:        dbPath,
		Summarize:     true,
		SummaryModel:  "test/model",
		SummaryCLI:    "cli",
		SummaryLength: "short",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.SummariesCreated != 1 {
		t.Fatalf("SummariesCreated = %d, want 1 (stats=%+v)", stats.SummariesCreated, stats)
	}
	item, err := st.GetItem(context.Background(), "apple-note:default:note-1")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.SummaryText != "apple note summary" {
		t.Fatalf("SummaryText = %q", item.SummaryText)
	}
	if item.SummaryPromptVersion != appleNoteSummaryPromptVersion {
		t.Fatalf("SummaryPromptVersion = %q", item.SummaryPromptVersion)
	}
}

func TestRunSkipsCurrentSummaryUnlessForced(t *testing.T) {
	cfg, st, dbPath := newRunFixture(t)
	counter := installCountingFakeSummarize(t)

	first, err := Run(context.Background(), cfg, st, Options{
		DBPath:        dbPath,
		Summarize:     true,
		SummaryModel:  "test/model",
		SummaryCLI:    "cli",
		SummaryLength: "short",
	})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.SummariesCreated != 1 {
		t.Fatalf("first SummariesCreated = %d, want 1", first.SummariesCreated)
	}

	second, err := Run(context.Background(), cfg, st, Options{
		DBPath:        dbPath,
		Summarize:     true,
		SummaryModel:  "test/model",
		SummaryCLI:    "cli",
		SummaryLength: "short",
	})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.SummariesCreated != 0 {
		t.Fatalf("second SummariesCreated = %d, want 0", second.SummariesCreated)
	}
	if got := readCounter(t, counter); got != "1" {
		t.Fatalf("summary command count after second run = %q, want 1", got)
	}

	forced, err := Run(context.Background(), cfg, st, Options{
		DBPath:        dbPath,
		Force:         true,
		Summarize:     true,
		SummaryModel:  "test/model",
		SummaryCLI:    "cli",
		SummaryLength: "short",
	})
	if err != nil {
		t.Fatalf("forced Run: %v", err)
	}
	if forced.NotesRendered == 0 {
		t.Fatalf("forced run should re-render unchanged notes: %+v", forced)
	}
	if got := readCounter(t, counter); got != "2" {
		t.Fatalf("summary command count after forced run = %q, want 2", got)
	}
}

func TestAppleNoteSummaryInputIncludesAttachmentText(t *testing.T) {
	t.Parallel()

	input := appleNoteSummaryInput(model.Item{
		Text:        "note body",
		ArticleText: "attachment text",
	})
	if !strings.Contains(input, "Apple Note Body:\nnote body") || !strings.Contains(input, "Apple Note Attachments:\nattachment text") {
		t.Fatalf("summary input did not include body and attachment text:\n%s", input)
	}
}

func TestRunExclusionsAndBlockedCounts(t *testing.T) {
	t.Parallel()

	stats := Stats{}
	countSkip(&stats, exclusionReason(NoteDocument{Shared: true}, Options{ExcludeShared: true}))
	countSkip(&stats, exclusionReason(NoteDocument{PasswordProtected: true}, Options{}))
	countSkip(&stats, exclusionReason(NoteDocument{AccountName: "iCloud"}, Options{ExcludeAccounts: []string{"icloud"}}))
	countSkip(&stats, exclusionReason(NoteDocument{FolderPath: "iCloud/Private"}, Options{ExcludeFolders: []string{"Private"}}))

	if stats.ExcludedShared != 1 || stats.BlockedLocked != 1 || stats.ExcludedAccounts != 1 || stats.ExcludedFolders != 1 {
		t.Fatalf("unexpected skip stats: %+v", stats)
	}
}

func TestRunForgetExcludedPurgesMaterializedNote(t *testing.T) {
	t.Parallel()

	cfg, st, dbPath := newRunFixture(t)
	if _, err := Run(context.Background(), cfg, st, Options{DBPath: dbPath}); err != nil {
		t.Fatalf("initial Run: %v", err)
	}
	ctx := context.Background()
	materialized, err := st.GetItem(ctx, "apple-note:default:note-1")
	if err != nil {
		t.Fatalf("load materialized note: %v", err)
	}
	now := time.Now().UTC()
	if _, err := st.SaveItemSummary(ctx, materialized.ID, model.SummaryResult{
		Text: "private mirror summary", RawJSON: `{}`, Model: "test", PromptVersion: "v1",
		Status: model.ItemSummaryStatusOK, FetchedAt: now, Tool: "test", ToolVersion: "v1",
	}, "summary-hash"); err != nil {
		t.Fatalf("seed item summary mirror: %v", err)
	}
	if _, err := st.SaveItemOCR(ctx, materialized.ID, model.OCRResult{
		Text: "private mirror OCR", RawJSON: `{}`, Model: "test",
		Status: model.ItemOCRStatusOK, FetchedAt: now, Tool: "test", ToolVersion: "v1",
	}, "ocr-hash"); err != nil {
		t.Fatalf("seed item OCR mirror: %v", err)
	}
	if err := st.SaveXMediaTranscriptionState(ctx, materialized.ID, model.XMediaTranscriptStatusOK, "", now); err != nil {
		t.Fatalf("seed transcript mirror: %v", err)
	}
	chunk := retrievalchunk.Chunk{
		ID: "apple-note-private-chunk", ParentKind: "item", ParentSourceKey: "apple-note:default:note-1",
		EvidenceRole: "raw", SectionOrdinal: 0, Ordinal: 0, StartChar: 0, EndChar: 12,
		Heading: "Reader Note", ChunkerVersion: retrievalchunk.Version,
		InputContentHash: "apple-note-input", TextHash: "apple-note-text", Text: "Decoded body",
	}
	if _, err := st.ReplaceRetrievalChunks(ctx, "item", chunk.ParentSourceKey, []retrievalchunk.Chunk{chunk}); err != nil {
		t.Fatalf("seed retrieval chunk: %v", err)
	}
	if err := st.PutRetrievalEmbedding(ctx, store.RetrievalEmbeddingRow{
		ChunkID: chunk.ID, ProfileID: "apple-note-profile", Provider: "fake", Model: "fake-v1",
		Dimensions: 2, Representation: "dense_f32", Normalization: "l2",
		VectorBytes: []byte{0, 0, 128, 63, 0, 0, 0, 0}, ChunkTextHash: chunk.TextHash,
		Status: store.RetrievalEmbeddingReady,
	}); err != nil {
		t.Fatalf("seed retrieval embedding: %v", err)
	}
	if err := st.PutRetrievalIndexGeneration(ctx, store.RetrievalIndexGenerationRow{
		GenerationID: "apple-note-generation", ProfileID: "apple-note-profile",
		Backend: "hnsw", BackendVersion: "1", Dimensions: 2, DistanceMetric: "cosine",
		BuildStatus: store.RetrievalGenerationCompleted,
	}); err != nil {
		t.Fatalf("seed retrieval generation: %v", err)
	}
	if err := st.ActivateRetrievalIndexGeneration(ctx, "apple-note-generation"); err != nil {
		t.Fatalf("activate retrieval generation: %v", err)
	}
	stats, err := Run(context.Background(), cfg, st, Options{
		DBPath:         dbPath,
		ExcludeFolders: []string{"Private"},
		ForgetExcluded: true,
	})
	if err != nil {
		t.Fatalf("purge Run: %v", err)
	}
	if stats.NotesPurged != 1 {
		t.Fatalf("NotesPurged = %d, want 1 (stats=%+v)", stats.NotesPurged, stats)
	}
	item, err := st.GetItem(context.Background(), "apple-note:default:note-1")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.Text != "" || item.ArticleText != "" || item.LinksJSON != "[]" {
		t.Fatalf("item content not purged: text=%q article=%q links=%q", item.Text, item.ArticleText, item.LinksJSON)
	}
	if item.SummaryText != "" || item.OCRText != "" {
		t.Fatalf("authoritative enrichments rehydrated purged note: summary=%q ocr=%q", item.SummaryText, item.OCRText)
	}
	for _, role := range []string{
		model.ItemEnrichmentRoleSummary,
		model.ItemEnrichmentRoleOCR,
		model.ItemEnrichmentRoleXMediaTranscript,
	} {
		if _, err := st.GetItemEnrichment(ctx, item.ID, role); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("purged note enrichment %s still exists: %v", role, err)
		}
	}
	parents, err := st.ListRetrievalParents(ctx, "", 10)
	if err != nil {
		t.Fatalf("ListRetrievalParents after purge: %v", err)
	}
	if len(parents) != 1 || len(parents[0].Sections) != 0 {
		t.Fatalf("purged Apple Note remains projectable: %+v", parents)
	}
	results, err := st.Search(context.Background(), "Decoded body", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, result := range results {
		if result.SourceKey == item.SourceKey {
			t.Fatalf("purged item still appears in search: %+v", result)
		}
	}
	ready, err := st.ListReadyEmbeddings(ctx, "apple-note-profile", 10)
	if err != nil {
		t.Fatalf("ListReadyEmbeddings after purge: %v", err)
	}
	if len(ready) != 0 {
		t.Fatalf("purged Apple Note still has ready embeddings: %+v", ready)
	}
	status, err := st.RetrievalStatus(ctx, "apple-note-profile")
	if err != nil {
		t.Fatalf("RetrievalStatus after purge: %v", err)
	}
	if status.ActiveGenerationID != "" || status.StaleGenerations != 1 {
		t.Fatalf("retrieval status after purge = %+v, want one stale inactive generation", status)
	}
}

func newRunFixture(t *testing.T) (config.Config, *store.Store, string) {
	t.Helper()

	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	sourceDB := filepath.Join(root, "NoteStore.sqlite")
	writeReaderFixture(t, sourceDB)
	return cfg, st, sourceDB
}

func insertFixtureNote(t *testing.T, sourceDB string, pk int, id string, title string, body string) {
	t.Helper()

	db, err := sql.Open("sqlite", sourceDB)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()
	noteDataPK := pk + 100
	if _, err := db.Exec(`INSERT INTO ZICCLOUDSYNCINGOBJECT (
		Z_PK, ZIDENTIFIER, ZTITLE1, ZSNIPPET, ZNOTEDATA, ZCREATIONDATE1, ZMODIFICATIONDATE1, ZFOLDERPATH
	) VALUES (?, ?, ?, ?, ?, 100, 200, 'iCloud/Test')`, pk, id, title, body, noteDataPK); err != nil {
		t.Fatalf("insert fixture note row: %v", err)
	}
	zdata := append([]byte{0x0a, byte(len(body))}, []byte(body)...)
	if _, err := db.Exec(`INSERT INTO ZICNOTEDATA (Z_PK, ZDATA) VALUES (?, ?)`, noteDataPK, zdata); err != nil {
		t.Fatalf("insert fixture note data: %v", err)
	}
}

func hasProgressEvent(events []ProgressEvent, phase string) bool {
	for _, event := range events {
		if event.Phase == phase {
			return true
		}
	}
	return false
}

func installFakeSummarize(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "summarize")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ] || [ \"$1\" = \"version\" ]; then echo \"test-1.0.0\"; exit 0; fi\ncat >/dev/null\nprintf '%s' '{\"input\":{\"model\":\"test/model\"},\"extracted\":{\"url\":\"\",\"title\":\"\",\"description\":\"\",\"siteName\":\"\",\"content\":\"\"},\"summary\":\"apple note summary\"}'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installCountingFakeSummarize(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	counter := filepath.Join(dir, "counter")
	path := filepath.Join(dir, "summarize")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ] || [ \"$1\" = \"version\" ]; then echo \"test-1.0.0\"; exit 0; fi\ncat >/dev/null\ncount=0\nif [ -f " + shellQuote(counter) + " ]; then count=$(cat " + shellQuote(counter) + "); fi\ncount=$((count + 1))\nprintf '%s' \"$count\" > " + shellQuote(counter) + "\nprintf '%s' '{\"input\":{\"model\":\"test/model\"},\"extracted\":{\"url\":\"\",\"title\":\"\",\"description\":\"\",\"siteName\":\"\",\"content\":\"\"},\"summary\":\"apple note summary\"}'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake summarize: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return counter
}

func readCounter(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
