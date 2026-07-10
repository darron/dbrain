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
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			resolvedFlags, err := resolveSyncAllFlags(cfg.RootDir, flags, syncAllOverridesFromCommand(cmd))
			if err != nil {
				return err
			}
			if err := cfg.EnsureDirs(); err != nil {
				return err
			}
			metricsRun, closeMetrics, err := openSyncMetrics(cfg, "cli")
			if err != nil {
				return err
			}
			defer func() {
				if closeErr := closeMetrics(); err == nil && closeErr != nil {
					err = closeErr
				}
			}()
			lock, err := acquireSyncAllLock(cfg, "cli")
			if err != nil {
				emitSyncMetricsSkipped(metricsRun, err)
				return err
			}
			defer func() {
				_ = lock.Close()
			}()

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

			options, err := syncOptionsFromFlags(cmd.Context(), cfg, resolvedFlags, newLogger(commandDebugEnabled(cmd), logWriter), progress)
			if err != nil {
				return err
			}
			options.Metrics = metricsRun
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

func syncAllOverridesFromCommand(cmd *cobra.Command) syncAllFlagOverrides {
	if cmd == nil {
		return syncAllFlagOverrides{}
	}
	return syncAllFlagOverrides{
		skipXBookmarks: cmd.Flags().Changed("skip-x-bookmarks"),
		skipX:          cmd.Flags().Changed("skip-x"),
		skipXMedia:     cmd.Flags().Changed("skip-x-media"),
		skipXPhotoOCR:  cmd.Flags().Changed("skip-x-photo-ocr"),
		skipGitHub:     cmd.Flags().Changed("skip-github"),
		skipYouTube:    cmd.Flags().Changed("skip-youtube"),
		skipFeeds:      cmd.Flags().Changed("skip-feeds"),
		skipCategorize: cmd.Flags().Changed("skip-categorize"),
		watchLater:     cmd.Flags().Changed("watch-later"),
		liked:          cmd.Flags().Changed("liked"),
		appleNotes:     cmd.Flags().Changed("apple-notes"),
		safariTabs:     cmd.Flags().Changed("safari-tabs"),
		browser:        cmd.Flags().Changed("browser"),
		profile:        cmd.Flags().Changed("profile"),
	}
}
