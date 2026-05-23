package brainresearch

import (
	"context"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/researchhybrid"
)

func (b *Builder) collectStrategyEvidence(ctx context.Context, strategy researchStrategy, opts Options, limit int, maxChars int) ([]ask.Evidence, error) {
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
	return researchhybrid.Merge(out, nil, limit), nil
}
