package app

import (
	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
)

func newStatsItemsCommand(root *rootOptions) *cobra.Command {
	var sourceType string
	var groupBy string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "items",
		Short: "Show item counts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
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
			cfg, err := loadConfig(root.root, root.configFile)
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
