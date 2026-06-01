package brainresearch

import (
	"context"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/researchhybrid"
)

const exactTagPrimaryBoost = 64

func (b *Builder) collectStrategyEvidence(ctx context.Context, strategy researchStrategy, hints ask.QueryHints, opts Options, limit int, maxChars int) ([]ask.Evidence, error) {
	variants := strategy.Variants
	if len(variants) == 0 {
		variants = []QueryVariant{{Query: opts.Question, Reason: "original_question"}}
	}

	type scoredEvidence struct {
		doc   ask.Evidence
		score int
		order int
	}
	seen := map[string]scoredEvidence{}
	order := 0
	perVariantLimit := max(limit, 8)
	entityIndex, err := entities.BuildIndex(ctx, b.st)
	if err != nil {
		return nil, err
	}
	for _, variant := range variants {
		resp, err := ask.Run(ctx, b.cfg, b.st, variant.Query, ask.Options{
			Limit:               perVariantLimit,
			RetrieveOnly:        true,
			SourceTypes:         opts.SourceTypes,
			IncludeRelated:      opts.IncludeRelated,
			RelatedLimit:        opts.RelatedLimit,
			MaxCharsPerDoc:      maxChars,
			SearchLimit:         max(perVariantLimit*2, 12),
			EntityIndex:         entityIndex,
			DisableTagExpansion: true,
		})
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

	exactTagDocs, err := b.buildExactTagCandidates(ctx, hints.TagQueries, opts.SourceTypes, maxChars, hints.Terms, max(perVariantLimit*4, 64))
	if err != nil {
		return nil, err
	}
	for rank, doc := range exactTagDocs {
		if strings.TrimSpace(doc.SourceKey) == "" {
			continue
		}
		boostExactTagPrimaryCandidate(&doc)
		scored := scoreEvidenceWithResearchStrategy(doc, strategy, QueryVariant{Query: "tag:" + exactTagQueryForDoc(doc), Reason: "exact_tag"}, rank)
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

	ordered := make([]scoredEvidence, 0, len(seen))
	for _, doc := range seen {
		ordered = append(ordered, doc)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].score != ordered[j].score {
			return ordered[i].score > ordered[j].score
		}
		return ordered[i].order < ordered[j].order
	})
	if len(ordered) > limit {
		ordered = ordered[:limit]
	}
	out := make([]ask.Evidence, 0, len(ordered))
	for _, scored := range ordered {
		out = append(out, scored.doc)
	}
	return out, nil
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
