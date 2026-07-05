package brainresearch

import (
	"testing"

	"github.com/darron/dbrain/internal/ask"
)

func TestEvidenceMatchesProtectedAnchorFromAuthorMetadata(t *testing.T) {
	t.Parallel()

	row := ask.Evidence{
		SourceKey: "x:kristof-author",
		Title:     "The Road to Serfdom made simple",
		Author:    "Krzysztof Szczawinski @Kristof_Poland",
		Summary:   "A thread explaining economic planning.",
	}
	if !EvidenceMatchesProtectedAnchor(row, anchorFromHandle("@Kristof_Poland", "current_user_text")) {
		t.Fatalf("expected author metadata to prove protected anchor match")
	}
}

func TestEvidenceDoesNotMatchProtectedAnchorByExpansionTermsOnly(t *testing.T) {
	t.Parallel()

	row := ask.Evidence{
		SourceKey: "src:poland-economy",
		Title:     "Poland economy notes",
		Summary:   "Trade and inflation notes without the target author.",
	}
	if EvidenceMatchesProtectedAnchor(row, anchorFromHandle("@Kristof_Poland", "current_user_text")) {
		t.Fatalf("expected expansion term alone to be insufficient for protected anchor match")
	}
}

func TestEvidenceMatchesSourceKeyAnchorExactly(t *testing.T) {
	t.Parallel()

	row := ask.Evidence{SourceKey: "x:2071948517837353292"}
	if !EvidenceMatchesProtectedAnchor(row, anchorFromSourceKey("x:2071948517837353292", "current_user_text")) {
		t.Fatalf("expected exact source key anchor to match evidence source key")
	}
	feedEntry := ask.Evidence{SourceKey: "feed-entry:abc123def456"}
	if !EvidenceMatchesProtectedAnchor(feedEntry, anchorFromSourceKey("feed-entry:abc123def456", "current_user_text")) {
		t.Fatalf("expected exact feed-entry source key anchor to match evidence source key")
	}
}
