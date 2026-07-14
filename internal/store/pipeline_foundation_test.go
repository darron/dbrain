package store

import (
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestWorkerPendingMatchesPipelineMediaArchive(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	now := time.Now().UTC()
	seedPhoto := func(key string) int64 {
		item, err := st.UpsertItem(t.Context(), model.Item{
			SourceKey: key, SourceType: "x_bookmark", ExternalID: key,
			CanonicalURL: "https://x.com/example/status/" + key, Title: key,
			ContentHash: key, LinksJSON: "[]", NotePath: "items/x/archive.md", RawJSON: `{}`,
			ImportedAt: now, UpdatedAt: now, LastSeenAt: now,
		})
		if err != nil {
			t.Fatalf("UpsertItem: %v", err)
		}
		if _, err := st.SaveXHydration(t.Context(), item.ItemID, model.XHydration{
			Status: "ok_graphql", FullText: "photo", FetchedAt: now,
			APIJSON: `{"snapshot":{"media_objects":[{"type":"photo","url":"https://pbs.twimg.com/media/` + key + `.png"}]}}`,
		}); err != nil {
			t.Fatalf("SaveXHydration: %v", err)
		}
		refs, err := st.ListItemMediaRefs(t.Context(), item.ItemID)
		if err != nil || len(refs) != 1 {
			t.Fatalf("ListItemMediaRefs: %v %+v", err, refs)
		}
		if _, err := st.SaveMediaDownload(t.Context(), refs[0].MediaAssetID, model.MediaDownloadResult{
			LocalPath: "media/x/photo/" + key + ".png", ContentHash: key, Status: model.MediaDownloadStatusDownloaded, DownloadedAt: now,
		}); err != nil {
			t.Fatalf("SaveMediaDownload: %v", err)
		}
		if _, err := st.db.ExecContext(t.Context(), `UPDATE items SET ocr_status='ok', ocr_text='text' WHERE id=?`, item.ItemID); err != nil {
			t.Fatalf("seed OCR: %v", err)
		}
		return refs[0].MediaAssetID
	}
	seedPhoto("x:archive-pending")
	invalidAssetID := seedPhoto("x:archive-invalid")
	if _, err := st.db.ExecContext(t.Context(), `UPDATE media_assets SET archive_status='legacy_bogus' WHERE id=?`, invalidAssetID); err != nil {
		t.Fatalf("seed invalid archive status: %v", err)
	}

	candidates, err := st.ListMediaAssetsForArchive(t.Context(), 100, false)
	if err != nil {
		t.Fatalf("ListMediaAssetsForArchive: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("worker candidates = %d, want 1", len(candidates))
	}
	stats, err := st.Pipeline(t.Context(), "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}
	row := pipelineRowByKind(t, stats.MediaArchive, "media_archive")
	if row.Total != 2 || row.Pending != len(candidates) || row.Unknown != 1 || !row.PartitionValid {
		t.Fatalf("unexpected archive partitions: %+v candidates=%d", row, len(candidates))
	}
}

func TestWorkerPendingMatchesPipelineSourceRepair(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	sourceID := insertTestSource(t, st, "src:makerworld-repair", "https://makerworld.com/en/models/1")
	if _, err := st.db.ExecContext(t.Context(), `
		UPDATE sources SET domain='makerworld.com', extract_status='dead',
			extract_failure_kind='http_access_denied', extract_failure_count=2
		WHERE id=?`, sourceID); err != nil {
		t.Fatalf("seed MakerWorld repair: %v", err)
	}
	candidates, err := st.ListSourcesForEnrichment(t.Context(), 100, false, false, "", "", "")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("source worker candidates = %d, want 1", len(candidates))
	}
	stats, err := st.Pipeline(t.Context(), "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}
	extraction := pipelineRowByKind(t, stats.Extraction, "web")
	summary := pipelineRowByKind(t, stats.Summary, "web")
	if extraction.Pending != 1 || !extraction.PartitionValid || summary.Pending != 1 || summary.Blocked != 0 || !summary.PartitionValid {
		t.Fatalf("source repair partitions disagree: extraction=%+v summary=%+v", extraction, summary)
	}
}

func TestPipelinePartitionsClassifiesKnownHydrationTerminalStates(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	for _, status := range []string{"not_found", "empty"} {
		seedAuditSnapshotItem(t, st, "x:hydration-terminal-"+status)
		if _, err := st.db.ExecContext(t.Context(), `UPDATE items SET x_post_status=? WHERE source_key=?`, status, "x:hydration-terminal-"+status); err != nil {
			t.Fatalf("seed hydration status %q: %v", status, err)
		}
	}
	stats, err := st.Pipeline(t.Context(), "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}
	row := pipelineRowByKind(t, stats.Hydration, "ALL")
	if row.Total != 2 || row.Terminal != 2 || row.Unknown != 0 || !row.PartitionValid {
		t.Fatalf("known hydration terminal states misclassified: %+v", row)
	}
}

func TestPipelinePartitionsClassifiesSourceExtractionTerminalAndCooldown(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	now := time.Now().UTC()
	deadID := insertTestSource(t, st, "src:terminal-dead", "https://example.com/dead")
	goneID := insertTestSource(t, st, "src:terminal-gone", "https://example.com/gone")
	recentErrorID := insertTestSource(t, st, "src:cooldown-error", "https://example.com/error")
	if _, err := st.db.ExecContext(t.Context(), `UPDATE sources SET extract_status='dead' WHERE id=?`, deadID); err != nil {
		t.Fatalf("seed dead source: %v", err)
	}
	if _, err := st.db.ExecContext(t.Context(), `UPDATE sources SET extract_status='gone' WHERE id=?`, goneID); err != nil {
		t.Fatalf("seed gone source: %v", err)
	}
	if _, err := st.db.ExecContext(t.Context(), `
		UPDATE sources SET extract_status='error', extract_failure_kind='fetch_failed',
			extract_failure_count=1, extract_last_failed_at=? WHERE id=?`, now.Format(time.RFC3339), recentErrorID); err != nil {
		t.Fatalf("seed recent source error: %v", err)
	}

	worker, err := st.ListSourcesForEnrichment(t.Context(), 100, false, false, "", "", "")
	if err != nil {
		t.Fatalf("ListSourcesForEnrichment: %v", err)
	}
	if len(worker) != 0 {
		t.Fatalf("terminal/cooldown sources unexpectedly runnable: %+v", worker)
	}
	stats, err := st.Pipeline(t.Context(), "", "", "")
	if err != nil {
		t.Fatalf("Pipeline: %v", err)
	}
	row := pipelineRowByKind(t, stats.Extraction, "web")
	if row.Total != 3 || row.Terminal != 2 || row.Blocked != 1 || row.Unknown != 0 || !row.PartitionValid {
		t.Fatalf("known extraction states misclassified: %+v", row)
	}
}
