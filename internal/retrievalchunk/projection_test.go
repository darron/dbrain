package retrievalchunk

import (
	"testing"

	"github.com/darron/dbrain/internal/model"
)

func TestProjectItemSeparatesRawOCRTranscriptAndDerivedEvidence(t *testing.T) {
	item := model.Item{
		SourceKey:              "x:123",
		SourceType:             "x_bookmark",
		Title:                  "An item",
		AuthorName:             "Ada",
		ContentHash:            "item-v1",
		Text:                   "raw item text",
		OCRText:                "raw OCR text",
		ArticleTitle:           model.XMediaTranscriptArticleTitle,
		ArticleText:            "raw transcript text",
		XMediaTranscriptStatus: model.XMediaTranscriptStatusOK,
		SummaryText:            "derived summary text",
	}

	got := ProjectItem(item)
	if got.Kind != "item" || got.SourceKey != item.SourceKey || got.Author != item.AuthorName {
		t.Fatalf("unexpected item projection metadata: %+v", got)
	}
	want := []Section{
		{Role: "raw", Heading: "An item", Text: "raw item text"},
		{Role: "ocr", Heading: "OCR", Text: "raw OCR text"},
		{Role: "transcript", Heading: model.XMediaTranscriptArticleTitle, Text: "raw transcript text"},
		{Role: "summary", Heading: "Summary", Text: "derived summary text", Derived: true},
	}
	assertSections(t, got.Sections, want)
}

func TestProjectItemTreatsOrdinaryArticleTextAsRaw(t *testing.T) {
	got := ProjectItem(model.Item{
		SourceKey: "item:article", ContentHash: "item-v1", Text: "feed text",
		ArticleTitle: "Full article", ArticleText: "article body",
	})
	want := []Section{
		{Role: "raw", Text: "feed text"},
		{Role: "raw", Heading: "Full article", Text: "article body"},
	}
	assertSections(t, got.Sections, want)
}

func TestProjectSourceSeparatesExtractFromDerivedSummary(t *testing.T) {
	source := model.SourceDocument{
		SourceKey: "src:123", SourceType: "article", Title: "Architecture",
		Domain: "example.com", ContentHash: "source-v1",
		ExtractedText: "raw source extract", SummaryText: "derived source summary",
	}

	got := ProjectSource(source)
	if got.Kind != "source" || got.SourceKey != source.SourceKey || got.Author != source.Domain {
		t.Fatalf("unexpected source projection metadata: %+v", got)
	}
	want := []Section{
		{Role: "raw", Heading: "Architecture", Text: "raw source extract"},
		{Role: "summary", Heading: "Summary", Text: "derived source summary", Derived: true},
	}
	assertSections(t, got.Sections, want)
}

func TestProjectionOmitsBlankAndDuplicateRawSections(t *testing.T) {
	got := ProjectItem(model.Item{
		SourceKey: "item:dedupe", ContentHash: "item-v1",
		Text: " same evidence ", XPostText: "same evidence", OCRText: "   ",
	})
	if len(got.Sections) != 1 || got.Sections[0].Text != "same evidence" {
		t.Fatalf("projection retained blank or duplicate sections: %+v", got.Sections)
	}
}

func assertSections(t *testing.T, got, want []Section) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got sections %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("section %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
