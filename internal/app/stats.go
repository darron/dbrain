package app

import (
	"github.com/spf13/cobra"
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
