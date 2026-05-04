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

func entityMatchesResearchQuery(entity entities.Entity, joined string, terms []string) bool {
	joined = strings.ToLower(strings.TrimSpace(joined))
	if len(terms) <= 1 || joined == "" {
		return true
	}

	candidates := entitySearchCandidates(entity)
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(candidate, joined) {
			return true
		}
	}

	matched := 0
	seen := map[string]struct{}{}
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" || len([]rune(term)) < 3 {
			continue
		}
		if _, exists := seen[term]; exists {
			continue
		}
		for _, candidate := range candidates {
			if strings.Contains(candidate, term) {
				seen[term] = struct{}{}
				matched++
				break
			}
		}
		if matched >= 2 {
			return true
		}
	}
	return false
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

func scoreEntityForQuery(entity entities.Entity, query string) int {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" || len([]rune(query)) < 3 {
		return 0
	}

	score := 0
	for _, candidate := range entitySearchCandidates(entity) {
		if candidate == "" {
			continue
		}
		switch {
		case candidate == query:
			score = maxInt(score, 10)
		case strings.Contains(candidate, query) && len([]rune(query)) >= 5:
			score = maxInt(score, 6)
		case strings.Contains(candidate, query):
			score = maxInt(score, 4)
		}
	}
	return score
}

func entitySearchCandidates(entity entities.Entity) []string {
	candidates := []string{
		strings.ToLower(strings.TrimSpace(entity.Name)),
		strings.ToLower(strings.TrimSpace(entity.Domain)),
		strings.ToLower(strings.TrimSpace(entityKeySearchValue(entity.Key))),
	}
	for _, alias := range entity.Aliases {
		candidates = append(candidates, strings.ToLower(strings.TrimSpace(alias)))
	}
	return candidates
}

func entityKeySearchValue(key string) string {
	key = strings.TrimSpace(strings.ToLower(key))
	switch {
	case strings.HasPrefix(key, "github-repo:"):
		return strings.TrimPrefix(key, "github-repo:")
	case strings.HasPrefix(key, "github-owner:"):
		return strings.TrimPrefix(key, "github-owner:")
	case strings.HasPrefix(key, "x-author:name:"):
		return strings.TrimPrefix(key, "x-author:name:")
	case strings.HasPrefix(key, "x-author:"):
		return strings.TrimPrefix(key, "x-author:")
	case strings.HasPrefix(key, "site:"):
		return strings.TrimPrefix(key, "site:")
	default:
		if idx := strings.IndexByte(key, ':'); idx >= 0 && idx+1 < len(key) {
			return key[idx+1:]
		}
		return key
	}
}
