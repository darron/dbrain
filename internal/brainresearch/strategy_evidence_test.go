package brainresearch

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/researchsemantic"
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/semanticindex"
)

type fakeSemanticRetriever struct {
	rows    []retrieval.EvidenceDocument
	status  semanticindex.Status
	err     error
	queries []string
	options []researchsemantic.Options
}

func (f *fakeSemanticRetriever) Retrieve(_ context.Context, q string, opts researchsemantic.Options) ([]retrieval.EvidenceDocument, semanticindex.Status, error) {
	f.queries = append(f.queries, q)
	f.options = append(f.options, opts)
	return append([]retrieval.EvidenceDocument(nil), f.rows...), f.status, f.err
}

func TestCanonicalSemanticQueryUsesPreferredTermsKeysAndFallbacks(t *testing.T) {
	strategy := researchStrategy{Concepts: []QueryConcept{
		{Key: "knowledge_base", Preferred: "Knowledge Base", Role: "context"},
		{Key: "ignored", Preferred: "frame", Role: conceptRoleFrame},
		{Key: "intent", Preferred: "lookup", Role: conceptRoleIntent},
		{Key: "context", Preferred: "Context Term", Role: "context"},
		{Key: "anchor-key", Preferred: " Alpha ", Role: "anchor"},
		{Key: "term-key", Terms: []string{"", "Beta"}, Role: "content"},
		{Key: "Gamma", Terms: nil, Role: "content"},
		{Key: "duplicate", Preferred: "alpha", Role: "content"},
	}}
	if got := canonicalSemanticQuery(strategy, ask.QueryHints{TextQuery: "hint"}, "question"); got != "Alpha Beta Gamma" {
		t.Fatalf("got %q", got)
	}
	if got := canonicalSemanticQuery(researchStrategy{}, ask.QueryHints{TextQuery: "  normalized hint  "}, "question"); got != "normalized hint" {
		t.Fatalf("got %q", got)
	}
	if got := canonicalSemanticQuery(researchStrategy{}, ask.QueryHints{}, "  cleaned   question "); got != "cleaned question" {
		t.Fatalf("got %q", got)
	}
}

func TestIntersectSemanticSourceTypesPreservesConfiguredPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		configured, requested, want []string
		compatible                  bool
	}{
		{"family narrows", []string{"x"}, []string{"x_bookmark"}, []string{"x_bookmark"}, true},
		{"request family", []string{"x_quote"}, []string{"x"}, []string{"x_quote"}, true},
		{"empty request preserves configured", []string{"article"}, nil, []string{"article"}, true},
		{"empty configured uses request", nil, []string{"x"}, []string{"x"}, true},
		{"disjoint denies all", []string{"article"}, []string{"x"}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, compatible := intersectSemanticSourceTypes(tc.configured, tc.requested)
			if !reflect.DeepEqual(got, tc.want) || compatible != tc.compatible {
				t.Fatalf("got=%v compatible=%t want=%v compatible=%t", got, compatible, tc.want, tc.compatible)
			}
		})
	}
}

func TestCollectStrategyEvidenceDisjointSemanticFiltersSkipPoolAndRetriever(t *testing.T) {
	_, st := inspectionTestStore(t)
	fake := &fakeSemanticRetriever{}
	b := New(config.Config{}, st).WithSemanticRetriever(fake, researchsemantic.Options{Filters: semanticindex.Filters{AllowedSourceTypes: []string{"article"}}})
	poolCalls := 0
	b.strategyPoolRunner = func(context.Context, string, ask.Options, int) (ask.Response, ask.Response, error) {
		poolCalls++
		return ask.Response{}, ask.Response{}, nil
	}
	got, err := b.collectStrategyEvidence(context.Background(), researchStrategy{Variants: []QueryVariant{{Query: "one"}}}, ask.QueryHints{}, Options{Question: "one", UseSemantic: true, SourceTypes: []string{"x"}}, 1, 100, nil)
	if err != nil || got == nil || poolCalls != 0 || len(fake.queries) != 0 {
		t.Fatalf("got=%+v err=%v pool=%d semantic=%d", got, err, poolCalls, len(fake.queries))
	}
}

func TestBuildCoverageCountsUniqueParents(t *testing.T) {
	evidence := []ask.Evidence{
		{SourceKey: "p", Kind: "item", SourceType: "x_bookmark", UserTags: "one", Chunk: &retrieval.EvidenceChunk{ID: "a", ParentSourceKey: "p"}},
		{SourceKey: "p", Kind: "item", SourceType: "x_bookmark", UserTags: "two", Chunk: &retrieval.EvidenceChunk{ID: "b", ParentSourceKey: "p"}},
	}
	got := buildCoverage(evidence)
	if got.EvidenceCount != 1 || len(got.ByKind) != 1 || got.ByKind[0].Count != 1 || len(got.BySourceType) != 1 || got.BySourceType[0].Count != 1 || len(got.TopUserTags) != 1 || got.TopUserTags[0].Key != "one" {
		t.Fatalf("coverage=%+v", got)
	}
}

func TestCollectStrategyEvidenceCallsSemanticOnceWithCanonicalQueryAndBoundOptions(t *testing.T) {
	_, st := inspectionTestStore(t)
	fake := &fakeSemanticRetriever{status: semanticindex.Status{State: semanticindex.StateSearched}}
	b := New(config.Config{}, st).WithSemanticRetriever(fake, researchsemantic.Options{Limit: 37, MaxChunks: 123, Filters: semanticindex.Filters{AllowedParentKinds: []string{"item"}, AllowedSourceTypes: []string{"x"}}})
	strategy := researchStrategy{Variants: []QueryVariant{{Query: "one"}, {Query: "two"}}, Concepts: []QueryConcept{{Key: "alpha", Preferred: "Alpha", Role: conceptRoleAnchor}, {Key: "beta", Terms: []string{"Beta"}, Role: conceptRoleContent}}}
	got, err := b.collectStrategyEvidence(context.Background(), strategy, ask.QueryHints{TextQuery: "fallback"}, Options{Question: "question", UseSemantic: true, SourceTypes: []string{"x_bookmark"}}, 5, 100, nil)
	if err != nil || got == nil || len(fake.queries) != 1 || fake.queries[0] != "Alpha Beta" || fake.options[0].Limit != 37 || fake.options[0].MaxChunks != 123 || !reflect.DeepEqual(fake.options[0].Filters.AllowedParentKinds, []string{"item"}) || !reflect.DeepEqual(fake.options[0].Filters.AllowedSourceTypes, []string{"x_bookmark"}) {
		t.Fatalf("got=%+v err=%v queries=%+v options=%+v", got, err, fake.queries, fake.options)
	}
}

func TestCollectStrategyEvidenceDeepFailureFailsOpenAndCancellationPropagates(t *testing.T) {
	_, st := inspectionTestStore(t)
	baseline := ask.Evidence{SourceKey: "legacy", Retrieval: &ask.RetrievalInfo{Score: 7}}
	semantic := chunkEvidence("semantic", "chunk")
	makeBuilder := func(deepErr error) *Builder {
		b := New(config.Config{}, st).WithSemanticRetriever(&fakeSemanticRetriever{rows: []retrieval.EvidenceDocument{semantic}, status: semanticindex.Status{State: semanticindex.StateSearched}}, researchsemantic.Options{})
		b.strategyPoolRunner = func(_ context.Context, _ string, _ ask.Options, _ int) (ask.Response, ask.Response, error) {
			return ask.Response{}, ask.Response{}, deepErr
		}
		b.strategyRunner = func(_ context.Context, _ string, _ ask.Options) (ask.Response, error) {
			return ask.Response{Evidence: []ask.Evidence{baseline}}, nil
		}
		return b
	}
	strategy := researchStrategy{Variants: []QueryVariant{{Query: "one"}}}
	opts := Options{Question: "one", UseSemantic: true}
	got, err := makeBuilder(errors.New("deep offline")).collectStrategyEvidence(context.Background(), strategy, ask.QueryHints{}, opts, 1, 100, nil)
	if err != nil || !reflect.DeepEqual(got, []ask.Evidence{baseline}) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	_, err = makeBuilder(context.Canceled).collectStrategyEvidence(context.Background(), strategy, ask.QueryHints{}, opts, 1, 100, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestCollectStrategyEvidenceSuccessfulSemanticUsesOneCandidatePoolCallPerVariant(t *testing.T) {
	_, st := inspectionTestStore(t)
	fake := &fakeSemanticRetriever{rows: []retrieval.EvidenceDocument{chunkEvidence("semantic", "chunk")}, status: semanticindex.Status{State: semanticindex.StateSearched}}
	b := New(config.Config{}, st).WithSemanticRetriever(fake, researchsemantic.Options{})
	calls := 0
	b.strategyPoolRunner = func(_ context.Context, q string, _ ask.Options, _ int) (ask.Response, ask.Response, error) {
		calls++
		legacy := ask.Response{Evidence: []ask.Evidence{{SourceKey: "legacy:" + q}}}
		deep := ask.Response{Evidence: []ask.Evidence{{SourceKey: "deep:" + q}}}
		return legacy, deep, nil
	}
	strategy := researchStrategy{Variants: []QueryVariant{{Query: "one"}, {Query: "two"}}}
	_, err := b.collectStrategyEvidence(context.Background(), strategy, ask.QueryHints{}, Options{Question: "question", UseSemantic: true}, 5, 100, nil)
	if err != nil || calls != 2 || len(fake.queries) != 1 {
		t.Fatalf("err=%v pool_calls=%d semantic_calls=%d", err, calls, len(fake.queries))
	}
}

func TestCollectStrategyEvidenceReturnsLegacyFallbackErrorAfterPoolFailure(t *testing.T) {
	_, st := inspectionTestStore(t)
	poolErr := errors.New("pool failed")
	legacyErr := errors.New("legacy failed")
	b := New(config.Config{}, st).WithSemanticRetriever(&fakeSemanticRetriever{}, researchsemantic.Options{})
	b.strategyPoolRunner = func(context.Context, string, ask.Options, int) (ask.Response, ask.Response, error) {
		return ask.Response{}, ask.Response{}, poolErr
	}
	b.strategyRunner = func(context.Context, string, ask.Options) (ask.Response, error) { return ask.Response{}, legacyErr }
	_, err := b.collectStrategyEvidence(context.Background(), researchStrategy{Variants: []QueryVariant{{Query: "one"}}}, ask.QueryHints{}, Options{Question: "one", UseSemantic: true}, 1, 100, nil)
	if !errors.Is(err, legacyErr) {
		t.Fatalf("err=%v want=%v", err, legacyErr)
	}
}

func chunkEvidence(parent, id string) ask.Evidence {
	return ask.Evidence{SourceKey: parent, Excerpt: id, EvidenceRole: "raw", Chunk: &retrieval.EvidenceChunk{ID: id, ParentSourceKey: parent, SectionOrdinal: 1}}
}

func TestCollectStrategyEvidenceSemanticFailureModesFailOpenAndCancellationPropagates(t *testing.T) {
	_, st := inspectionTestStore(t)
	strategy := researchStrategy{Variants: []QueryVariant{{Query: "one"}, {Query: "two"}}}
	hints := ask.QueryHints{TextQuery: "fallback"}
	opts := Options{Question: "question", UseSemantic: true}
	for _, tc := range []struct {
		name   string
		status semanticindex.Status
		err    error
	}{{"searched empty", semanticindex.Status{State: semanticindex.StateSearched}, nil}, {"unavailable", semanticindex.Status{State: semanticindex.StateUnavailable, Reason: semanticindex.ReasonTooLarge}, nil}, {"error", semanticindex.Status{}, errors.New("offline")}} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSemanticRetriever{status: tc.status, err: tc.err}
			got, err := New(config.Config{}, st).WithSemanticRetriever(fake, researchsemantic.Options{}).collectStrategyEvidence(context.Background(), strategy, hints, opts, 5, 100, nil)
			if err != nil || got == nil || len(fake.queries) != 1 {
				t.Fatalf("got=%+v err=%v calls=%d", got, err, len(fake.queries))
			}
		})
	}
	fake := &fakeSemanticRetriever{err: context.Canceled}
	_, err := New(config.Config{}, st).WithSemanticRetriever(fake, researchsemantic.Options{}).collectStrategyEvidence(context.Background(), strategy, hints, opts, 5, 100, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
