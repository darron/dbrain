package app

import (
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
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

			promptVersion := ""
			toolName := ""
			toolVersion := ""
			if strings.TrimSpace(model) != "" {
				promptVersion = sourceenrich.SummaryPromptVersion
				toolName = summarizecli.SummaryToolName(model)
				toolVersion = summarizecli.SummaryToolVersion(cmd.Context(), "", model)
			}

			stats, err := st.Backlog(cmd.Context(), promptVersion, toolName, toolVersion)
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

			promptVersion := ""
			toolName := ""
			toolVersion := ""
			if strings.TrimSpace(model) != "" {
				promptVersion = sourceenrich.SummaryPromptVersion
				toolName = summarizecli.SummaryToolName(model)
				toolVersion = summarizecli.SummaryToolVersion(cmd.Context(), "", model)
			}

			stats, err := st.Pipeline(cmd.Context(), promptVersion, toolName, toolVersion)
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
