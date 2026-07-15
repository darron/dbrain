package xmediatranscribe

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func TestSaveTranscriptItemPersistsMixedSettingsProvenanceBeforePublishingTranscript(t *testing.T) {
	t.Parallel()

	cfg, st, item := openTranscriptSaveFixture(t, true)
	seedTranscriptMediaInputs(t, st, item.ID)
	blocks := []transcriptBlock{
		{Heading: "Video 1", Text: "first transcript block", Backend: "macwhisper", Model: "automatic", Language: "auto"},
		{Heading: "Video 2", Text: "second transcript block", Backend: "whisper.cpp", Model: "ggml-base.bin", Language: "en", VADEnabled: true},
	}

	changed, err := saveTranscriptItem(context.Background(), cfg, st, Options{}, item, blocks)
	if err != nil {
		t.Fatalf("saveTranscriptItem: %v", err)
	}
	if !changed {
		t.Fatal("saveTranscriptItem reported unchanged for new mixed transcript")
	}
	got, err := st.GetItem(context.Background(), item.SourceKey)
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.ArticleTitle != transcriptArticleTitle || !strings.Contains(got.ArticleText, "first transcript block") || !strings.Contains(got.ArticleText, "second transcript block") {
		t.Fatalf("transcript article was not published: title=%q text=%q", got.ArticleTitle, got.ArticleText)
	}
	if got.XMediaTranscriptStatus != model.XMediaTranscriptStatusOK {
		t.Fatalf("transcript status = %q, want ok", got.XMediaTranscriptStatus)
	}
	if got.SummaryStatus != "" || got.SummaryText != "" {
		t.Fatalf("stale summary survived successful transcript replacement: status=%q text=%q", got.SummaryStatus, got.SummaryText)
	}
	enrichment, err := st.GetItemEnrichment(context.Background(), item.ID, model.ItemEnrichmentRoleXMediaTranscript)
	if err != nil {
		t.Fatalf("GetItemEnrichment: %v", err)
	}
	if enrichment.RawJSON != `[{"backend":"macwhisper","model":"automatic","language":"auto","vad_enabled":false},{"backend":"whisper.cpp","model":"ggml-base.bin","language":"en","vad_enabled":true}]` {
		t.Fatalf("unexpected raw mixed provenance: %s", enrichment.RawJSON)
	}
	if enrichment.Tool != `["macwhisper","whisper.cpp"]` || enrichment.Model != `["automatic","ggml-base.bin"]` {
		t.Fatalf("unexpected mixed aggregate provenance: tool=%q model=%q", enrichment.Tool, enrichment.Model)
	}
	if enrichment.ToolVersion != xMediaTranscriptionToolVersion || !strings.HasPrefix(enrichment.InputHash, "sha256:") || enrichment.CompletedAt.IsZero() {
		t.Fatalf("incomplete mixed transcript provenance: %+v", enrichment)
	}
}

func TestSaveTranscriptItemProvenancePreflightFailureDoesNotMutateItemOrSummary(t *testing.T) {
	t.Parallel()

	cfg, st, item := openTranscriptSaveFixture(t, true)
	before, err := st.GetItem(context.Background(), item.SourceKey)
	if err != nil {
		t.Fatalf("GetItem before: %v", err)
	}

	_, err = saveTranscriptItem(context.Background(), cfg, st, Options{}, item, []transcriptBlock{
		{Heading: "Video 1", Text: "invalid transcript block", Backend: "", Model: "unknown-model", Language: "en"},
	})
	if err == nil {
		t.Fatal("saveTranscriptItem succeeded without a resolved backend")
	}
	after, err := st.GetItem(context.Background(), item.SourceKey)
	if err != nil {
		t.Fatalf("GetItem after: %v", err)
	}
	if after.ArticleTitle != before.ArticleTitle || after.ArticleText != before.ArticleText || after.ContentHash != before.ContentHash ||
		after.SummaryStatus != before.SummaryStatus || after.SummaryText != before.SummaryText || after.XMediaTranscriptStatus != before.XMediaTranscriptStatus {
		t.Fatalf("failed provenance preflight mutated item:\nbefore=%+v\nafter=%+v", before, after)
	}
	if _, err := st.GetItemEnrichment(context.Background(), item.ID, model.ItemEnrichmentRoleXMediaTranscript); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed provenance preflight created transcript enrichment: %v", err)
	}
}

func TestSaveTranscriptItemInputHashPreflightFailureDoesNotMutateItemOrSummary(t *testing.T) {
	t.Parallel()

	cfg, st, item := openTranscriptSaveFixture(t, true)
	before, err := st.GetItem(context.Background(), item.SourceKey)
	if err != nil {
		t.Fatalf("GetItem before: %v", err)
	}

	_, err = saveTranscriptItem(context.Background(), cfg, st, Options{}, item, []transcriptBlock{
		{Heading: "Video 1", Text: "valid transcript without durable media input", Backend: "macwhisper", Model: "automatic", Language: "auto"},
	})
	if err == nil {
		t.Fatal("saveTranscriptItem succeeded without a durable eligible media content hash")
	}
	after, err := st.GetItem(context.Background(), item.SourceKey)
	if err != nil {
		t.Fatalf("GetItem after: %v", err)
	}
	if after.ArticleTitle != before.ArticleTitle || after.ArticleText != before.ArticleText || after.ContentHash != before.ContentHash ||
		after.SummaryStatus != before.SummaryStatus || after.SummaryText != before.SummaryText || after.XMediaTranscriptStatus != before.XMediaTranscriptStatus {
		t.Fatalf("failed input-hash preflight mutated item:\nbefore=%+v\nafter=%+v", before, after)
	}
	if _, err := st.GetItemEnrichment(context.Background(), item.ID, model.ItemEnrichmentRoleXMediaTranscript); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed input-hash preflight created transcript enrichment: %v", err)
	}
}

func openTranscriptSaveFixture(t *testing.T, withSummary bool) (config.Config, *store.Store, model.Item) {
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
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, 7, 14, 1, 0, 0, 0, time.UTC)
	result, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey: "x:test-mixed-transcript", SourceType: "x_bookmark", ExternalID: "mixed-transcript",
		CanonicalURL: "https://x.com/example/status/mixed-transcript", Title: "Mixed transcript",
		ArticleTitle: "Original article", ArticleText: "Original article text", ContentHash: "original-content-hash",
		NotePath: "items/x/mixed-transcript.md", RawJSON: `{}`, ImportedAt: now, UpdatedAt: now, LastSeenAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	if withSummary {
		if _, err := st.SaveItemSummary(context.Background(), result.ItemID, model.SummaryResult{
			Text: "summary that must survive failed preflight", RawJSON: `{"summary":true}`, Model: "test-model",
			PromptVersion: "prompt-v1", Status: model.ItemSummaryStatusOK, FetchedAt: now,
			Tool: "test-tool", ToolVersion: "tool-v1",
		}, "sha256:summary-input"); err != nil {
			t.Fatalf("SaveItemSummary: %v", err)
		}
	}
	item, err := st.GetItem(context.Background(), "x:test-mixed-transcript")
	if err != nil {
		t.Fatalf("GetItem fixture: %v", err)
	}
	return cfg, st, item
}

func seedTranscriptMediaInputs(t *testing.T, st *store.Store, itemID int64) {
	t.Helper()
	now := time.Date(2026, 7, 14, 1, 5, 0, 0, time.UTC)
	if _, err := st.SaveXHydration(context.Background(), itemID, model.XHydration{
		FullText: "mixed media", Language: "en", Status: "ok_graphql", FetchedAt: now,
		APIJSON: `{"snapshot":{"media_objects":[{"type":"video","url":"https://video.twimg.com/mixed-1.mp4"},{"type":"video","url":"https://video.twimg.com/mixed-2.mp4"}]}}`,
	}); err != nil {
		t.Fatalf("SaveXHydration: %v", err)
	}
	refs, err := st.ListItemMediaRefs(context.Background(), itemID)
	if err != nil {
		t.Fatalf("ListItemMediaRefs: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("media refs = %d, want 2: %+v", len(refs), refs)
	}
	for i, ref := range refs {
		if _, err := st.SaveMediaDownload(context.Background(), ref.MediaAssetID, model.MediaDownloadResult{
			MIMEType: "video/mp4", ByteSize: int64(100 + i), ContentHash: "sha256:media-" + string(rune('a'+i)),
			LocalPath: "media/x/video/mixed-" + string(rune('a'+i)) + ".mp4", Status: model.MediaDownloadStatusDownloaded, DownloadedAt: now,
		}); err != nil {
			t.Fatalf("SaveMediaDownload %d: %v", i, err)
		}
	}
}
