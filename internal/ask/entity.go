package ask

import (
	"context"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func buildEntityMatchIndex(index []entities.Entity, question string, terms []string, limit int) map[string]entityMatch {
	if len(index) == 0 {
		return nil
	}

	type scoredEntity struct {
		Entity entities.Entity
		Score  int
	}

	queries := make([]weightedQuery, 0, len(terms)+2)
	full := strings.ToLower(strings.TrimSpace(question))
	if full != "" {
		queries = append(queries, weightedQuery{Value: full, Weight: 8})
	}
	joined := strings.ToLower(strings.TrimSpace(strings.Join(terms, " ")))
	if joined != "" && joined != full {
		queries = append(queries, weightedQuery{Value: joined, Weight: 6})
	}
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		weight := 3
		if len(term) >= 5 {
			weight = 4
		}
		queries = append(queries, weightedQuery{Value: term, Weight: weight})
	}

	scored := map[string]*scoredEntity{}
	for _, query := range queries {
		for _, entity := range index {
			if !entityMatchesResearchQuery(entity, joined, terms) {
				continue
			}
			matchScore := scoreEntityForQuery(entity, query.Value)
			if matchScore == 0 {
				continue
			}
			entry, ok := scored[entity.Key]
			if !ok {
				entry = &scoredEntity{Entity: entity}
				scored[entity.Key] = entry
			}
			entry.Score += query.Weight + matchScore
			entry.Score += minInt(entity.ReferenceCount, 3)
		}
	}

	ordered := make([]scoredEntity, 0, len(scored))
	for _, entry := range scored {
		ordered = append(ordered, *entry)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Score != ordered[j].Score {
			return ordered[i].Score > ordered[j].Score
		}
		if ordered[i].Entity.ReferenceCount != ordered[j].Entity.ReferenceCount {
			return ordered[i].Entity.ReferenceCount > ordered[j].Entity.ReferenceCount
		}
		return strings.ToLower(ordered[i].Entity.Name) < strings.ToLower(ordered[j].Entity.Name)
	})
	if limit > 0 && len(ordered) > limit {
		ordered = ordered[:limit]
	}

	matches := map[string]entityMatch{}
	for _, entry := range ordered {
		for _, ref := range entry.Entity.References {
			current := matches[ref.SourceKey]
			current.Labels = appendUniqueFold(current.Labels, entry.Entity.Name)
			current.Boost += minInt(entry.Score, 8)
			matches[ref.SourceKey] = current
		}
	}
	return matches
}

func collectEntityCandidates(ctx context.Context, cfg config.Config, st *store.Store, opts Options, question string, terms []string, entityMatches map[string]entityMatch, seen map[string]struct{}) ([]evidenceCandidate, error) {
	if len(entityMatches) == 0 {
		return nil, nil
	}

	keys := make([]string, 0, len(entityMatches))
	for sourceKey := range entityMatches {
		if _, exists := seen[sourceKey]; exists {
			continue
		}
		keys = append(keys, sourceKey)
	}
	sort.Slice(keys, func(i, j int) bool {
		left := entityMatches[keys[i]]
		right := entityMatches[keys[j]]
		if left.Boost != right.Boost {
			return left.Boost > right.Boost
		}
		return keys[i] < keys[j]
	})
	if len(keys) > maxInt(opts.Limit*2, 8) {
		keys = keys[:maxInt(opts.Limit*2, 8)]
	}

	candidates := make([]evidenceCandidate, 0, len(keys))
	for _, sourceKey := range keys {
		candidate, ok, err := buildEvidence(ctx, cfg, st, model.SearchResult{SourceKey: sourceKey}, opts.MaxCharsPerDoc, terms)
		if err != nil {
			return nil, err
		}
		if !ok || !matchesSourceTypes(opts.SourceTypes, candidate.SourceType) {
			continue
		}
		candidate.Relationship = "entity match"
		scoreCandidate(&candidate, question, terms)
		addRetrievalLane(&candidate, RetrievalLane{Name: "lexical", Status: "used"})
		addRetrievalLane(&candidate, RetrievalLane{Name: "entity", Status: "used"})
		applyEntityMatches(&candidate, entityMatches)
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func applyEntityMatches(candidate *evidenceCandidate, matches map[string]entityMatch) {
	match, ok := matches[candidate.SourceKey]
	if !ok {
		return
	}
	candidate.EntityMatches = append([]string(nil), match.Labels...)
	addRetrievalSignal(candidate, "entity_reference", strings.Join(match.Labels, ", "), match.Boost)
}
