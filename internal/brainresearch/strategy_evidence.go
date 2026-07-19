package brainresearch

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/researchhybrid"
	"github.com/darron/dbrain/internal/researchsemantic"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
)

const exactTagPrimaryBoost = 64

const (
	shadowReasonRetrieverMissing semanticindex.StatusReason = "retriever_missing"
	shadowReasonFiltersDisjoint  semanticindex.StatusReason = "filters_disjoint"
	shadowReasonSearchedEmpty    semanticindex.StatusReason = "searched_empty"
)

type scoredResearchEvidence struct {
	doc   ask.Evidence
	score int
	order int
}

func (b *Builder) collectStrategyEvidence(ctx context.Context, strategy researchStrategy, hints ask.QueryHints, opts Options, limit int, maxChars int, anchors []ProtectedAnchor) ([]ask.Evidence, error) {
	variants := strategy.Variants
	if len(variants) == 0 {
		variants = []QueryVariant{{Query: opts.Question, Reason: "original_question"}}
	}

	seen := map[string]scoredResearchEvidence{}
	baselineSeen := map[string]scoredResearchEvidence{}
	order := 0
	baselineOrder := 0
	legacyPerVariantLimit := max(limit, 8)
	perVariantLimit := legacyPerVariantLimit
	semanticSourceTypes, semanticFiltersCompatible := intersectSemanticSourceTypes(b.semanticOptions.Filters.AllowedSourceTypes, opts.SourceTypes)
	semanticRequested := (b.semanticMode != semanticconfig.ModeOff || opts.UseSemantic) && !opts.DisableSemantic
	semanticActive := semanticRequested && b.semanticRetriever != nil && semanticFiltersCompatible
	deepResponses := make([]ask.Response, len(variants))
	poolFailed := false
	entityIndex, err := entities.BuildIndex(ctx, b.st)
	if err != nil {
		return nil, err
	}
	for variantIndex, variant := range variants {
		runOpts := ask.Options{
			Limit:               perVariantLimit,
			RetrieveOnly:        true,
			SourceTypes:         opts.SourceTypes,
			IncludeRelated:      opts.IncludeRelated,
			RelatedLimit:        opts.RelatedLimit,
			MaxCharsPerDoc:      maxChars,
			SearchLimit:         max(perVariantLimit*2, 12),
			EntityIndex:         entityIndex,
			DisableTagExpansion: true,
		}
		var resp ask.Response
		if semanticActive {
			var deep ask.Response
			resp, deep, err = b.runStrategyPool(ctx, variant.Query, runOpts, max(legacyPerVariantLimit, researchhybrid.DefaultLexicalDepth))
			if err == nil {
				deepResponses[variantIndex] = deep
			} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				return nil, err
			} else {
				poolFailed = true
				resp, err = b.runStrategyEvidence(ctx, variant.Query, runOpts)
			}
		} else {
			resp, err = b.runStrategyEvidence(ctx, variant.Query, runOpts)
		}
		if err != nil {
			return nil, err
		}
		emitEvent(opts.Observer, "variant_retrieved", map[string]interface{}{
			"query":           variant.Query,
			"reason":          variant.Reason,
			"candidate_count": len(resp.Evidence),
			"source_keys":     evidenceSourceKeys(resp.Evidence),
		})
		for rank, doc := range resp.Evidence {
			if strings.TrimSpace(doc.SourceKey) == "" {
				continue
			}
			scored := scoreEvidenceWithResearchStrategy(doc, strategy, variant, rank)
			if rank < legacyPerVariantLimit {
				updateScoredEvidence(baselineSeen, scored, &baselineOrder)
			}
			current, exists := seen[doc.SourceKey]
			if exists {
				emitEvent(opts.Observer, "evidence_deduped", map[string]interface{}{
					"source_key":     doc.SourceKey,
					"query":          variant.Query,
					"existing_score": current.score,
					"new_score":      scored.score,
					"selected":       scored.score > current.score,
				})
			}
			if !exists || scored.score > current.score {
				scored.order = order
				if exists {
					scored.order = current.order
				} else {
					order++
				}
				seen[doc.SourceKey] = scored
			}
		}
	}

	exactTagDocs, err := b.buildExactTagCandidates(ctx, hints.TagQueries, opts.SourceTypes, maxChars, hints.Terms, max(legacyPerVariantLimit*4, 64))
	if err != nil {
		return nil, err
	}
	for rank, doc := range exactTagDocs {
		if strings.TrimSpace(doc.SourceKey) == "" {
			continue
		}
		boostExactTagPrimaryCandidate(&doc)
		scored := scoreEvidenceWithResearchStrategy(doc, strategy, QueryVariant{Query: "tag:" + exactTagQueryForDoc(doc), Reason: "exact_tag"}, rank)
		updateExactScoredEvidence(baselineSeen, scored, &baselineOrder)
		current, exists := seen[doc.SourceKey]
		if exists {
			emitEvent(opts.Observer, "evidence_deduped", map[string]interface{}{
				"source_key":     doc.SourceKey,
				"query":          "exact_tag",
				"existing_score": current.score,
				"new_score":      scored.score,
				"selected":       scored.score > current.score,
			})
			current.doc = mergeEvidenceRetrieval(current.doc, scored.doc)
			if scored.score > current.score {
				current.score = scored.score
				if current.doc.Retrieval != nil {
					current.doc.Retrieval.Score = scored.score
				}
			}
			seen[doc.SourceKey] = current
			continue
		}
		scored.order = order
		order++
		seen[doc.SourceKey] = scored
	}
	if len(exactTagDocs) > 0 {
		emitEvent(opts.Observer, "exact_tag_retrieved", map[string]interface{}{
			"candidate_count": len(exactTagDocs),
			"source_keys":     evidenceSourceKeys(exactTagDocs),
		})
	}

	baseline := orderedScoredEvidence(baselineSeen, anchors)
	comparisonLexical := make([]ask.Evidence, 0, len(baseline))
	for _, scored := range baseline {
		comparisonLexical = append(comparisonLexical, scored.doc)
	}
	if len(baseline) > limit {
		baseline = baseline[:limit]
	}
	out := make([]ask.Evidence, 0, len(baseline))
	for _, scored := range baseline {
		out = append(out, scored.doc)
	}
	if !semanticRequested {
		return out, nil
	}
	if b.semanticRetriever == nil {
		b.setShadowComparison(comparisonLexical, comparisonLexical, semanticindex.Status{State: semanticindex.StateUnavailable, Reason: shadowReasonRetrieverMissing})
		return out, nil
	}
	if !semanticFiltersCompatible {
		b.setShadowComparison(comparisonLexical, comparisonLexical, semanticindex.Status{State: semanticindex.StateUnavailable, Reason: shadowReasonFiltersDisjoint})
		return out, nil
	}
	if poolFailed {
		b.setShadowComparison(comparisonLexical, comparisonLexical, semanticindex.Status{State: semanticindex.StateUnavailable, Reason: semanticindex.ReasonSearchError})
		return out, nil
	}
	query := canonicalSemanticQuery(strategy, hints, opts.Question)
	semanticOpts := b.semanticOptions
	semanticOpts.Filters.AllowedSourceTypes = semanticSourceTypes
	semanticTimeout := semanticOpts.Timeout
	if semanticTimeout <= 0 {
		semanticTimeout = researchsemantic.DefaultQueryTimeout
	}
	semanticCtx, cancelSemantic := context.WithTimeout(ctx, semanticTimeout)
	semanticRows, status, semanticErr := b.semanticRetriever.Retrieve(semanticCtx, query, semanticOpts)
	semanticCtxErr := semanticCtx.Err()
	cancelSemantic()
	if semanticErr != nil {
		if parentErr := ctx.Err(); parentErr != nil {
			return nil, parentErr
		}
		if errors.Is(semanticErr, context.DeadlineExceeded) && errors.Is(semanticCtxErr, context.DeadlineExceeded) {
			status.State = semanticindex.StateUnavailable
			status.Reason = semanticindex.ReasonProviderUnavailable
			if status.Backend == "" {
				status.Backend = semanticindex.BackendExact
			}
			b.setShadowComparison(comparisonLexical, comparisonLexical, status)
			return out, nil
		}
		if errors.Is(semanticErr, context.Canceled) || errors.Is(semanticErr, context.DeadlineExceeded) {
			return nil, semanticErr
		}
		if status.State == "" {
			status = semanticindex.Status{State: semanticindex.StateUnavailable, Reason: semanticindex.ReasonSearchError}
		}
		b.setShadowComparison(comparisonLexical, comparisonLexical, status)
		return out, nil
	}
	if status.State != semanticindex.StateSearched || len(semanticRows) == 0 {
		if status.State == semanticindex.StateSearched && status.Reason == "" {
			status.Reason = shadowReasonSearchedEmpty
		}
		b.setShadowComparison(comparisonLexical, comparisonLexical, status)
		return out, nil
	}
	deepSeen := map[string]scoredResearchEvidence{}
	deepOrder := 0
	for variantIndex, variant := range variants {
		resp := deepResponses[variantIndex]
		for rank, doc := range resp.Evidence {
			if strings.TrimSpace(doc.SourceKey) == "" {
				continue
			}
			updateScoredEvidence(deepSeen, scoreEvidenceWithResearchStrategy(doc, strategy, variant, rank), &deepOrder)
		}
	}
	for rank, doc := range exactTagDocs {
		if strings.TrimSpace(doc.SourceKey) == "" {
			continue
		}
		boostExactTagPrimaryCandidate(&doc)
		updateExactScoredEvidence(deepSeen, scoreEvidenceWithResearchStrategy(doc, strategy, QueryVariant{Query: "tag:" + exactTagQueryForDoc(doc), Reason: "exact_tag"}, rank), &deepOrder)
	}
	ordered := orderedScoredEvidence(deepSeen, anchors)
	lexical := make([]ask.Evidence, 0, len(ordered))
	for _, scored := range ordered {
		lexical = append(lexical, scored.doc)
	}
	protected := protectedSourceKeys(lexical, semanticRows, anchors)
	hybridWindow := researchhybrid.Fuse(lexical, semanticRows, researchhybrid.DefaultFusedCandidateWindow, maxChars, protected)
	b.setShadowComparison(comparisonLexical, hybridWindow, status)
	if b.semanticMode == semanticconfig.ModeShadow {
		return out, nil
	}
	return researchhybrid.Fuse(lexical, semanticRows, limit, maxChars, protected), nil
}

func (b *Builder) setShadowComparison(lexical, hybrid []ask.Evidence, status semanticindex.Status) {
	statusCopy := status
	b.semanticStatus = &statusCopy
	if b.semanticMode != semanticconfig.ModeShadow {
		return
	}
	b.shadowComparison = buildShadowComparison(lexical, hybrid, status)
}

func buildShadowComparison(lexical, hybrid []ask.Evidence, status semanticindex.Status) *ShadowComparison {
	lexicalRefs := shadowReferences(lexical)
	hybridRefs := shadowReferences(hybrid)
	comparison := &ShadowComparison{
		Status: status.State, Reason: status.Reason,
		LexicalCount: len(lexical), HybridCount: len(hybrid),
		Lexical: lexicalRefs, Hybrid: hybridRefs,
		Added: make([]ShadowRankedReference, 0), Removed: make([]ShadowRankedReference, 0), Reordered: make([]ShadowRankedReference, 0),
	}
	lexicalRanks := make(map[string]int, len(lexicalRefs))
	hybridRanks := make(map[string]int, len(hybridRefs))
	for _, ref := range lexicalRefs {
		lexicalRanks[shadowReferenceKey(ref)] = ref.Rank
	}
	for _, ref := range hybridRefs {
		key := shadowReferenceKey(ref)
		hybridRanks[key] = ref.Rank
		if _, ok := lexicalRanks[key]; !ok {
			comparison.Added = append(comparison.Added, ref)
		} else if lexicalRanks[key] != ref.Rank {
			comparison.Reordered = append(comparison.Reordered, ref)
		}
	}
	for _, ref := range lexicalRefs {
		if _, ok := hybridRanks[shadowReferenceKey(ref)]; !ok {
			comparison.Removed = append(comparison.Removed, ref)
		}
	}
	return comparison
}

func shadowReferences(rows []ask.Evidence) []ShadowRankedReference {
	limit := min(len(rows), researchhybrid.DefaultFusedCandidateWindow)
	out := make([]ShadowRankedReference, 0, limit)
	for i := 0; i < limit; i++ {
		row := rows[i]
		ref := ShadowRankedReference{SourceKey: strings.TrimSpace(row.SourceKey), Rank: i + 1}
		if row.Chunk != nil {
			ref.ChunkID = strings.TrimSpace(row.Chunk.ID)
		}
		out = append(out, ref)
	}
	return out
}

func shadowReferenceKey(ref ShadowRankedReference) string {
	return ref.SourceKey + "\x00" + ref.ChunkID
}

func updateScoredEvidence(seen map[string]scoredResearchEvidence, scored scoredResearchEvidence, order *int) {
	key := strings.TrimSpace(scored.doc.SourceKey)
	if key == "" {
		return
	}
	current, exists := seen[key]
	if !exists || scored.score > current.score {
		if exists {
			scored.order = current.order
		} else {
			scored.order = *order
			*order++
		}
		seen[key] = scored
	}
}
func updateExactScoredEvidence(seen map[string]scoredResearchEvidence, scored scoredResearchEvidence, order *int) {
	key := strings.TrimSpace(scored.doc.SourceKey)
	if key == "" {
		return
	}
	current, exists := seen[key]
	if !exists {
		scored.order = *order
		*order++
		seen[key] = scored
		return
	}
	current.doc = mergeEvidenceRetrieval(current.doc, scored.doc)
	if scored.score > current.score {
		current.score = scored.score
		if current.doc.Retrieval != nil {
			current.doc.Retrieval.Score = scored.score
		}
	}
	seen[key] = current
}
func orderedScoredEvidence(seen map[string]scoredResearchEvidence, anchors []ProtectedAnchor) []scoredResearchEvidence {
	ordered := make([]scoredResearchEvidence, 0, len(seen))
	for _, doc := range seen {
		ordered = append(ordered, doc)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].score != ordered[j].score {
			return ordered[i].score > ordered[j].score
		}
		return ordered[i].order < ordered[j].order
	})
	return filterToProtectedAnchorEvidenceWhenAvailable(ordered, anchors)
}

func canonicalSemanticQuery(strategy researchStrategy, hints ask.QueryHints, question string) string {
	if value := preferredSemanticConceptQuery(strategy.Concepts); value != "" {
		return value
	}
	if value := strings.Join(strings.Fields(hints.TextQuery), " "); value != "" {
		return value
	}
	return strings.Join(strings.Fields(question), " ")
}

func preferredSemanticConceptQuery(concepts []QueryConcept) string {
	values := make([]string, 0, len(concepts))
	seen := map[string]struct{}{}
	for _, concept := range concepts {
		role := strings.ToLower(strings.TrimSpace(concept.Role))
		if role != conceptRoleAnchor && role != conceptRoleContent {
			continue
		}
		value := strings.TrimSpace(concept.Preferred)
		if value == "" {
			for _, term := range concept.Terms {
				value = strings.TrimSpace(term)
				if value != "" {
					break
				}
			}
		}
		if value == "" {
			value = strings.TrimSpace(concept.Key)
		}
		key := strings.ToLower(value)
		if value == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		values = append(values, value)
	}
	return strings.Join(values, " ")
}

func (b *Builder) runStrategyEvidence(ctx context.Context, query string, opts ask.Options) (ask.Response, error) {
	if b.strategyRunner != nil {
		return b.strategyRunner(ctx, query, opts)
	}
	return ask.Run(ctx, b.cfg, b.st, query, opts)
}
func (b *Builder) runStrategyPool(ctx context.Context, query string, opts ask.Options, deepLimit int) (ask.Response, ask.Response, error) {
	if b.strategyPoolRunner != nil {
		return b.strategyPoolRunner(ctx, query, opts, deepLimit)
	}
	return ask.RunCandidatePool(ctx, b.cfg, b.st, query, opts, deepLimit)
}

func intersectSemanticSourceTypes(configured, requested []string) ([]string, bool) {
	configured = cleanSemanticSourceTypes(configured)
	requested = cleanSemanticSourceTypes(requested)
	if len(configured) == 0 {
		return requested, true
	}
	if len(requested) == 0 {
		return configured, true
	}
	out := make([]string, 0)
	seen := map[string]struct{}{}
	for _, cfg := range configured {
		for _, req := range requested {
			value, ok := semanticSourceTypeIntersection(cfg, req)
			if !ok {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}
func cleanSemanticSourceTypes(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
func semanticSourceTypeIntersection(a, b string) (string, bool) {
	if a == b {
		return a, true
	}
	af, bf := sourceTypeFamily(a), sourceTypeFamily(b)
	if a == bf {
		return b, true
	}
	if b == af {
		return a, true
	}
	return "", false
}
func protectedSourceKeys(lexical, semantic []ask.Evidence, anchors []ProtectedAnchor) map[string]struct{} {
	out := map[string]struct{}{}
	for _, row := range append(append([]ask.Evidence(nil), lexical...), semantic...) {
		protect := EvidenceMatchesAnyProtectedAnchor(row, anchors)
		if row.Retrieval != nil {
			for _, lane := range row.Retrieval.Lanes {
				if strings.EqualFold(lane.Name, researchhybrid.LaneExactTag) {
					protect = true
				}
			}
			for _, signal := range row.Retrieval.Signals {
				if strings.Contains(strings.ToLower(signal.Name), "exact") || strings.Contains(strings.ToLower(signal.Name), "protected") {
					protect = true
				}
			}
		}
		if protect {
			key := strings.TrimSpace(row.SourceKey)
			if row.Chunk != nil && strings.TrimSpace(row.Chunk.ParentSourceKey) != "" {
				key = strings.TrimSpace(row.Chunk.ParentSourceKey)
			}
			if key != "" {
				out[key] = struct{}{}
			}
		}
	}
	return out
}

func filterToProtectedAnchorEvidenceWhenAvailable(ordered []scoredResearchEvidence, anchors []ProtectedAnchor) []scoredResearchEvidence {
	if len(anchors) == 0 {
		return ordered
	}
	matching := make([]scoredResearchEvidence, 0, len(ordered))
	for _, scored := range ordered {
		if EvidenceMatchesAnyProtectedAnchor(scored.doc, anchors) {
			matching = append(matching, scored)
		}
	}
	if len(matching) == 0 {
		return ordered
	}
	return matching
}

func boostExactTagPrimaryCandidate(doc *ask.Evidence) {
	if doc.Retrieval == nil {
		doc.Retrieval = &ask.RetrievalInfo{}
	}
	doc.Retrieval.Score += exactTagPrimaryBoost
	doc.Retrieval.Signals = append(doc.Retrieval.Signals, ask.RetrievalSignal{
		Name:   "exact_tag_primary_candidate",
		Detail: "",
		Weight: exactTagPrimaryBoost,
	})
	*doc = researchhybrid.WithLane(*doc, ask.RetrievalLane{Name: researchhybrid.LaneExactTag, Status: researchhybrid.StatusUsed})
}

func exactTagQueryForDoc(doc ask.Evidence) string {
	if doc.Retrieval == nil {
		return ""
	}
	for _, signal := range doc.Retrieval.Signals {
		if signal.Name == "exact_user_tag_example" {
			return signal.Detail
		}
	}
	return ""
}

func mergeEvidenceRetrieval(primary ask.Evidence, secondary ask.Evidence) ask.Evidence {
	if secondary.Retrieval == nil {
		return primary
	}
	if primary.Retrieval == nil {
		primary.Retrieval = secondary.Retrieval
		return primary
	}
	if secondary.Retrieval.Score > primary.Retrieval.Score {
		primary.Retrieval.Score = secondary.Retrieval.Score
	}
	for _, lane := range secondary.Retrieval.Lanes {
		primary = researchhybrid.WithLane(primary, lane)
	}
	primary.Retrieval.Signals = append(primary.Retrieval.Signals, secondary.Retrieval.Signals...)
	primary.Retrieval.MatchedTerms = uniqueStrings(append(primary.Retrieval.MatchedTerms, secondary.Retrieval.MatchedTerms...))
	primary.Retrieval.MissingTerms = missingTermsAfterMatches(
		uniqueStrings(append(primary.Retrieval.MissingTerms, secondary.Retrieval.MissingTerms...)),
		primary.Retrieval.MatchedTerms,
	)
	return primary
}

func missingTermsAfterMatches(missing []string, matched []string) []string {
	if len(missing) == 0 || len(matched) == 0 {
		return missing
	}
	matchedSet := make(map[string]struct{}, len(matched))
	for _, term := range matched {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		matchedSet[term] = struct{}{}
	}
	out := missing[:0]
	for _, term := range missing {
		if _, ok := matchedSet[strings.ToLower(strings.TrimSpace(term))]; ok {
			continue
		}
		out = append(out, term)
	}
	return out
}
