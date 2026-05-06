package app

import (
	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/startuplog"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
)

var runSyncAll = syncjob.Run

func newSyncCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Run multi-stage refresh flows",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newSyncAllCommand(root))
	return cmd
}

func newSyncAllCommand(root *rootOptions) *cobra.Command {
	var flags syncAllFlags

	cmd := &cobra.Command{
		Use:   "all",
		Short: "Run the incremental brain refresh pipeline end to end",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			resolvedFlags, err := resolveSyncAllFlags(cfg.RootDir, flags)
			if err != nil {
				return err
			}

			progress := cmd.ErrOrStderr()
			logWriter := cmd.ErrOrStderr()
			var syncUI *syncProgressUI
			if !resolvedFlags.jsonOut {
				syncUI = newSyncProgressUI(cmd.ErrOrStderr())
				defer syncUI.Close()
				progress = syncUI
				logWriter = syncUI.LogWriter()
			}

			startuplog.WriteVersion(progress)
			st, err := store.OpenWithOptions(cfg.DBPath, store.OpenOptions{
				MigrationReporter: startuplog.MigrationReporter(progress),
			})
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			options := syncOptionsFromFlags(cfg, resolvedFlags, newLogger(commandDebugEnabled(cmd), logWriter), progress)
			stats, err := runSyncAll(cmd.Context(), cfg, st, options)
			if err != nil {
				return err
			}

			if resolvedFlags.jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}

			return writeSyncStats(cmd.OutOrStdout(), stats)
		},
	}

	bindSyncAllFlags(cmd, &flags)

	return cmd
}
