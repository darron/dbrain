package app

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
)

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

			promptVersion, toolName, toolVersion := statsSummaryFreshness(cmd, model)
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

			promptVersion, toolName, toolVersion := statsSummaryFreshness(cmd, model)
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

func statsSummaryFreshness(cmd *cobra.Command, model string) (string, string, string) {
	if strings.TrimSpace(model) == "" {
		return "", "", ""
	}
	return sourceenrich.SummaryPromptVersion,
		summarizecli.SummaryToolName(model),
		summarizecli.SummaryToolVersion(cmd.Context(), "", model)
}
