package mastodonapi

import (
	"net/url"
	"strings"
	"testing"
)

func TestParseNextBookmarksCursorRequiresExactOriginPathAndAllowedQuery(t *testing.T) {
	valid := `<https://hachyderm.io/api/v1/bookmarks?limit=40&max_id=abc>; rel="next"`
	got, err := ParseNextBookmarksCursor(valid, "https://hachyderm.io")
	if err != nil {
		t.Fatalf("ParseNextBookmarksCursor(valid): %v", err)
	}
	if got != "https://hachyderm.io/api/v1/bookmarks?limit=40&max_id=abc" {
		t.Fatalf("cursor = %q", got)
	}

	for _, raw := range []string{
		`<https://other.example/api/v1/bookmarks?limit=40&max_id=abc>; rel="next"`,
		`<https://hachyderm.io/api/v1/statuses/1?limit=40>; rel="next"`,
		`<https://hachyderm.io/api/v1/bookmarks?limit=40&token=secret>; rel="next"`,
		`<https://hachyderm.io/api/v1/bookmarks?limit=40&max_id=abc#fragment>; rel="next"`,
	} {
		if _, err := ParseNextBookmarksCursor(raw, "https://hachyderm.io"); err == nil {
			t.Errorf("ParseNextBookmarksCursor(%q) accepted invalid cursor", raw)
		}
	}
}

func TestParseNextBookmarksCursorTreatsCursorAsOpaque(t *testing.T) {
	raw := `<https://hachyderm.io/api/v1/bookmarks?limit=40&since_id=not-a-status-id&min_id=opaque%2Fvalue>; rel=next, <https://hachyderm.io/api/v1/bookmarks?limit=40>; rel="prev"`
	got, err := ParseNextBookmarksCursor(raw, "https://hachyderm.io")
	if err != nil {
		t.Fatalf("ParseNextBookmarksCursor: %v", err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse cursor: %v", err)
	}
	if parsed.Query().Get("min_id") != "opaque/value" || parsed.Query().Get("since_id") != "not-a-status-id" {
		t.Fatalf("cursor query was interpreted or changed: %q", got)
	}
}

func TestParseNextBookmarksCursorRejectsMalformedLinkHeader(t *testing.T) {
	for _, raw := range []string{"", `https://hachyderm.io/api/v1/bookmarks?limit=40`, `<https://hachyderm.io/api/v1/bookmarks?limit=40>; rel="previous"`} {
		got, err := ParseNextBookmarksCursor(raw, "https://hachyderm.io")
		if err != nil {
			if raw == "" || strings.Contains(raw, "previous") || !strings.HasPrefix(raw, "<") {
				continue
			}
			t.Fatalf("ParseNextBookmarksCursor(%q): %v", raw, err)
		}
		if got != "" {
			t.Fatalf("cursor = %q for header %q, want empty", got, raw)
		}
	}
}
