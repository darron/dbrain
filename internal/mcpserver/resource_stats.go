package mcpserver

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
)

func (s *Server) readStatsResource(ctx context.Context, uri string, parsed *url.URL) ([]map[string]string, error) {
	switch strings.Trim(parsed.Path, "/") {
	case "activity":
		window := secondsTimeout(intFromQuery(parsed.Query(), "window_seconds"))
		if window <= 0 {
			window = 15 * time.Minute
		}
		stats, err := s.st.Activity(ctx, timeNowUTC(), window)
		if err != nil {
			return nil, err
		}
		return jsonResourceContents(uri, stats)
	case "backlog":
		stats, err := s.st.Backlog(ctx, sourceenrich.SummaryPromptVersion, "", "")
		if err != nil {
			return nil, err
		}
		return jsonResourceContents(uri, stats)
	case "items":
		groupBy := firstNonEmpty(strings.TrimSpace(parsed.Query().Get("group_by")), "source-type")
		sourceType := strings.TrimSpace(parsed.Query().Get("source_type"))
		buckets, err := s.st.CountItems(ctx, sourceType, groupBy)
		if err != nil {
			return nil, err
		}
		payload := map[string]interface{}{
			"source_type": sourceType,
			"group_by":    groupBy,
			"buckets":     buckets,
			"total":       countBucketTotal(buckets),
		}
		return jsonResourceContents(uri, payload)
	case "sources":
		groupBy := firstNonEmpty(strings.TrimSpace(parsed.Query().Get("group_by")), "source-type")
		filter := map[string]string{
			"source_type":    strings.TrimSpace(parsed.Query().Get("source_type")),
			"extract_tool":   strings.TrimSpace(parsed.Query().Get("extract_tool")),
			"summary_status": strings.TrimSpace(parsed.Query().Get("summary_status")),
			"extract_status": strings.TrimSpace(parsed.Query().Get("extract_status")),
		}
		buckets, err := s.st.CountSources(ctx, store.SourceCountFilter{
			SourceType:    filter["source_type"],
			ExtractTool:   filter["extract_tool"],
			SummaryStatus: filter["summary_status"],
			ExtractStatus: filter["extract_status"],
		}, groupBy)
		if err != nil {
			return nil, err
		}
		payload := map[string]interface{}{
			"filter":   filter,
			"group_by": groupBy,
			"buckets":  buckets,
			"total":    countBucketTotal(buckets),
		}
		return jsonResourceContents(uri, payload)
	default:
		return nil, fmt.Errorf("unknown stats resource %q", parsed.Path)
	}
}
