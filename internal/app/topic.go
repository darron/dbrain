package app

import (
	"github.com/spf13/cobra"
)

func newTopicCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "topic",
		Short: "Build and write topic maps from the local brain",
		RunE:  helpCommand,
	}
	cmd.AddCommand(
		newTopicMapCommand(root),
		newTopicGenerateCommand(root),
		newTopicRefreshCommand(root),
		newTopicIndexCommand(root),
	)
	return cmd
}
