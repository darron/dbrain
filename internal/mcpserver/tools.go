package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
)

func (s *Server) handleToolCall(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("decode tools/call params: %w", err)
	}

	switch params.Name {
	case "dbrain_search":
		return s.toolSearch(ctx, params.Arguments)
	case "dbrain_get":
		return s.toolGet(ctx, params.Arguments)
	case "dbrain_get_many":
		return s.toolGetMany(ctx, params.Arguments)
	case "dbrain_okf_search":
		return s.toolOKFSearch(params.Arguments)
	case "dbrain_okf_get":
		return s.toolOKFGet(params.Arguments)
	case "dbrain_research_pack":
		return s.toolResearchPack(ctx, params.Arguments)
	case "dbrain_entity_map":
		return s.toolEntityMap(ctx, params.Arguments)
	case "dbrain_topic_map":
		return s.toolTopicMap(ctx, params.Arguments)
	case "dbrain_topic_brief":
		return s.toolTopicBrief(ctx, params.Arguments)
	case "dbrain_related":
		return s.toolRelated(ctx, params.Arguments)
	case "dbrain_whats_new":
		return s.toolWhatsNew(ctx, params.Arguments)
	case "dbrain_stats_items":
		return s.toolStatsItems(ctx, params.Arguments)
	case "dbrain_stats_sources":
		return s.toolStatsSources(ctx, params.Arguments)
	case "dbrain_stats_activity":
		return s.toolStatsActivity(ctx, params.Arguments)
	case "dbrain_stats_backlog":
		return s.toolStatsBacklog(ctx)
	default:
		return nil, fmt.Errorf("unknown tool %q", params.Name)
	}
}
