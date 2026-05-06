package topics

import (
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/entities"
)

func collectTopicEntities(graph TopicMap, index []entities.Entity, topic string, limit int) []TopicEntity {
	if len(graph.Nodes) == 0 || len(index) == 0 {
		return nil
	}

	nodeKeys := map[string]struct{}{}
	for _, node := range graph.Nodes {
		nodeKeys[node.SourceKey] = struct{}{}
	}
	topicTerms := topicQueryTerms(topic)

	type scoredTopicEntity struct {
		Entity       TopicEntity
		SortScore    int
		KindPriority int
	}

	matched := make([]scoredTopicEntity, 0, len(index))
	for _, entity := range index {
		sourceKeys := make([]string, 0, 4)
		seen := map[string]struct{}{}
		for _, ref := range entity.References {
			if _, ok := nodeKeys[ref.SourceKey]; !ok {
				continue
			}
			if _, exists := seen[ref.SourceKey]; exists {
				continue
			}
			seen[ref.SourceKey] = struct{}{}
			sourceKeys = append(sourceKeys, ref.SourceKey)
		}
		if len(sourceKeys) == 0 {
			continue
		}
		score := scoreTopicEntity(entity, topic, topicTerms, len(sourceKeys))
		if score <= 0 {
			continue
		}
		matched = append(matched, scoredTopicEntity{
			Entity: TopicEntity{
				Key:               entity.Key,
				Name:              entity.Name,
				Kind:              string(entity.Kind),
				NotePath:          entity.NotePath,
				CanonicalURL:      entity.CanonicalURL,
				ReferenceCount:    entity.ReferenceCount,
				MatchedReferences: len(sourceKeys),
				MatchedSourceKeys: sourceKeys,
			},
			SortScore:    score,
			KindPriority: topicEntityKindPriority(entity.Kind),
		})
	}

	sort.Slice(matched, func(i, j int) bool {
		if matched[i].SortScore != matched[j].SortScore {
			return matched[i].SortScore > matched[j].SortScore
		}
		if matched[i].Entity.MatchedReferences != matched[j].Entity.MatchedReferences {
			return matched[i].Entity.MatchedReferences > matched[j].Entity.MatchedReferences
		}
		if matched[i].KindPriority != matched[j].KindPriority {
			return matched[i].KindPriority > matched[j].KindPriority
		}
		if matched[i].Entity.ReferenceCount != matched[j].Entity.ReferenceCount {
			return matched[i].Entity.ReferenceCount > matched[j].Entity.ReferenceCount
		}
		if matched[i].Entity.Kind != matched[j].Entity.Kind {
			return matched[i].Entity.Kind < matched[j].Entity.Kind
		}
		return strings.ToLower(matched[i].Entity.Name) < strings.ToLower(matched[j].Entity.Name)
	})

	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	result := make([]TopicEntity, 0, len(matched))
	for _, entry := range matched {
		result = append(result, entry.Entity)
	}
	return result
}

func buildTopicPivots(graph TopicMap) TopicPivots {
	pivots := TopicPivots{
		Projects:     make([]TopicEntity, 0, 3),
		Orgs:         make([]TopicEntity, 0, 3),
		Sites:        make([]TopicEntity, 0, 3),
		People:       make([]TopicEntity, 0, 3),
		SeedNodes:    make([]TopicMapNode, 0, len(graph.Nodes)),
		RelatedNodes: make([]TopicMapNode, 0, len(graph.Nodes)),
	}

	for _, entity := range graph.Entities {
		switch entity.Kind {
		case "project":
			if len(pivots.Projects) < 3 {
				pivots.Projects = append(pivots.Projects, entity)
			}
		case "org":
			if len(pivots.Orgs) < 3 {
				pivots.Orgs = append(pivots.Orgs, entity)
			}
		case "site":
			if len(pivots.Sites) < 3 {
				pivots.Sites = append(pivots.Sites, entity)
			}
		case "person":
			if len(pivots.People) < 3 {
				pivots.People = append(pivots.People, entity)
			}
		}
	}

	for _, node := range graph.Nodes {
		switch node.Role {
		case "seed":
			if len(pivots.SeedNodes) < 5 {
				pivots.SeedNodes = append(pivots.SeedNodes, node)
			}
		case "related":
			if len(pivots.RelatedNodes) < 5 {
				pivots.RelatedNodes = append(pivots.RelatedNodes, node)
			}
		}
	}

	return pivots
}
