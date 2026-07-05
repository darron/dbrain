package brainresearch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

type fakeAnchorResolver struct {
	anchors []ProtectedAnchor
	err     error
}

func (f fakeAnchorResolver) ResolveAnchors(ctx context.Context, anchors []ProtectedAnchor) ([]ProtectedAnchor, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.anchors != nil {
		return f.anchors, nil
	}
	return anchors, nil
}

func TestExtractProtectedAnchorsPreservesHandlesAndUnderscoreAliases(t *testing.T) {
	t.Parallel()

	anchors := extractProtectedAnchors("Can you synthesize the Tweets from @Kristof_Poland - they're in the dbrain. Also try Kristof_Poland.")
	handle := findAnchorByCanonical(anchors, "kristof_poland")
	if handle == nil {
		t.Fatalf("expected kristof_poland anchor, got %#v", anchors)
	}
	if got := countAnchorsByCanonicalAndRelation(anchors, "kristof_poland", "authored_by"); got != 1 {
		t.Fatalf("expected handle and bare alias to dedupe into one authored_by anchor, got %d anchors in %#v", got, anchors)
	}
	if handle.Kind != "handle" || handle.Relation != "authored_by" {
		t.Fatalf("unexpected handle anchor kind/relation: %#v", handle)
	}
	if !containsString(handle.ExactTerms, "@Kristof_Poland") ||
		!containsString(handle.ExactTerms, "Kristof_Poland") ||
		!containsString(handle.ExactTerms, "kristof_poland") {
		t.Fatalf("expected exact handle terms to preserve raw and canonical aliases, got %#v", handle.ExactTerms)
	}
	if !containsString(handle.PhraseTerms, "kristof poland") {
		t.Fatalf("expected phrase alias, got %#v", handle.PhraseTerms)
	}
	if !containsString(handle.ExpansionTerms, "kristof") || !containsString(handle.ExpansionTerms, "poland") {
		t.Fatalf("expected expansion terms for recall only, got %#v", handle.ExpansionTerms)
	}
}

func TestExtractProtectedAnchorsRejectsEmailAndCodeIdentifiers(t *testing.T) {
	t.Parallel()

	input := "Email bob@example.com. Ignore user_id, created_at, max_retries, `snake_case` in code, and notes from my_code_review folder."
	anchors := extractProtectedAnchors(input)
	if len(anchors) != 0 {
		t.Fatalf("expected no anchors from email/code identifiers, got %#v", anchors)
	}
	if HasCurrentProtectedAnchor(input) {
		t.Fatalf("expected exported anchor predicate to reject email/code identifiers")
	}
}

func TestExtractProtectedAnchorsRejectsTechnicalUnderscoreInSynthesisQuestion(t *testing.T) {
	t.Parallel()

	input := "Synthesize notes about api_gateway configuration"
	if anchors := extractProtectedAnchors(input); len(anchors) != 0 {
		t.Fatalf("expected no anchors from technical underscore identifier, got %#v", anchors)
	}
	if HasCurrentProtectedAnchor(input) {
		t.Fatalf("expected exported anchor predicate to reject technical underscore identifier")
	}
}

func TestSourceKeyCandidatesRemainProtectedAnchors(t *testing.T) {
	t.Parallel()

	anchors := extractProtectedAnchors("Compare x:2071948517837353292 with src:1212afd25440 and feed-entry:abc123def456.")
	xAnchor := findAnchorByRaw(anchors, "x:2071948517837353292")
	if xAnchor == nil || xAnchor.Kind != "source_key" || !containsString(xAnchor.ExactTerms, "x:2071948517837353292") {
		t.Fatalf("expected exact x source-key anchor, got anchors=%#v anchor=%#v", anchors, xAnchor)
	}
	srcAnchor := findAnchorByRaw(anchors, "src:1212afd25440")
	if srcAnchor == nil || srcAnchor.Kind != "source_key" || !containsString(srcAnchor.ExactTerms, "src:1212afd25440") {
		t.Fatalf("expected exact src source-key anchor, got anchors=%#v anchor=%#v", anchors, srcAnchor)
	}
	feedAnchor := findAnchorByRaw(anchors, "feed-entry:abc123def456")
	if feedAnchor == nil || feedAnchor.Kind != "source_key" || !containsString(feedAnchor.ExactTerms, "feed-entry:abc123def456") {
		t.Fatalf("expected exact feed-entry source-key anchor, got anchors=%#v anchor=%#v", anchors, feedAnchor)
	}
}

func TestSourceKeyCandidatesRejectMarkdownURLsCodeBlocksAndLookalikes(t *testing.T) {
	t.Parallel()

	input := "Use x:2071948517837353292), [src:1212afd25440](https://example.com/src:deadbeef), and `x:2071948517837353292`; ignore 0xdeadbeef and 20260704T174350."
	anchors := extractProtectedAnchors(input)
	if findAnchorByRaw(anchors, "x:2071948517837353292") == nil {
		t.Fatalf("expected punctuation/code-spanned x source key without punctuation, got %#v", anchors)
	}
	if findAnchorByRaw(anchors, "src:1212afd25440") == nil {
		t.Fatalf("expected markdown label src source key, got %#v", anchors)
	}
	for _, anchor := range anchors {
		if strings.Contains(anchor.Raw, "deadbeef") || strings.Contains(anchor.Raw, "20260704T174350") || strings.Contains(anchor.Raw, ")") {
			t.Fatalf("source-key parser accepted lookalike or punctuation: %#v", anchors)
		}
	}
}

func TestExtractProtectedAnchorsFromHashtagDisplayNameAndCollection(t *testing.T) {
	t.Parallel()

	raw := extractProtectedAnchors("Synthesize #Kristof_Poland, #kristof-poland, posts by Vitalik Buterin, and the Tyler Cowen collection.")
	if findAnchorByCanonical(raw, "kristof_poland") == nil {
		t.Fatalf("expected hashtag alias candidate for Kristof_Poland, got %#v", raw)
	}

	resolved, err := fakeAnchorResolver{anchors: []ProtectedAnchor{
		{
			Kind:       "tag_alias",
			Relation:   "tag",
			Raw:        "#Kristof_Poland",
			Canonical:  "kristof_poland",
			ResolvedID: "tag:kristof-poland",
			Source:     "current_user_text",
			Confidence: "alias",
			ExactTerms: []string{"#Kristof_Poland", "Kristof_Poland", "kristof_poland", "kristof-poland"},
		},
		{
			Kind:        "entity_alias",
			Relation:    "authored_by",
			Raw:         "Vitalik Buterin",
			Canonical:   "vitalik buterin",
			ResolvedID:  "x-author:vitalikbuterin",
			Source:      "current_user_text",
			Confidence:  "alias",
			ExactTerms:  []string{"Vitalik Buterin"},
			PhraseTerms: []string{"vitalik buterin"},
		},
		{
			Kind:        "collection",
			Relation:    "collection",
			Raw:         "Tyler Cowen collection",
			Canonical:   "tyler cowen",
			ResolvedID:  "collection:tyler-cowen",
			Source:      "current_user_text",
			Confidence:  "alias",
			ExactTerms:  []string{"Tyler Cowen collection"},
			PhraseTerms: []string{"tyler cowen"},
		},
	}}.ResolveAnchors(context.Background(), raw)
	if err != nil {
		t.Fatalf("fake resolver: %v", err)
	}
	for _, want := range []string{"tag:kristof-poland", "x-author:vitalikbuterin", "collection:tyler-cowen"} {
		if findAnchorByResolvedID(resolved, want) == nil {
			t.Fatalf("expected resolved anchor %s, got %#v", want, resolved)
		}
	}
}

func TestAnchorResolverEnrichesKnownXAuthorEntity(t *testing.T) {
	t.Parallel()

	st, err := store.Open(t.TempDir() + "/brain.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	now := time.Now().UTC()
	if _, err := st.UpsertItem(context.Background(), model.Item{
		SourceKey:    "x:kristof-poland-anchor",
		SourceType:   "x_bookmark",
		ExternalID:   "2071948517837353292",
		CanonicalURL: "https://x.com/Kristof_Poland/status/2071948517837353292",
		Title:        "The Road to Serfdom made simple",
		AuthorHandle: "Kristof_Poland",
		AuthorName:   "Krzysztof Szczawinski",
		Text:         "The Road to Serfdom made simple.",
		ContentHash:  "kristof-polish-anchor-hash",
		NotePath:     "items/x/kristof-polish-anchor.md",
		RawJSON:      `{}`,
		ImportedAt:   now,
		UpdatedAt:    now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("upsert item: %v", err)
	}

	anchors := []ProtectedAnchor{anchorFromHandle("@Kristof_Poland", "current_user_text")}
	resolved, err := (storeAnchorResolver{st: st}).ResolveAnchors(context.Background(), anchors)
	if err != nil {
		t.Fatalf("resolve anchors: %v", err)
	}
	got := findAnchorByCanonical(resolved, "kristof_poland")
	if got == nil {
		t.Fatalf("expected resolved kristof anchor, got %#v", resolved)
	}
	if got.ResolvedID != "x-author:kristof_poland" {
		t.Fatalf("ResolvedID=%q, want x-author:kristof_poland; anchor=%#v", got.ResolvedID, got)
	}
	if !containsString(got.ExactTerms, "@Kristof_Poland") || !containsString(got.ExactTerms, "Kristof_Poland") {
		t.Fatalf("expected resolved alias terms to keep handle aliases, got %#v", got.ExactTerms)
	}
}

func TestAnchorResolverKeepsRawAnchorWhenResolutionFails(t *testing.T) {
	t.Parallel()

	raw := []ProtectedAnchor{anchorFromHandle("@Kristof_Poland", "current_user_text")}
	b := &Builder{}
	resolved := b.resolveProtectedAnchors(context.Background(), raw, Options{
		AnchorResolver: fakeAnchorResolver{err: errors.New("resolver unavailable")},
	})
	if len(resolved) != 1 || resolved[0].Raw != "@Kristof_Poland" || resolved[0].ResolvedID != "" {
		t.Fatalf("expected raw unresolved anchor to be preserved, got %#v", resolved)
	}
}

func findAnchorByCanonical(anchors []ProtectedAnchor, canonical string) *ProtectedAnchor {
	for i := range anchors {
		if anchors[i].Canonical == canonical {
			return &anchors[i]
		}
	}
	return nil
}

func findAnchorByRaw(anchors []ProtectedAnchor, raw string) *ProtectedAnchor {
	for i := range anchors {
		if anchors[i].Raw == raw {
			return &anchors[i]
		}
	}
	return nil
}

func findAnchorByResolvedID(anchors []ProtectedAnchor, resolvedID string) *ProtectedAnchor {
	for i := range anchors {
		if anchors[i].ResolvedID == resolvedID {
			return &anchors[i]
		}
	}
	return nil
}

func countAnchorsByCanonicalAndRelation(anchors []ProtectedAnchor, canonical string, relation string) int {
	count := 0
	for _, anchor := range anchors {
		if anchor.Canonical == canonical && anchor.Relation == relation {
			count++
		}
	}
	return count
}
