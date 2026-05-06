package retrieval

import (
	"testing"
	"time"
)

func TestNewContentSectionTrimsCountsAndTruncates(t *testing.T) {
	at := time.Date(2026, 5, 5, 12, 0, 0, 0, time.FixedZone("test", -6*60*60))
	section := NewContentSection("summary_text", "derived", " ok ", " model ", " tool ", at, " alpha beta ", 5)

	if section.Name != "summary_text" || section.Role != "derived" {
		t.Fatalf("unexpected identity: %#v", section)
	}
	if section.Status != "ok" || section.Model != "model" || section.Tool != "tool" {
		t.Fatalf("expected trimmed metadata, got %#v", section)
	}
	if section.At != "2026-05-05T18:00:00Z" {
		t.Fatalf("expected UTC timestamp, got %q", section.At)
	}
	if section.Chars != len([]rune("alpha beta")) {
		t.Fatalf("expected original trimmed char count, got %d", section.Chars)
	}
	if section.Text != "alpha" || !section.Truncated {
		t.Fatalf("expected truncated text, got text=%q truncated=%v", section.Text, section.Truncated)
	}
}

func TestAppendUniqueContentSectionSkipsBlankAndDuplicateText(t *testing.T) {
	var sections []ContentSection
	AppendUniqueContentSection(&sections, ContentSection{Name: "blank", Text: " "})
	AppendUniqueContentSection(&sections, ContentSection{Name: "one", Text: "same"})
	AppendUniqueContentSection(&sections, ContentSection{Name: "two", Text: "same"})

	if len(sections) != 1 {
		t.Fatalf("expected one unique nonblank section, got %d", len(sections))
	}
	if sections[0].Name != "one" {
		t.Fatalf("expected first unique section to survive, got %#v", sections[0])
	}
}

func TestContentSectionCatalogClearsPayloadText(t *testing.T) {
	catalog := ContentSectionCatalog([]ContentSection{{
		Name:      "summary_text",
		Role:      "derived",
		Chars:     10,
		Text:      "payload",
		Truncated: true,
	}})
	if len(catalog) != 1 {
		t.Fatalf("expected one catalog entry, got %d", len(catalog))
	}
	if catalog[0].Text != "" || catalog[0].Truncated {
		t.Fatalf("expected text and truncation to be cleared, got %#v", catalog[0])
	}
	if catalog[0].Chars != 10 {
		t.Fatalf("expected metadata to survive, got %#v", catalog[0])
	}
}
