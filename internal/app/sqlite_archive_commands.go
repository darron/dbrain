package app

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/sqlitearchive"
)

func newSQLiteArchiveCommand(root *rootOptions) *cobra.Command {
	var opts sqliteArchiveFlags
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "archive",
		Short: "Snapshot, compress, and upload the local SQLite database",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			store, prefix, err := buildSQLiteArchiveStore(cmd.Context(), cfg.RootDir, opts)
			if err != nil {
				return err
			}
			var progressFn func(sqlitearchive.Event)
			if !jsonOut {
				ui := newCLIProgressUI(cmd.ErrOrStderr())
				defer ui.stopActive(false, "")
				progressFn = ui.Handle
			}
			result, err := sqlitearchive.Archive(cmd.Context(), cfg, sqlitearchive.Options{
				Prefix:   prefix,
				Store:    store,
				Progress: progressFn,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Archived SQLite database: %s\n", result.Key)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Local DB: %s\n", result.LocalDBPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Snapshot bytes: %d\n", result.SnapshotSize)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Compressed bytes: %d\n", result.ArchiveSize)
			return nil
		},
	}
	addSQLiteArchiveFlags(cmd, &opts)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print archive result as JSON")
	return cmd
}

func newSQLiteRestoreCommand(root *rootOptions) *cobra.Command {
	var opts sqliteArchiveFlags
	var yes bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Download and restore the newest archived SQLite database",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			store, prefix, err := buildSQLiteArchiveStore(cmd.Context(), cfg.RootDir, opts)
			if err != nil {
				return err
			}
			archiveOpts := sqlitearchive.Options{
				Prefix: prefix,
				Store:  store,
			}
			var ui *cliProgressUI
			if !jsonOut {
				ui = newCLIProgressUI(cmd.ErrOrStderr())
				defer ui.stopActive(false, "")
				archiveOpts.Progress = ui.Handle
				ui.Handle(sqlitearchive.Event{Kind: sqlitearchive.EventStageStart, Stage: "list", Message: "Finding newest SQLite archive"})
			}
			plan, err := sqlitearchive.Latest(cmd.Context(), archiveOpts)
			if err != nil {
				return err
			}
			if ui != nil {
				ui.Handle(sqlitearchive.Event{Kind: sqlitearchive.EventStageDone, Stage: "list", Message: fmt.Sprintf("Found %s", plan.Object.Key)})
			}
			if !yes {
				ok, err := confirmRestore(cmd.InOrStdin(), cmd.OutOrStdout(), cfg.DBPath, plan.Object)
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Restore cancelled.")
					return nil
				}
			}
			result, err := sqlitearchive.Restore(cmd.Context(), cfg, plan, archiveOpts)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Restored SQLite database: %s\n", result.RestoredPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Archive key: %s\n", result.Key)
			for _, backupPath := range result.BackupPaths {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Moved existing file: %s\n", backupPath)
			}
			return nil
		},
	}
	addSQLiteArchiveFlags(cmd, &opts)
	cmd.Flags().BoolVar(&yes, "yes", false, "Restore without interactive confirmation")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print restore result as JSON")
	return cmd
}
