package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
)

func (s *Server) toolStatsItems(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		SourceType string `json:"source_type"`
		GroupBy    string `json:"group_by"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode stats items args: %w", err)
	}
	groupBy := firstNonEmpty(args.GroupBy, "source-type")
	buckets, err := s.st.CountItems(ctx, args.SourceType, groupBy)
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{
		"source_type": strings.TrimSpace(args.SourceType),
		"group_by":    groupBy,
		"buckets":     buckets,
		"total":       countBucketTotal(buckets),
	}
	return toolOKResult(formatCountBuckets(groupBy, buckets), payload), nil
}

func (s *Server) toolStatsSources(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		SourceType    string `json:"source_type"`
		ExtractTool   string `json:"extract_tool"`
		SummaryStatus string `json:"summary_status"`
		ExtractStatus string `json:"extract_status"`
		GroupBy       string `json:"group_by"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode stats sources args: %w", err)
	}
	groupBy := firstNonEmpty(args.GroupBy, "source-type")
	filter := store.SourceCountFilter{
		SourceType:    strings.TrimSpace(args.SourceType),
		ExtractTool:   strings.TrimSpace(args.ExtractTool),
		SummaryStatus: strings.TrimSpace(args.SummaryStatus),
		ExtractStatus: strings.TrimSpace(args.ExtractStatus),
	}
	buckets, err := s.st.CountSources(ctx, filter, groupBy)
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{
		"filter":   filter,
		"group_by": groupBy,
		"buckets":  buckets,
		"total":    countBucketTotal(buckets),
	}
	return toolOKResult(formatCountBuckets(groupBy, buckets), payload), nil
}

func (s *Server) toolStatsActivity(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		WindowSeconds int `json:"window_seconds"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode stats activity args: %w", err)
	}
	window := secondsTimeout(args.WindowSeconds)
	if window <= 0 {
		window = 15 * time.Minute
	}
	stats, err := s.st.Activity(ctx, timeNowUTC(), window)
	if err != nil {
		return nil, err
	}
	text := fmt.Sprintf("Latest source summary: %s\nSources summarized in window: %d", stats.LatestSourceSummaryAt.Format("2006-01-02T15:04:05Z07:00"), stats.SourcesSummarizedInWindow)
	return toolOKResult(text, stats), nil
}

func (s *Server) toolStatsBacklog(ctx context.Context) (map[string]interface{}, error) {
	stats, err := s.st.Backlog(ctx, sourceenrich.SummaryPromptVersion, "", "")
	if err != nil {
		return nil, err
	}
	text := fmt.Sprintf("Queue drained: %t\nSource extraction pending: %d\nSource summary pending: %d", stats.Drained, stats.SourceExtractionPending, stats.SourceSummaryPending)
	return toolOKResult(text, stats), nil
}
