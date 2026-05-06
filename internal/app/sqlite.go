package app

import (
	"github.com/spf13/cobra"
)

func newSQLiteCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sqlite",
		Short: "Manage the local SQLite database",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newSQLiteArchiveCommand(root), newSQLiteRestoreCommand(root))
	return cmd
}
