package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dbrain/internal/sourceenrich"
	"dbrain/internal/store"
	"dbrain/internal/summarizecli"
)

func newStatsCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show database counts and import progress",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newStatsItemsCommand(root), newStatsSourcesCommand(root), newStatsActivityCommand(root), newStatsBacklogCommand(root), newStatsPipelineCommand(root))
	return cmd
}

func newStatsItemsCommand(root *rootOptions) *cobra.Command {
	var sourceType string
	var groupBy string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "items",
		Short: "Show item counts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}

			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			buckets, err := st.CountItems(cmd.Context(), sourceType, groupBy)
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), buckets)
			}
			return writeCountBuckets(cmd.OutOrStdout(), groupBy, buckets)
		},
	}

	cmd.Flags().StringVar(&sourceType, "source-type", "", "Optional item source type filter")
	cmd.Flags().StringVar(&groupBy, "group-by", "source-type", "Grouping: source-type or none")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print counts as JSON")
	return cmd
}

func newStatsSourcesCommand(root *rootOptions) *cobra.Command {
	var sourceType string
	var extractTool string
	var summaryStatus string
	var extractStatus string
	var groupBy string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "sources",
		Short: "Show source counts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}

			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			buckets, err := st.CountSources(cmd.Context(), store.SourceCountFilter{
				SourceType:    sourceType,
				ExtractTool:   extractTool,
				SummaryStatus: summaryStatus,
				ExtractStatus: extractStatus,
			}, groupBy)
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), buckets)
			}
			return writeCountBuckets(cmd.OutOrStdout(), groupBy, buckets)
		},
	}

	cmd.Flags().StringVar(&sourceType, "source-type", "", "Optional source type filter")
	cmd.Flags().StringVar(&extractTool, "extract-tool", "", "Optional extract tool filter")
	cmd.Flags().StringVar(&summaryStatus, "summary-status", "", "Optional summary status filter")
	cmd.Flags().StringVar(&extractStatus, "extract-status", "", "Optional extract status filter")
	cmd.Flags().StringVar(&groupBy, "group-by", "source-type", "Grouping: source-type, summary-status, extract-status, or none")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print counts as JSON")
	return cmd
}

func newStatsActivityCommand(root *rootOptions) *cobra.Command {
	var window time.Duration
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Show recent database write activity",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}

			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			stats, err := st.Activity(cmd.Context(), time.Now().UTC(), window)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}
			return writeActivityStats(cmd.OutOrStdout(), stats)
		},
	}

	cmd.Flags().DurationVar(&window, "window", 15*time.Minute, "Recent activity window")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print activity as JSON")
	return cmd
}

func newStatsBacklogCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool
	var model string

	cmd := &cobra.Command{
		Use:   "backlog",
		Short: "Show remaining queued work by pipeline stage",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}

			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			stats, err := st.Backlog(
				cmd.Context(),
				sourceenrich.SummaryPromptVersion,
				summarizecli.SummaryToolName(model),
				summarizecli.SummaryToolVersion(cmd.Context(), "", model),
			)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}
			return writeBacklogStats(cmd.OutOrStdout(), stats)
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print backlog as JSON")
	cmd.Flags().StringVar(&model, "model", "", "Optional summary model when evaluating summary freshness")
	return cmd
}

func newStatsPipelineCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool
	var model string

	cmd := &cobra.Command{
		Use:   "pipeline",
		Short: "Show pipeline completion by stage and data type",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}

			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			stats, err := st.Pipeline(
				cmd.Context(),
				sourceenrich.SummaryPromptVersion,
				summarizecli.SummaryToolName(model),
				summarizecli.SummaryToolVersion(cmd.Context(), "", model),
			)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}
			return writePipelineStats(cmd.OutOrStdout(), stats, model)
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print pipeline stats as JSON")
	cmd.Flags().StringVar(&model, "model", "", "Optional summary model when evaluating summary freshness")
	return cmd
}

func writeCountBuckets(dst interface{ Write([]byte) (int, error) }, groupBy string, buckets []store.CountBucket) error {
	total := 0
	grouped := strings.TrimSpace(groupBy) != "none"
	for _, bucket := range buckets {
		total += bucket.Count
		if grouped {
			label := displayBucketKey(groupBy, bucket.Key)
			if _, err := fmt.Fprintf(dst, "%s: %d\n", label, bucket.Count); err != nil {
				return err
			}
			continue
		}
	}
	if grouped {
		_, err := fmt.Fprintf(dst, "Total: %d\n", total)
		return err
	}
	_, err := fmt.Fprintf(dst, "Count: %d\n", total)
	return err
}

func displayBucketKey(groupBy string, key string) string {
	value := strings.TrimSpace(key)
	if value != "" {
		return value
	}
	switch strings.TrimSpace(groupBy) {
	case "summary-status", "extract-status":
		return "pending"
	default:
		return "(empty)"
	}
}

func writeActivityStats(dst interface{ Write([]byte) (int, error) }, stats store.ActivityStats) error {
	lines := []struct {
		label string
		value string
	}{
		{"Now", stats.Now.Format(time.RFC3339)},
		{"Window", stats.Window},
		{"Latest item write", formatActivityTime(stats.Now, stats.LatestItemUpdatedAt)},
		{"Latest source write", formatActivityTime(stats.Now, stats.LatestSourceUpdatedAt)},
		{"Latest source summary", formatActivityTime(stats.Now, stats.LatestSourceSummaryAt)},
		{"Items updated in window", fmt.Sprintf("%d", stats.ItemsUpdatedInWindow)},
		{"Sources updated in window", fmt.Sprintf("%d", stats.SourcesUpdatedInWindow)},
		{"Sources summarized in window", fmt.Sprintf("%d", stats.SourcesSummarizedInWindow)},
	}

	for _, line := range lines {
		if _, err := fmt.Fprintf(dst, "%s: %s\n", line.label, line.value); err != nil {
			return err
		}
	}
	return nil
}

func formatActivityTime(now time.Time, value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	age := now.Sub(value).Round(time.Second)
	if age < 0 {
		age = 0
	}
	return fmt.Sprintf("%s (%s ago)", value.Format(time.RFC3339), age)
}

func writeBacklogStats(dst interface{ Write([]byte) (int, error) }, stats store.BacklogStats) error {
	drained := "no"
	if stats.Drained {
		drained = "yes"
	}

	lines := []struct {
		label string
		value string
	}{
		{"Queue drained", drained},
		{"X hydration pending", fmt.Sprintf("%d", stats.XHydrationPending)},
		{"Link discovery pending", fmt.Sprintf("%d", stats.LinkDiscoveryPending)},
		{"Source extraction pending", fmt.Sprintf("%d", stats.SourceExtractionPending)},
		{"Source summary pending", fmt.Sprintf("%d", stats.SourceSummaryPending)},
	}
	for _, line := range lines {
		if _, err := fmt.Fprintf(dst, "%s: %s\n", line.label, line.value); err != nil {
			return err
		}
	}

	if err := writeOptionalBucketSection(dst, "Source extraction backlog by type", stats.SourceExtractionPendingByType); err != nil {
		return err
	}
	if err := writeOptionalBucketSection(dst, "Source summary backlog by type", stats.SourceSummaryPendingByType); err != nil {
		return err
	}
	return nil
}

func writeOptionalBucketSection(dst interface{ Write([]byte) (int, error) }, title string, buckets []store.CountBucket) error {
	if len(buckets) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(dst, "%s:\n", title); err != nil {
		return err
	}
	for _, bucket := range buckets {
		if _, err := fmt.Fprintf(dst, "%s: %d\n", displayBucketKey("source-type", bucket.Key), bucket.Count); err != nil {
			return err
		}
	}
	return nil
}

func writePipelineStats(dst interface{ Write([]byte) (int, error) }, stats store.PipelineStats, model string) error {
	if _, err := fmt.Fprintf(dst, "Hydration\n"); err != nil {
		return err
	}
	if err := writePipelineTable(dst, stats.Hydration); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(dst, "\nExtraction\n"); err != nil {
		return err
	}
	if err := writePipelineTable(dst, stats.Extraction); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(dst, "\nSummary\n"); err != nil {
		return err
	}
	summaryTarget := strings.TrimSpace(model)
	if summaryTarget == "" {
		summaryTarget = "default"
	}
	if _, err := fmt.Fprintf(dst, "Model target: %s\n", summaryTarget); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Freshness target: %s / %s / %s\n", stats.SummaryPromptVersion, fallbackDisplay(stats.SummaryTool, "summary"), fallbackDisplay(stats.SummaryToolVersion, "unknown")); err != nil {
		return err
	}
	if err := writePipelineTable(dst, stats.Summary); err != nil {
		return err
	}

	if len(stats.Transcription) > 0 {
		if _, err := fmt.Fprintf(dst, "\nTranscription\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(dst, "X video items that still need transcript materialization before they are fully covered.\n"); err != nil {
			return err
		}
		if err := writePipelineTable(dst, stats.Transcription); err != nil {
			return err
		}
	}

	return nil
}

func writePipelineTable(dst interface{ Write([]byte) (int, error) }, rows []store.PipelineStageRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintf(dst, "(none)\n")
		return err
	}

	headers := []string{"Type", "Total", "Current", "Pending", "Blocked", "Failed", "Current %"}
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = len(header)
	}

	values := make([][]string, 0, len(rows))
	for _, row := range rows {
		record := []string{
			pipelineKindLabel(row.Kind),
			fmt.Sprintf("%d", row.Total),
			fmt.Sprintf("%d", row.Current),
			fmt.Sprintf("%d", row.Pending),
			fmt.Sprintf("%d", row.Blocked),
			fmt.Sprintf("%d", row.Failed),
			fmt.Sprintf("%.1f%%", row.PercentCurrent),
		}
		for i, value := range record {
			if len(value) > widths[i] {
				widths[i] = len(value)
			}
		}
		values = append(values, record)
	}

	if _, err := fmt.Fprintf(dst, "%s\n", formatPipelineTableRow(headers, widths)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "%s\n", formatPipelineTableDivider(widths)); err != nil {
		return err
	}
	for _, record := range values {
		if _, err := fmt.Fprintf(dst, "%s\n", formatPipelineTableRow(record, widths)); err != nil {
			return err
		}
	}
	return nil
}

func formatPipelineTableRow(values []string, widths []int) string {
	parts := make([]string, 0, len(values))
	for i, value := range values {
		align := "%-*s"
		if i > 0 {
			align = "%*s"
		}
		parts = append(parts, fmt.Sprintf(align, widths[i], value))
	}
	return "| " + strings.Join(parts, " | ") + " |"
}

func formatPipelineTableDivider(widths []int) string {
	parts := make([]string, 0, len(widths))
	for _, width := range widths {
		parts = append(parts, strings.Repeat("-", width))
	}
	return "|-" + strings.Join(parts, "-|-") + "-|"
}

func pipelineKindLabel(value string) string {
	switch strings.TrimSpace(value) {
	case "":
		return "(empty)"
	default:
		return value
	}
}

func fallbackDisplay(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
