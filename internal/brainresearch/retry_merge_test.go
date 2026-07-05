package brainresearch

import (
	"reflect"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/ask"
)

func TestMergeRetryPackPreservesInitialAnchoredRowsAndRejectsGenericRetry(t *testing.T) {
	t.Parallel()

	initial := retryMergeInitialPack()
	retry := Pack{Evidence: []ask.Evidence{{
		SourceKey:  "src:generic-synthesis",
		Kind:       "source",
		SourceType: "web",
		Title:      "Generic synthesis workflows",
		Summary:    "A generic note about summarization workflows.",
	}}}

	merged, decision := MergeRetryPack(initial, retry, MergeRetryOptions{
		MissingConcepts: []string{"essays"},
		RetryAction:     "focused_retry",
		RetryQuestion:   "@Kristof_Poland essays",
	})

	if got := evidenceSourceKeys(merged.Evidence); !reflect.DeepEqual(got, []string{"x:kristof-1", "x:kristof-2"}) {
		t.Fatalf("expected initial anchored rows to survive without generic retry row, got %v decision=%+v", got, decision)
	}
	if !reflect.DeepEqual(decision.PreservedInitialSourceKeys, []string{"x:kristof-1", "x:kristof-2"}) {
		t.Fatalf("unexpected preserved initial keys: %+v", decision)
	}
	if !reflect.DeepEqual(decision.RejectedRetrySourceKeys, []string{"src:generic-synthesis"}) {
		t.Fatalf("expected generic retry row to be rejected, got %+v", decision)
	}
	if merged.QueryPlan.TextQuery != initial.QueryPlan.TextQuery || merged.Topic != initial.Topic || merged.TopicBrief != initial.TopicBrief || !reflect.DeepEqual(merged.NextSteps, initial.NextSteps) {
		t.Fatalf("expected initial plan/topic fields to be preserved: merged=%+v initial=%+v", merged, initial)
	}
}

func TestMergeRetryPackAcceptsRetryRowsThatFillMissingContent(t *testing.T) {
	t.Parallel()

	initial := retryMergeInitialPack()
	retry := Pack{Evidence: []ask.Evidence{{
		SourceKey:  "x:kristof-essay",
		Kind:       "item",
		SourceType: "x_bookmark",
		Author:     "Krzysztof Szczawinski @Kristof_Poland",
		Title:      "Essays by Kristof_Poland",
		Summary:    "A thread collecting essays and arguments.",
		UserTags:   "Kristof_Poland",
	}}}

	merged, decision := MergeRetryPack(initial, retry, MergeRetryOptions{MissingConcepts: []string{"essays"}, RetryAction: "focused_retry"})
	if got := evidenceSourceKeys(merged.Evidence); !reflect.DeepEqual(got, []string{"x:kristof-1", "x:kristof-2", "x:kristof-essay"}) {
		t.Fatalf("expected accepted anchored retry row after initial rows, got %v decision=%+v", got, decision)
	}
	if !reflect.DeepEqual(decision.AcceptedRetrySourceKeys, []string{"x:kristof-essay"}) {
		t.Fatalf("expected retry row to be accepted, got %+v", decision)
	}
}

func TestMergeRetryPackAcceptsOnlyAnchoredRelatedExpansionRows(t *testing.T) {
	t.Parallel()

	initial := retryMergeInitialPack()
	retry := Pack{Evidence: []ask.Evidence{
		{
			SourceKey:    "src:kristof-related",
			Kind:         "source",
			SourceType:   "web",
			Title:        "Related economic context",
			Summary:      "Context linked from an anchored Kristof row.",
			RelatedTo:    "x:kristof-1",
			Relationship: "linked source",
		},
		{
			SourceKey:    "src:generic-related",
			Kind:         "source",
			SourceType:   "web",
			Title:        "Generic synthesis workflows",
			Summary:      "Generic synthesis content unrelated to the protected author.",
			RelatedTo:    "src:generic-synthesis",
			Relationship: "linked source",
		},
	}}

	merged, decision := MergeRetryPack(initial, retry, MergeRetryOptions{RetryAction: "related_expansion"})
	if got := evidenceSourceKeys(merged.Evidence); !reflect.DeepEqual(got, []string{"x:kristof-1", "x:kristof-2", "src:kristof-related"}) {
		t.Fatalf("expected only anchored related expansion row to merge, got %v decision=%+v", got, decision)
	}
	if !reflect.DeepEqual(decision.AcceptedRetrySourceKeys, []string{"src:kristof-related"}) {
		t.Fatalf("expected anchored related row accepted, got %+v", decision)
	}
	if !reflect.DeepEqual(decision.RejectedRetrySourceKeys, []string{"src:generic-related"}) {
		t.Fatalf("expected unrelated related row rejected, got %+v", decision)
	}
}

func TestMergeRetryPackRecomputesCoverageAndRecallNote(t *testing.T) {
	t.Parallel()

	initial := retryMergeInitialPack()
	retry := Pack{
		Evidence: []ask.Evidence{{
			SourceKey:  "x:kristof-essay",
			Kind:       "item",
			SourceType: "x_bookmark",
			Author:     "Krzysztof Szczawinski @Kristof_Poland",
			Title:      "Essays by Kristof_Poland",
			Summary:    "A thread collecting essays and arguments.",
			UserTags:   "Kristof_Poland, essays",
		}},
		ExactTagEvidence: []ask.Evidence{{SourceKey: "x:tagged", Kind: "item", SourceType: "x_bookmark", UserTags: "Kristof_Poland"}},
	}

	merged, decision := MergeRetryPack(initial, retry, MergeRetryOptions{MissingConcepts: []string{"essays"}, RetryAction: "focused_retry"})
	if len(decision.AcceptedRetrySourceKeys) != 1 {
		t.Fatalf("expected retry acceptance in decision, got %+v", decision)
	}
	if merged.Coverage.EvidenceCount != 3 {
		t.Fatalf("expected merged evidence count 3, got %+v", merged.Coverage)
	}
	if !hasBucket(merged.Coverage.ByKind, "item", 3) || !hasBucket(merged.Coverage.BySourceType, "x_bookmark", 3) {
		t.Fatalf("expected row-derived coverage to be rebuilt, got %+v", merged.Coverage)
	}
	if !hasBucket(merged.Coverage.TopUserTags, "Kristof_Poland", 3) || !hasBucket(merged.Coverage.TopUserTags, "essays", 1) {
		t.Fatalf("expected merged tag coverage, got %+v", merged.Coverage.TopUserTags)
	}
	if !hasBucket(merged.Coverage.ExactTagMatches, "Kristof_Poland", 11) || merged.Coverage.ItemTextMatches != 7 || merged.Coverage.DisplayedLimit != 8 {
		t.Fatalf("expected corpus coverage fields to be preserved, got %+v", merged.Coverage)
	}
	if !strings.Contains(merged.Coverage.RecallNote, "exact user-tag matches") || !strings.Contains(merged.Coverage.RecallNote, "items=7") {
		t.Fatalf("expected recall note to be recomputed from preserved corpus coverage, got %q", merged.Coverage.RecallNote)
	}
	if got := evidenceSourceKeys(merged.ExactTagEvidence); !reflect.DeepEqual(got, []string{"x:initial-tag", "x:tagged"}) {
		t.Fatalf("expected exact-tag evidence to append/dedupe, got %v", got)
	}
}

func TestMergeRetryPackReordersNoAnchorCaseByContentMatch(t *testing.T) {
	t.Parallel()

	initial := retryMergeInitialPack()
	initial.QueryPlan.ProtectedAnchors = nil
	initial.QueryPlan.Concepts = []QueryConcept{{Key: "essays", Preferred: "essays", Terms: []string{"essays", "essay"}, Required: true, Role: "content"}}
	retry := Pack{Evidence: []ask.Evidence{{
		SourceKey:  "src:essay-match",
		Kind:       "source",
		SourceType: "web",
		Title:      "Essays about the target topic",
		Summary:    "A direct essay match.",
	}}}

	merged, decision := MergeRetryPack(initial, retry, MergeRetryOptions{MissingConcepts: []string{"essays"}, RetryAction: "focused_retry"})
	if got := evidenceSourceKeys(merged.Evidence); !reflect.DeepEqual(got, []string{"src:essay-match", "x:kristof-1", "x:kristof-2"}) {
		t.Fatalf("expected accepted retry row to rank first without protected anchors while preserving initial rows, got %v decision=%+v", got, decision)
	}
}

func retryMergeInitialPack() Pack {
	return Pack{
		SchemaVersion: SchemaVersion,
		Question:      "Can you synthesize Kristof_Poland essays?",
		Mode:          "evidence_only",
		QueryPlan: QueryPlan{
			TextQuery: "Kristof_Poland essays",
			ProtectedAnchors: []ProtectedAnchor{{
				Kind:        "handle",
				Relation:    "authored_by",
				Raw:         "@Kristof_Poland",
				Canonical:   "kristof_poland",
				ExactTerms:  []string{"@Kristof_Poland", "Kristof_Poland", "kristof_poland"},
				PhraseTerms: []string{"kristof poland"},
			}},
			Concepts: []QueryConcept{
				{Key: "kristof_poland", Preferred: "@Kristof_Poland", Terms: []string{"@Kristof_Poland", "Kristof_Poland", "kristof_poland"}, Required: true, Role: "anchor"},
				{Key: "essays", Preferred: "essays", Terms: []string{"essays", "essay"}, Required: true, Role: "content"},
			},
		},
		Topic:          "kristof-polish-topic",
		UsedTopicBrief: true,
		TopicBrief:     &TopicBrief{Topic: "kristof-polish-topic", Summary: "topic summary"},
		NextSteps:      []SuggestedAction{{Action: "follow_up", Label: "Inspect more Kristof rows"}},
		Coverage: Coverage{
			EvidenceCount:     2,
			ByKind:            []Bucket{{Key: "item", Count: 2}},
			BySourceType:      []Bucket{{Key: "x_bookmark", Count: 2}},
			TopUserTags:       []Bucket{{Key: "Kristof_Poland", Count: 2}},
			ExactTagMatches:   []Bucket{{Key: "Kristof_Poland", Count: 11}},
			ItemTextMatches:   7,
			SourceTextMatches: 3,
			DisplayedLimit:    8,
			RelatedLimit:      2,
			RecallNote:        "old note",
		},
		Evidence: []ask.Evidence{
			{SourceKey: "x:kristof-1", Kind: "item", SourceType: "x_bookmark", Author: "Krzysztof Szczawinski @Kristof_Poland", Title: "Road to Serfdom", Summary: "Economics thread.", UserTags: "Kristof_Poland"},
			{SourceKey: "x:kristof-2", Kind: "item", SourceType: "x_bookmark", Author: "Krzysztof Szczawinski @Kristof_Poland", Title: "Atlas Shrugged", Summary: "Political thread.", UserTags: "Kristof_Poland"},
		},
		ExactTagEvidence: []ask.Evidence{{SourceKey: "x:initial-tag", Kind: "item", SourceType: "x_bookmark", UserTags: "Kristof_Poland"}},
	}
}

func hasBucket(buckets []Bucket, key string, count int) bool {
	for _, bucket := range buckets {
		if bucket.Key == key && bucket.Count == count {
			return true
		}
	}
	return false
}
