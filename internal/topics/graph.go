package topics

import (
	"context"
	"strings"

	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

func resolveGraphNode(ctx context.Context, st *store.Store, lookup string, role string) (graphNode, bool, error) {
	if item, err := st.GetItem(ctx, lookup); err == nil {
		return graphNode{
			TopicMapNode: TopicMapNode{
				SourceKey:  item.SourceKey,
				Kind:       "item",
				Title:      firstNonEmpty(item.Title, item.SourceKey),
				URL:        item.CanonicalURL,
				NotePath:   item.NotePath,
				SourceType: item.SourceType,
				Role:       role,
			},
			ItemID: item.ID,
		}, true, nil
	}
	source, err := st.GetSource(ctx, lookup)
	if err != nil {
		return graphNode{}, false, nil
	}
	return graphNode{
		TopicMapNode: TopicMapNode{
			SourceKey:  source.SourceKey,
			Kind:       "source",
			Title:      firstNonEmpty(source.Title, source.SourceKey, source.CanonicalURL),
			URL:        source.CanonicalURL,
			NotePath:   source.NotePath,
			SourceType: source.SourceType,
			Role:       role,
		},
		SourceID: source.ID,
	}, true, nil
}

func graphNodeFromItemSourceRef(ctx context.Context, st *store.Store, ref model.ItemSourceRef) (graphNode, error) {
	source, err := st.GetSourceByID(ctx, ref.SourceID)
	if err != nil {
		return graphNode{}, err
	}
	return graphNode{
		TopicMapNode: TopicMapNode{
			SourceKey:  ref.SourceKey,
			Kind:       "source",
			Title:      firstNonEmpty(ref.Title, source.Title, ref.CanonicalURL),
			URL:        ref.CanonicalURL,
			NotePath:   ref.NotePath,
			SourceType: ref.SourceType,
			Role:       "related",
		},
		SourceID: ref.SourceID,
	}, nil
}

func graphNodeFromSourceBacklink(ctx context.Context, st *store.Store, ref model.SourceBacklink) (graphNode, error) {
	item, err := st.GetItem(ctx, ref.SourceKey)
	sourceType := ""
	if err == nil {
		sourceType = item.SourceType
	}
	return graphNode{
		TopicMapNode: TopicMapNode{
			SourceKey:  ref.SourceKey,
			Kind:       "item",
			Title:      firstNonEmpty(ref.Title, ref.SourceKey, ref.CanonicalURL),
			URL:        ref.CanonicalURL,
			NotePath:   ref.NotePath,
			SourceType: sourceType,
			Role:       "related",
		},
		ItemID: ref.ItemID,
	}, nil
}

func filterSearchResults(ctx context.Context, st *store.Store, results []model.SearchResult, sourceTypes []string) []model.SearchResult {
	if len(sourceTypes) == 0 {
		return results
	}
	filtered := make([]model.SearchResult, 0, len(results))
	for _, result := range results {
		if item, err := st.GetItem(ctx, result.SourceKey); err == nil {
			if matchesSourceTypes(sourceTypes, item.SourceType) {
				filtered = append(filtered, result)
			}
			continue
		}
		if source, err := st.GetSource(ctx, result.SourceKey); err == nil {
			if matchesSourceTypes(sourceTypes, source.SourceType) {
				filtered = append(filtered, result)
			}
		}
	}
	return filtered
}

func matchesSourceTypes(filters []string, sourceType string) bool {
	if len(filters) == 0 {
		return true
	}
	sourceType = strings.TrimSpace(strings.ToLower(sourceType))
	family := sourceTypeFamily(sourceType)
	for _, filter := range filters {
		filter = strings.TrimSpace(strings.ToLower(filter))
		if filter == "" {
			continue
		}
		if filter == sourceType || filter == family {
			return true
		}
	}
	return false
}

func addTopicEdge(index map[string]struct{}, graph *TopicMap, edge TopicMapEdge) {
	key := edge.From + "|" + edge.To + "|" + edge.Relationship
	if _, exists := index[key]; exists {
		return
	}
	index[key] = struct{}{}
	graph.Edges = append(graph.Edges, edge)
}

func seedCount(nodes map[string]graphNode) int {
	count := 0
	for _, node := range nodes {
		if node.Role == "seed" {
			count++
		}
	}
	return count
}
