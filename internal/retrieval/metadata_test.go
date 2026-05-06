package retrieval

import (
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestItemMetadataKeepsSlimPayloadShape(t *testing.T) {
	importedAt := time.Date(2026, 5, 5, 12, 0, 0, 0, time.FixedZone("test", -6*60*60))
	item := model.Item{
		ID:            7,
		SourceKey:     "x:7",
		SourceType:    "x_bookmark",
		CanonicalURL:  "https://x.com/a/status/7",
		Title:         "Title",
		UserTags:      "tag",
		SummaryStatus: "ok",
		ImportedAt:    importedAt,
	}

	got := ItemMetadata(item)
	if got["id"] != item.ID || got["source_key"] != item.SourceKey || got["canonical_url"] != item.CanonicalURL {
		t.Fatalf("identity fields were not mapped: %#v", got)
	}
	if got["summary_status"] != "ok" || got["user_tags"] != "tag" {
		t.Fatalf("status/tag fields were not mapped: %#v", got)
	}
	if got["imported_at"] != "2026-05-05T18:00:00Z" {
		t.Fatalf("expected UTC imported_at, got %#v", got["imported_at"])
	}
	if _, ok := got["raw_json"]; ok {
		t.Fatalf("slim item metadata should not expose raw_json: %#v", got)
	}
}

func TestSourceMetadataKeepsSlimPayloadShape(t *testing.T) {
	extractedAt := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	source := model.SourceDocument{
		ID:                  9,
		SourceKey:           "src:9",
		CanonicalURL:        "https://example.com",
		SourceType:          "web",
		ExtractStatus:       "error",
		ExtractFailureKind:  "http_access_denied",
		ExtractFailureCount: 3,
		ExtractedAt:         extractedAt,
		SummaryStatus:       "blocked",
	}

	got := SourceMetadata(source)
	if got["id"] != source.ID || got["source_key"] != source.SourceKey || got["canonical_url"] != source.CanonicalURL {
		t.Fatalf("identity fields were not mapped: %#v", got)
	}
	if got["extract_failure_kind"] != "http_access_denied" || got["extract_failure_count"] != 3 {
		t.Fatalf("failure fields were not mapped: %#v", got)
	}
	if got["extracted_at"] != "2026-05-05T12:00:00Z" {
		t.Fatalf("expected UTC extracted_at, got %#v", got["extracted_at"])
	}
	if _, ok := got["extracted_text"]; ok {
		t.Fatalf("slim source metadata should not expose extracted_text: %#v", got)
	}
}
