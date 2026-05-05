package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/topics"
	"github.com/darron/dbrain/internal/vault"
)

func (s *Server) toolEntityMap(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Query string `json:"query"`
		Kind  string `json:"kind"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode entity map args: %w", err)
	}
	results, err := entities.Search(ctx, s.st, strings.TrimSpace(args.Query), entities.SearchOptions{
		Kind:  args.Kind,
		Limit: defaultInt(args.Limit, 20),
	})
	if err != nil {
		return nil, err
	}
	return toolOKResult(entities.FormatText(results), map[string]interface{}{
		"count":    len(results),
		"entities": results,
	}), nil
}

func (s *Server) toolTopicMap(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Topic        string   `json:"topic"`
		SourceTypes  []string `json:"source_types"`
		SeedLimit    int      `json:"seed_limit"`
		RelatedLimit int      `json:"related_limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode topic map args: %w", err)
	}
	graph, err := topics.Build(ctx, s.st, args.Topic, topics.Options{
		SourceTypes:  args.SourceTypes,
		SeedLimit:    args.SeedLimit,
		RelatedLimit: args.RelatedLimit,
	})
	if err != nil {
		return nil, err
	}
	return toolOKResult(topics.FormatText(graph), graph), nil
}

func (s *Server) toolTopicBrief(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Topic        string   `json:"topic"`
		SourceTypes  []string `json:"source_types"`
		SeedLimit    int      `json:"seed_limit"`
		RelatedLimit int      `json:"related_limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode topic brief args: %w", err)
	}
	graph, err := topics.Build(ctx, s.st, args.Topic, topics.Options{
		SourceTypes:  args.SourceTypes,
		SeedLimit:    args.SeedLimit,
		RelatedLimit: args.RelatedLimit,
	})
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{
		"topic":         graph.Topic,
		"source_types":  graph.SourceTypes,
		"seed_limit":    graph.SeedLimit,
		"related_limit": graph.RelatedLimit,
		"summary":       topics.SummaryText(graph),
		"synthesis":     graph.Synthesis,
		"pivots":        graph.Pivots,
		"entities":      graph.Entities,
		"nodes":         graph.Nodes,
		"edges":         graph.Edges,
		"markdown":      vault.RenderTopic(graph),
	}
	return toolOKResult(payload["markdown"].(string), payload), nil
}

func (s *Server) toolRelated(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Lookup string `json:"lookup"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode related args: %w", err)
	}
	lookup := strings.TrimSpace(args.Lookup)
	if lookup == "" {
		return nil, fmt.Errorf("lookup is required")
	}

	if item, err := s.st.GetItem(ctx, lookup); err == nil {
		related, err := s.st.ListSourcesForItem(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		childIDs, err := s.st.ListItemChildLinks(ctx, item.ID, "quoted_post")
		if err != nil {
			return nil, err
		}
		relatedItems := make([]retrieval.RelatedDocument, 0, len(childIDs))
		for _, childID := range childIDs {
			child, err := s.st.GetItemByID(ctx, childID)
			if err != nil {
				continue
			}
			relatedItems = append(relatedItems, relatedDocument(child))
		}
		payload := map[string]interface{}{
			"kind":            "item",
			"lookup":          lookup,
			"item":            slimItem(item),
			"related_sources": related,
			"related_items":   relatedItems,
			"count":           len(related) + len(relatedItems),
		}
		return toolOKResult(formatRelatedItemGraph(item.SourceKey, related, relatedItems), payload), nil
	}

	source, err := s.st.GetSource(ctx, lookup)
	if err != nil {
		return nil, lookupNotFoundError(lookup)
	}
	backlinks, err := s.st.ListBacklinksForSource(ctx, source.ID)
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{
		"kind":      "source",
		"lookup":    lookup,
		"source":    slimSource(source),
		"backlinks": backlinks,
		"count":     len(backlinks),
	}
	return toolOKResult(formatBacklinks(source.SourceKey, backlinks), payload), nil
}
