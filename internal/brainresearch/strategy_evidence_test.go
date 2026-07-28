package brainresearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/researchhybrid"
	"github.com/darron/dbrain/internal/researchsemantic"
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticreadiness"
)

type fakeSemanticRetriever struct {
	rows    []retrieval.EvidenceDocument
	status  semanticindex.Status
	err     error
	queries []string
	options []researchsemantic.Options
	closes  int
}

func TestCatchingUpPreservesFirstThreeDistinctLexicalParents(t *testing.T) {
	_, st := inspectionTestStore(t)
	lexical := []ask.Evidence{
		{SourceKey: "lexical:first", Excerpt: "one"},
		{SourceKey: "lexical:second", Excerpt: "two"},
		{SourceKey: "lexical:third", Excerpt: "three"},
		{SourceKey: "lexical:fourth", Excerpt: "four"},
	}
	fake := &fakeSemanticRetriever{rows: []retrieval.EvidenceDocument{
		chunkEvidence("semantic:a", "chunk:a"), chunkEvidence("semantic:b", "chunk:b"), chunkEvidence("semantic:c", "chunk:c"),
	}, status: semanticindex.Status{State: semanticindex.StateSearched}}
	b := New(config.Config{}, st).WithSemanticMode(semanticconfig.ModeOn).WithSemanticRetriever(fake, researchsemantic.Options{})
	b.semanticReadiness = semanticreadiness.Decision{State: semanticreadiness.StateCatchingUp, Reason: "bounded debt", Searchable: true}
	b.strategyPoolRunner = func(context.Context, string, ask.Options, int) (ask.Response, ask.Response, error) {
		return ask.Response{Evidence: append([]ask.Evidence(nil), lexical...)}, ask.Response{Evidence: append([]ask.Evidence(nil), lexical...)}, nil
	}
	got, err := b.collectStrategyEvidence(context.Background(), researchStrategy{Variants: []QueryVariant{{Query: "q"}}}, ask.QueryHints{}, Options{Question: "q"}, 3, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range got {
		seen[row.SourceKey] = true
	}
	for _, key := range []string{"lexical:first", "lexical:second", "lexical:third"} {
		if !seen[key] {
			t.Fatalf("catching-up result displaced protected lexical parent %q: %#v", key, got)
		}
	}
	if b.semanticStatus == nil || string(b.semanticStatus.Reason) != string(semanticreadiness.StateCatchingUp) {
		t.Fatalf("catching-up semantic status=%+v", b.semanticStatus)
	}
}

func TestPreserveLeadingLexicalParentsUsesKindAndSourceKeyIdentity(t *testing.T) {
	lexical := []ask.Evidence{
		{Kind: "item", SourceKey: "shared:key", Excerpt: "item", Chunk: &retrieval.EvidenceChunk{ParentSourceKey: "shared:key"}},
		{Kind: "source", SourceKey: "shared:key", Excerpt: "source", Chunk: &retrieval.EvidenceChunk{ParentSourceKey: "shared:key"}},
		{Kind: "item", SourceKey: "lexical:no-chunk", Excerpt: "fallback"},
	}
	fused := []ask.Evidence{
		{Kind: "item", SourceKey: "semantic:a", Excerpt: "a", Chunk: &retrieval.EvidenceChunk{ParentSourceKey: "semantic:a"}},
		{Kind: "item", SourceKey: "semantic:b", Excerpt: "b", Chunk: &retrieval.EvidenceChunk{ParentSourceKey: "semantic:b"}},
		{Kind: "item", SourceKey: "semantic:c", Excerpt: "c", Chunk: &retrieval.EvidenceChunk{ParentSourceKey: "semantic:c"}},
	}

	got := preserveLeadingLexicalParents(fused, lexical, 3)
	seen := make(map[string]bool, len(got))
	for _, row := range got {
		seen[row.Kind+"\x00"+row.SourceKey] = true
	}
	for _, want := range []string{"item\x00shared:key", "source\x00shared:key", "item\x00lexical:no-chunk"} {
		if !seen[want] {
			t.Fatalf("missing protected lexical parent %q from %#v", want, got)
		}
	}
}

func TestIneligibleReadinessIsLexicalByteEquivalentAndDoesNotQuerySemantic(t *testing.T) {
	_, st := inspectionTestStore(t)
	lexical := []ask.Evidence{{SourceKey: "lexical:a", Excerpt: "alpha"}, {SourceKey: "lexical:b", Excerpt: "beta"}}
	build := func(mode semanticconfig.Mode, decision semanticreadiness.Decision) ([]ask.Evidence, *ShadowComparison, error) {
		b := New(config.Config{}, st).WithSemanticMode(mode)
		b.semanticReadiness = decision
		b.strategyRunner = func(context.Context, string, ask.Options) (ask.Response, error) {
			return ask.Response{Evidence: append([]ask.Evidence(nil), lexical...)}, nil
		}
		got, err := b.collectStrategyEvidence(context.Background(), researchStrategy{Variants: []QueryVariant{{Query: "q"}}}, ask.QueryHints{}, Options{Question: "q"}, 2, 100, nil)
		return got, b.shadowComparison, err
	}
	off, _, err := build(semanticconfig.ModeOff, semanticreadiness.Decision{State: semanticreadiness.StateDisabled})
	if err != nil {
		t.Fatal(err)
	}
	ineligible, _, err := build(semanticconfig.ModeOn, semanticreadiness.Decision{State: semanticreadiness.StateNeedsIndex, Reason: "exact cap exceeded"})
	if err != nil || !reflect.DeepEqual(ineligible, off) {
		t.Fatalf("ineligible=%#v off=%#v err=%v", ineligible, off, err)
	}
}

type blockingSemanticRetriever struct{}

func (blockingSemanticRetriever) Retrieve(ctx context.Context, _ string, _ researchsemantic.Options) ([]retrieval.EvidenceDocument, semanticindex.Status, error) {
	<-ctx.Done()
	return nil, semanticindex.Status{State: semanticindex.StateUnavailable, Reason: semanticindex.ReasonCanceled}, ctx.Err()
}

func (f *fakeSemanticRetriever) Retrieve(_ context.Context, q string, opts researchsemantic.Options) ([]retrieval.EvidenceDocument, semanticindex.Status, error) {
	f.queries = append(f.queries, q)
	f.options = append(f.options, opts)
	return append([]retrieval.EvidenceDocument(nil), f.rows...), f.status, f.err
}

func (f *fakeSemanticRetriever) Close() error {
	f.closes++
	return nil
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

func TestShadowReturnsExactLexicalEvidenceAndBoundedContentFreeComparison(t *testing.T) {
	_, st := inspectionTestStore(t)
	legacy := []ask.Evidence{{SourceKey: "legacy:b", Excerpt: "private lexical b"}, {SourceKey: "legacy:a", Excerpt: "private lexical a"}}
	semanticRows := []retrieval.EvidenceDocument{chunkEvidence("semantic:new", "chunk:new")}
	build := func(mode semanticconfig.Mode) (Pack, int, int, error) {
		fake := &fakeSemanticRetriever{rows: semanticRows, status: semanticindex.Status{State: semanticindex.StateSearched}}
		b := New(config.Config{}, st).WithSemanticMode(mode).WithSemanticRetriever(fake, researchsemantic.Options{})
		poolCalls := 0
		lexicalCalls := 0
		b.strategyPoolRunner = func(context.Context, string, ask.Options, int) (ask.Response, ask.Response, error) {
			poolCalls++
			return ask.Response{Evidence: append([]ask.Evidence(nil), legacy...)}, ask.Response{Evidence: append([]ask.Evidence(nil), legacy...)}, nil
		}
		b.strategyRunner = func(context.Context, string, ask.Options) (ask.Response, error) {
			lexicalCalls++
			return ask.Response{Evidence: append([]ask.Evidence(nil), legacy...)}, nil
		}
		pack, err := b.Build(context.Background(), Options{Question: "one", Limit: 8, DisablePlanner: true})
		return pack, poolCalls, lexicalCalls, err
	}
	off, offPoolCalls, offLexicalCalls, err := build(semanticconfig.ModeOff)
	if err != nil {
		t.Fatal(err)
	}
	if offPoolCalls != 0 || offLexicalCalls != 1 {
		t.Fatalf("off calls: pool=%d lexical=%d", offPoolCalls, offLexicalCalls)
	}
	shadow, calls, shadowLexicalCalls, err := build(semanticconfig.ModeShadow)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || shadowLexicalCalls != 0 {
		t.Fatalf("shadow calls: pool=%d lexical=%d", calls, shadowLexicalCalls)
	}
	if !reflect.DeepEqual(shadow.Evidence, off.Evidence) {
		t.Fatalf("shadow evidence changed: shadow=%#v off=%#v", shadow.Evidence, off.Evidence)
	}
	if shadow.QueryPlan.SemanticMode != semanticconfig.ModeShadow || shadow.QueryPlan.ShadowComparison == nil {
		t.Fatalf("query plan = %#v", shadow.QueryPlan)
	}
	comparison := shadow.QueryPlan.ShadowComparison
	if comparison.Status != semanticindex.StateSearched || comparison.LexicalCount != 2 || comparison.HybridCount != 3 {
		t.Fatalf("comparison = %#v", comparison)
	}
	if comparison.Lexical == nil || comparison.Hybrid == nil || comparison.Added == nil || comparison.Removed == nil || comparison.Reordered == nil {
		t.Fatalf("comparison arrays must be non-null: %#v", comparison)
	}
	encoded, err := json.Marshal(comparison)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private lexical"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("comparison leaked content %q: %s", secret, encoded)
		}
	}
	if len(comparison.Added) != 1 || comparison.Added[0].SourceKey != "semantic:new" || comparison.Added[0].ChunkID != "chunk:new" || comparison.Added[0].Rank < 1 {
		t.Fatalf("allowed stable diagnostics missing: %#v", comparison.Added)
	}
	if len(comparison.Lexical) > researchhybrid.DefaultFusedCandidateWindow || len(comparison.Hybrid) > researchhybrid.DefaultFusedCandidateWindow {
		t.Fatalf("comparison is unbounded: %#v", comparison)
	}
	synthesisCfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	offPrepared, err := PrepareSynthesis(synthesisCfg, SynthesisOptions{Pack: off, Model: "cli/test/research"})
	if err != nil {
		t.Fatal(err)
	}
	shadowPrepared, err := PrepareSynthesis(synthesisCfg, SynthesisOptions{Pack: shadow, Model: "cli/test/research"})
	if err != nil {
		t.Fatal(err)
	}
	if offPrepared.Input != shadowPrepared.Input || !reflect.DeepEqual(offPrepared.Citations, shadowPrepared.Citations) {
		t.Fatalf("shadow changed synthesis input\noff=%q\nshadow=%q", offPrepared.Input, shadowPrepared.Input)
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

func TestCollectStrategyEvidenceSemanticChildTimeoutFailsOpenButParentDeadlinePropagates(t *testing.T) {
	_, st := inspectionTestStore(t)
	strategy := researchStrategy{Variants: []QueryVariant{{Query: "one"}}}
	build := func(timeout time.Duration) *Builder {
		b := New(config.Config{}, st).WithSemanticRetriever(blockingSemanticRetriever{}, researchsemantic.Options{Timeout: timeout})
		b.strategyPoolRunner = func(context.Context, string, ask.Options, int) (ask.Response, ask.Response, error) {
			lexical := ask.Response{Evidence: []ask.Evidence{{SourceKey: "lexical"}}}
			return lexical, lexical, nil
		}
		return b
	}

	liveParent, liveCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer liveCancel()
	got, err := build(25*time.Millisecond).collectStrategyEvidence(liveParent, strategy, ask.QueryHints{}, Options{Question: "one", UseSemantic: true}, 5, 100, nil)
	if err != nil || len(got) != 1 || got[0].SourceKey != "lexical" {
		t.Fatalf("child timeout got=%+v err=%v", got, err)
	}
	if liveParent.Err() != nil {
		t.Fatalf("child timeout consumed parent budget: %v", liveParent.Err())
	}
	b := build(time.Second)
	parent, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err = b.collectStrategyEvidence(parent, strategy, ask.QueryHints{}, Options{Question: "one", UseSemantic: true}, 5, 100, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("parent deadline error = %v", err)
	}

	failOpen := build(25 * time.Millisecond)
	_, err = failOpen.collectStrategyEvidence(context.Background(), strategy, ask.QueryHints{}, Options{Question: "one", UseSemantic: true}, 5, 100, nil)
	if err != nil || failOpen.semanticStatus == nil || failOpen.semanticStatus.Reason != semanticindex.ReasonProviderUnavailable {
		t.Fatalf("fail-open status=%+v err=%v", failOpen.semanticStatus, err)
	}
}

func TestShadowComparisonDistinguishesUnavailableAndSearchedEmpty(t *testing.T) {
	_, st := inspectionTestStore(t)
	strategy := researchStrategy{Variants: []QueryVariant{{Query: "one"}}}
	legacy := ask.Response{Evidence: []ask.Evidence{{SourceKey: "legacy"}}}
	build := func(retriever SemanticRetriever, filters []string) *Builder {
		b := New(config.Config{}, st).WithSemanticMode(semanticconfig.ModeShadow).WithSemanticRetriever(retriever, researchsemantic.Options{Filters: semanticindex.Filters{AllowedSourceTypes: filters}})
		b.strategyPoolRunner = func(context.Context, string, ask.Options, int) (ask.Response, ask.Response, error) {
			return legacy, legacy, nil
		}
		return b
	}
	missing := build(nil, nil)
	if _, err := missing.collectStrategyEvidence(context.Background(), strategy, ask.QueryHints{}, Options{Question: "one"}, 5, 100, nil); err != nil || missing.shadowComparison == nil || missing.shadowComparison.Status != semanticindex.StateUnavailable || missing.shadowComparison.Reason != shadowReasonRetrieverMissing {
		t.Fatalf("missing comparison=%#v err=%v", missing.shadowComparison, err)
	}
	disjoint := build(&fakeSemanticRetriever{}, []string{"article"})
	if _, err := disjoint.collectStrategyEvidence(context.Background(), strategy, ask.QueryHints{}, Options{Question: "one", SourceTypes: []string{"x"}}, 5, 100, nil); err != nil || disjoint.shadowComparison == nil || disjoint.shadowComparison.Reason != shadowReasonFiltersDisjoint {
		t.Fatalf("disjoint comparison=%#v err=%v", disjoint.shadowComparison, err)
	}
	empty := build(&fakeSemanticRetriever{status: semanticindex.Status{State: semanticindex.StateSearched}}, nil)
	if _, err := empty.collectStrategyEvidence(context.Background(), strategy, ask.QueryHints{}, Options{Question: "one"}, 5, 100, nil); err != nil || empty.shadowComparison == nil || empty.shadowComparison.Status != semanticindex.StateSearched || empty.shadowComparison.Reason != shadowReasonSearchedEmpty {
		t.Fatalf("empty comparison=%#v err=%v", empty.shadowComparison, err)
	}
}

func TestOnModeReportsFailOpenSemanticLaneOutcome(t *testing.T) {
	_, st := inspectionTestStore(t)
	for _, tc := range []struct {
		name       string
		status     semanticindex.Status
		wantStatus string
		wantReason string
	}{
		{"unavailable", semanticindex.Status{State: semanticindex.StateUnavailable, Reason: semanticindex.ReasonProviderUnavailable}, researchhybrid.StatusDisabled, string(semanticindex.ReasonProviderUnavailable)},
		{"searched empty", semanticindex.Status{State: semanticindex.StateSearched}, researchhybrid.StatusUsed, string(shadowReasonSearchedEmpty)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := New(config.Config{}, st).WithSemanticMode(semanticconfig.ModeOn).WithSemanticRetriever(&fakeSemanticRetriever{status: tc.status}, researchsemantic.Options{})
			b.strategyPoolRunner = func(context.Context, string, ask.Options, int) (ask.Response, ask.Response, error) {
				legacy := ask.Response{Evidence: []ask.Evidence{{SourceKey: "legacy"}}}
				return legacy, legacy, nil
			}
			pack, err := b.Build(context.Background(), Options{Question: "one", DisablePlanner: true})
			if err != nil {
				t.Fatal(err)
			}
			var semantic retrieval.RetrievalLane
			for _, lane := range pack.QueryPlan.RetrievalLanes {
				if lane.Name == researchhybrid.LaneSemantic {
					semantic = lane
				}
			}
			if semantic.Status != tc.wantStatus || semantic.Reason != tc.wantReason || pack.QueryPlan.ShadowComparison != nil {
				t.Fatalf("semantic lane=%#v comparison=%#v", semantic, pack.QueryPlan.ShadowComparison)
			}
		})
	}
}

func TestOnModeGenerationBusyPreservesSemanticOffLexicalEvidenceExactly(t *testing.T) {
	_, st := inspectionTestStore(t)
	legacy := []ask.Evidence{
		{SourceKey: "legacy:one", Excerpt: "first lexical result"},
		{SourceKey: "legacy:two", Excerpt: "second lexical result"},
	}
	build := func(mode semanticconfig.Mode) Pack {
		b := New(config.Config{}, st).
			WithSemanticMode(mode).
			WithSemanticRetriever(
				&fakeSemanticRetriever{status: semanticindex.Status{
					State:  semanticindex.StateUnavailable,
					Reason: researchsemantic.ReasonGenerationBusy,
				}},
				researchsemantic.Options{},
			)
		b.strategyRunner = func(context.Context, string, ask.Options) (ask.Response, error) {
			return ask.Response{Evidence: append([]ask.Evidence(nil), legacy...)}, nil
		}
		b.strategyPoolRunner = func(context.Context, string, ask.Options, int) (ask.Response, ask.Response, error) {
			response := ask.Response{Evidence: append([]ask.Evidence(nil), legacy...)}
			return response, response, nil
		}
		pack, err := b.Build(context.Background(), Options{Question: "one", DisablePlanner: true})
		if err != nil {
			t.Fatal(err)
		}
		return pack
	}
	off := build(semanticconfig.ModeOff)
	busy := build(semanticconfig.ModeOn)
	if !reflect.DeepEqual(busy.Evidence, off.Evidence) {
		t.Fatalf("generation-busy evidence changed: off=%#v busy=%#v", off.Evidence, busy.Evidence)
	}
	var lane retrieval.RetrievalLane
	for _, candidate := range busy.QueryPlan.RetrievalLanes {
		if candidate.Name == researchhybrid.LaneSemantic {
			lane = candidate
		}
	}
	if lane.Status != researchhybrid.StatusDisabled || lane.Reason != string(researchsemantic.ReasonGenerationBusy) {
		t.Fatalf("semantic lane=%#v", lane)
	}
}

func TestShadowModeReportsTruthfulLaneWithoutChangingLexicalEvidence(t *testing.T) {
	_, st := inspectionTestStore(t)
	legacy := []ask.Evidence{{SourceKey: "legacy:one", Excerpt: "lexical evidence"}}
	for _, tc := range []struct {
		name       string
		status     semanticindex.Status
		rows       []retrieval.EvidenceDocument
		wantStatus string
		wantReason string
	}{
		{"searched", semanticindex.Status{State: semanticindex.StateSearched, Backend: semanticindex.BackendExact, ProfileID: "profile", GenerationID: "generation"}, []retrieval.EvidenceDocument{chunkEvidence("semantic:new", "chunk:new")}, researchhybrid.StatusUsed, ""},
		{"unavailable", semanticindex.Status{State: semanticindex.StateUnavailable, Reason: semanticindex.ReasonProviderUnavailable, Backend: semanticindex.BackendExact, ProfileID: "profile"}, nil, researchhybrid.StatusDisabled, string(semanticindex.ReasonProviderUnavailable)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			build := func(mode semanticconfig.Mode) Pack {
				b := New(config.Config{}, st).WithSemanticMode(mode).WithSemanticRetriever(&fakeSemanticRetriever{status: tc.status, rows: tc.rows}, researchsemantic.Options{})
				b.strategyRunner = func(context.Context, string, ask.Options) (ask.Response, error) {
					return ask.Response{Evidence: append([]ask.Evidence(nil), legacy...)}, nil
				}
				b.strategyPoolRunner = func(context.Context, string, ask.Options, int) (ask.Response, ask.Response, error) {
					response := ask.Response{Evidence: append([]ask.Evidence(nil), legacy...)}
					return response, response, nil
				}
				pack, err := b.Build(context.Background(), Options{Question: "one", DisablePlanner: true})
				if err != nil {
					t.Fatal(err)
				}
				return pack
			}
			off, shadow := build(semanticconfig.ModeOff), build(semanticconfig.ModeShadow)
			if !reflect.DeepEqual(off.Evidence, shadow.Evidence) {
				t.Fatalf("shadow evidence changed: off=%#v shadow=%#v", off.Evidence, shadow.Evidence)
			}
			var lane retrieval.RetrievalLane
			for _, candidate := range shadow.QueryPlan.RetrievalLanes {
				if candidate.Name == researchhybrid.LaneSemantic {
					lane = candidate
				}
			}
			if lane.Status != tc.wantStatus || lane.Reason != tc.wantReason || lane.Backend != semanticindex.BackendExact || lane.Profile != "profile" || (tc.name == "searched" && lane.Generation != "generation") {
				t.Fatalf("shadow semantic lane=%#v", lane)
			}
		})
	}
}

func TestOnModeSmallLimitRetainsProtectedSemanticEvidence(t *testing.T) {
	_, st := inspectionTestStore(t)
	protectedKey := "x:protected"
	fake := &fakeSemanticRetriever{status: semanticindex.Status{State: semanticindex.StateSearched}, rows: []retrieval.EvidenceDocument{
		chunkEvidence("semantic:distractor", "chunk:distractor"),
		chunkEvidence(protectedKey, "chunk:protected"),
	}}
	b := New(config.Config{}, st).WithSemanticMode(semanticconfig.ModeOn).WithSemanticRetriever(fake, researchsemantic.Options{})
	b.strategyPoolRunner = func(context.Context, string, ask.Options, int) (ask.Response, ask.Response, error) {
		legacy := ask.Response{Evidence: []ask.Evidence{{SourceKey: "lexical:one"}, {SourceKey: "lexical:two"}}}
		return legacy, legacy, nil
	}
	rows, err := b.collectStrategyEvidence(context.Background(), researchStrategy{Variants: []QueryVariant{{Query: "one"}}}, ask.QueryHints{}, Options{Question: "one"}, 1, 100, []ProtectedAnchor{anchorFromSourceKey(protectedKey, "current_user_text")})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].SourceKey != protectedKey {
		t.Fatalf("protected evidence was dropped at small limit: %#v", rows)
	}
}
