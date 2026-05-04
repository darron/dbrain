package brainresearch

import (
	"context"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/entities"
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
		for rank, doc := range resp.Evidence {
			if strings.TrimSpace(doc.SourceKey) == "" {
				continue
			}
			scored := scoreEvidenceWithResearchStrategy(doc, strategy, variant, rank)
			current, exists := seen[doc.SourceKey]
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
	return out, nil
}

func scoreEvidenceWithResearchStrategy(doc ask.Evidence, strategy researchStrategy, variant QueryVariant, rank int) struct {
	doc   ask.Evidence
	score int
	order int
} {
	if doc.Retrieval == nil {
		doc.Retrieval = &ask.RetrievalInfo{}
	}
	score := doc.Retrieval.Score
	score += max(0, 8-rank)
	appendResearchSignal(doc.Retrieval, "research_query_variant", variant.Query, max(0, 2-rank))

	text := researchEvidenceText(doc)
	matched := make([]string, 0, len(strategy.Concepts))
	missing := make([]string, 0, len(strategy.Concepts))
	requiredTotal := 0
	requiredMatched := 0
	for _, concept := range strategy.Concepts {
		matches := conceptMatchesText(concept, text)
		if concept.Required {
			requiredTotal++
			if matches {
				requiredMatched++
				matched = append(matched, concept.Key)
				continue
			}
			missing = append(missing, concept.Key)
			continue
		}
		if matches {
			matched = append(matched, concept.Key)
		}
	}

	if len(matched) > 0 {
		weight := requiredMatched*14 + (len(matched)-requiredMatched)*4
		score += weight
		appendResearchSignal(doc.Retrieval, "research_concepts_matched", strings.Join(matched, ", "), weight)
	}
	if requiredTotal > 1 && len(missing) > 0 {
		weight := -12 * len(missing)
		score += weight
		appendResearchSignal(doc.Retrieval, "research_concepts_missing", strings.Join(missing, ", "), weight)
	}
	if requiredTotal > 1 && requiredMatched == requiredTotal {
		score += 24
		appendResearchSignal(doc.Retrieval, "all_required_research_concepts_matched", strings.Join(matched, ", "), 24)
	}
	doc.Retrieval.Score = score
	return struct {
		doc   ask.Evidence
		score int
		order int
	}{doc: doc, score: score}
}

func appendResearchSignal(info *ask.RetrievalInfo, name string, detail string, weight int) {
	if info == nil || strings.TrimSpace(name) == "" {
		return
	}
	info.Signals = append(info.Signals, ask.RetrievalSignal{
		Name:   name,
		Detail: strings.TrimSpace(detail),
		Weight: weight,
	})
}

func researchEvidenceText(doc ask.Evidence) string {
	return strings.ToLower(strings.Join([]string{
		doc.SourceKey,
		doc.Title,
		doc.URL,
		doc.NotePath,
		doc.Summary,
		doc.Excerpt,
		doc.Author,
		doc.SourceType,
		doc.UserTags,
		strings.Join(doc.EntityMatches, " "),
	}, "\n"))
}

func conceptMatchesText(concept QueryConcept, text string) bool {
	for _, term := range concept.Terms {
		if containsTerm(text, term) {
			return true
		}
	}
	return false
}

func containsTerm(text string, term string) bool {
	text = strings.ToLower(text)
	term = strings.ToLower(strings.TrimSpace(term))
	if text == "" || term == "" {
		return false
	}
	if strings.Contains(term, " ") {
		return strings.Contains(text, term)
	}
	start := 0
	for {
		idx := strings.Index(text[start:], term)
		if idx < 0 {
			return false
		}
		idx += start
		beforeOK := idx == 0 || !isAlphaNumeric(rune(text[idx-1]))
		after := idx + len(term)
		afterOK := after >= len(text) || !isAlphaNumeric(rune(text[after]))
		if beforeOK && afterOK {
			return true
		}
		start = idx + len(term)
		if start >= len(text) {
			return false
		}
	}
}

func isAlphaNumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}
