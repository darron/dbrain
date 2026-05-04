package topics

import (
	"context"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/store"
)

func Build(ctx context.Context, st *store.Store, topic string, opts Options) (TopicMap, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return TopicMap{}, fmt.Errorf("topic cannot be empty")
	}
	if opts.SeedLimit <= 0 {
		opts.SeedLimit = 6
	}
	if opts.RelatedLimit <= 0 {
		opts.RelatedLimit = 2
	}

	results, err := st.Search(ctx, topic, max(opts.SeedLimit*4, opts.SeedLimit))
	if err != nil {
		return TopicMap{}, err
	}
	sourceResults, err := st.SearchSources(ctx, topic, max(opts.SeedLimit*4, opts.SeedLimit))
	if err != nil {
		return TopicMap{}, err
	}
	results = append(results, sourceResults...)
	results = filterSearchResults(ctx, st, results, opts.SourceTypes)

	graph := TopicMap{
		Topic:        topic,
		SourceTypes:  normalizeSourceTypes(opts.SourceTypes),
		SeedLimit:    opts.SeedLimit,
		RelatedLimit: opts.RelatedLimit,
		Nodes:        make([]TopicMapNode, 0, opts.SeedLimit*(opts.RelatedLimit+1)),
		Edges:        make([]TopicMapEdge, 0, opts.SeedLimit*opts.RelatedLimit),
	}

	nodeIndex := map[string]graphNode{}
	edgeIndex := map[string]struct{}{}

	for _, result := range results {
		if seedCount(nodeIndex) >= opts.SeedLimit {
			break
		}
		node, ok, err := resolveGraphNode(ctx, st, result.SourceKey, "seed")
		if err != nil {
			return TopicMap{}, err
		}
		if !ok {
			continue
		}
		if _, exists := nodeIndex[node.SourceKey]; exists {
			continue
		}
		nodeIndex[node.SourceKey] = node
		graph.Nodes = append(graph.Nodes, node.TopicMapNode)
	}

	for _, node := range graph.Nodes {
		resolved := nodeIndex[node.SourceKey]
		remaining := opts.RelatedLimit
		if remaining <= 0 {
			continue
		}
		if resolved.ItemID > 0 {
			refs, err := st.ListSourcesForItem(ctx, resolved.ItemID)
			if err != nil {
				return TopicMap{}, err
			}
			for _, ref := range refs {
				if remaining == 0 {
					break
				}
				relatedNode, err := graphNodeFromItemSourceRef(ctx, st, ref)
				if err != nil {
					return TopicMap{}, err
				}
				if _, exists := nodeIndex[relatedNode.SourceKey]; !exists {
					nodeIndex[relatedNode.SourceKey] = relatedNode
					graph.Nodes = append(graph.Nodes, relatedNode.TopicMapNode)
				}
				addTopicEdge(edgeIndex, &graph, TopicMapEdge{
					From:         resolved.SourceKey,
					To:           relatedNode.SourceKey,
					Relationship: "links_to",
				})
				remaining--
			}
			continue
		}
		if resolved.SourceID > 0 {
			refs, err := st.ListBacklinksForSource(ctx, resolved.SourceID)
			if err != nil {
				return TopicMap{}, err
			}
			for _, ref := range refs {
				if remaining == 0 {
					break
				}
				relatedNode, err := graphNodeFromSourceBacklink(ctx, st, ref)
				if err != nil {
					return TopicMap{}, err
				}
				if _, exists := nodeIndex[relatedNode.SourceKey]; !exists {
					nodeIndex[relatedNode.SourceKey] = relatedNode
					graph.Nodes = append(graph.Nodes, relatedNode.TopicMapNode)
				}
				addTopicEdge(edgeIndex, &graph, TopicMapEdge{
					From:         relatedNode.SourceKey,
					To:           resolved.SourceKey,
					Relationship: "links_to",
				})
				remaining--
			}
		}
	}

	entityIndex, err := entities.BuildIndex(ctx, st)
	if err != nil {
		return TopicMap{}, err
	}
	graph.Entities = collectTopicEntities(graph, entityIndex, topic, max(opts.SeedLimit, 6))
	graph.Pivots = buildTopicPivots(graph)
	graph.Synthesis = synthesizeTopic(ctx, st, graph)

	return graph, nil
}

func FromDefinition(ctx context.Context, st *store.Store, def Definition) (TopicMap, error) {
	return Build(ctx, st, def.Topic, Options{
		SourceTypes:  def.SourceTypes,
		SeedLimit:    def.SeedLimit,
		RelatedLimit: def.RelatedLimit,
	})
}
