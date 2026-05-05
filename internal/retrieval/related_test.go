package retrieval

import (
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestRelatedDocumentFromItemMapsStablePayloadFields(t *testing.T) {
	importedAt := time.Date(2026, 5, 5, 12, 0, 0, 0, time.FixedZone("test", -6*60*60))
	updatedAt := importedAt.Add(time.Hour)
	item := model.Item{
		ID:                     42,
		SourceKey:              "x:123",
		SourceType:             "x_bookmark",
		ExternalID:             "123",
		CanonicalURL:           "https://x.com/a/status/123",
		Title:                  "Title",
		AuthorHandle:           "@author",
		AuthorName:             "Author",
		PublishedAt:            "2026-05-04T00:00:00Z",
		SavedAt:                "2026-05-05T00:00:00Z",
		Language:               "en",
		PrimaryCategory:        "category",
		PrimaryDomain:          "example.com",
		NotePath:               "items/x/2026/123.md",
		UserTags:               "tag",
		XPostStatus:            "ok",
		SummaryStatus:          "ok",
		SummaryModel:           "model",
		SummaryTool:            "tool",
		OCRStatus:              "done",
		OCRModel:               "ocr-model",
		OCRTool:                "ocr-tool",
		XMediaTranscriptStatus: "transcribed",
		ImportedAt:             importedAt,
		UpdatedAt:              updatedAt,
	}

	doc := RelatedDocumentFromItem(item)
	if doc.ID != item.ID || doc.SourceKey != item.SourceKey || doc.CanonicalURL != item.CanonicalURL {
		t.Fatalf("identity fields were not mapped: %#v", doc)
	}
	if doc.SummaryModel != "model" || doc.OCRModel != "ocr-model" || doc.XMediaTranscriptStatus != "transcribed" {
		t.Fatalf("enrichment fields were not mapped: %#v", doc)
	}
	if doc.ImportedAt != "2026-05-05T18:00:00Z" {
		t.Fatalf("expected UTC imported_at, got %q", doc.ImportedAt)
	}
	if doc.UpdatedAt != "2026-05-05T19:00:00Z" {
		t.Fatalf("expected UTC updated_at, got %q", doc.UpdatedAt)
	}
	if doc.LastSeenAt != "" {
		t.Fatalf("expected zero last_seen_at to be omitted, got %q", doc.LastSeenAt)
	}
}

func TestFirstNonZeroTime(t *testing.T) {
	want := time.Date(2026, 5, 5, 1, 2, 3, 0, time.UTC)
	if got := FirstNonZeroTime(time.Time{}, want, want.Add(time.Hour)); !got.Equal(want) {
		t.Fatalf("expected first non-zero time %s, got %s", want, got)
	}
	if got := FirstNonZeroTime(time.Time{}, time.Time{}); !got.IsZero() {
		t.Fatalf("expected zero time, got %s", got)
	}
}
