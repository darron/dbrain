package app

import (
	"errors"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/semanticrefresh"
	"github.com/darron/dbrain/internal/startuplog"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/syncjob"
)

var runSyncAll = syncjob.Run

func newSyncCommand(root *rootOptions) *cobra.Command {
	return newSyncCommandWithSemanticDeps(root, defaultSemanticRefreshDeps())
}

func newSyncCommandWithSemanticDeps(root *rootOptions, deps semanticRefreshDeps) *cobra.Command {
	completion := &syncCommandCompletion{}
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Run multi-stage refresh flows",
		RunE:  helpCommand,
		PersistentPostRunE: func(cmd *cobra.Command, _ []string) error {
			completed := completion.consume()
			if completed == nil {
				return nil
			}
			defer func() {
				_ = completed.lock.Close()
			}()
			started := time.Now()
			result, err := runConfiguredSemanticRefresh(
				cmd.Context(),
				completed.cfg,
				deps,
				func(progress semanticrefresh.Progress) error {
					return writeSemanticRefreshProgress(cmd.ErrOrStderr(), progress)
				},
			)
			elapsed := time.Since(started)
			if err != nil {
				var refreshErr *semanticrefresh.RefreshError
				if !errors.As(err, &refreshErr) {
					return err
				}
				if completed.jsonOut {
					if writeErr := writeSyncSemanticErrorJSON(
						cmd.OutOrStdout(),
						completed.stats,
						refreshErr,
					); writeErr != nil {
						return writeErr
					}
					return &ExitError{Code: 1, Err: refreshErr, Silent: true}
				}
				if writeErr := writeSyncStats(cmd.OutOrStdout(), completed.stats); writeErr != nil {
					return writeErr
				}
				return &ExitError{
					Code:   1,
					Err:    semanticRefreshHumanError{refreshErr: refreshErr, elapsed: elapsed},
					Silent: false,
				}
			}

			if completed.jsonOut {
				return writeSyncSemanticResultJSON(cmd.OutOrStdout(), completed.stats, result)
			}
			if err := writeSyncStats(cmd.OutOrStdout(), completed.stats); err != nil {
				return err
			}
			return writeSemanticRefreshResult(cmd.OutOrStdout(), result, elapsed)
		},
	}
	cmd.AddCommand(newSyncAllCommandWithCompletion(root, completion))
	return cmd
}

func newSyncAllCommand(root *rootOptions) *cobra.Command {
	return newSyncAllCommandWithCompletion(root, nil)
}

func newSyncAllCommandWithCompletion(root *rootOptions, completion *syncCommandCompletion) *cobra.Command {
	var flags syncAllFlags

	cmd := &cobra.Command{
		Use:   "all",
		Short: "Run the incremental brain refresh pipeline end to end",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			if completion != nil {
				completion.reset()
			}
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
			defer func() { _ = closeMetrics() }()
			lock, err := acquireSyncAllLock(cfg, "cli")
			if err != nil {
				emitSyncMetricsSkipped(metricsRun, err)
				return err
			}
			defer func() { _ = lock.Close() }()

			progress := cmd.ErrOrStderr()
			logWriter := cmd.ErrOrStderr()
			var syncUI *syncProgressUI
			if !resolvedFlags.jsonOut {
				syncUI = newSyncProgressUI(cmd.ErrOrStderr())
				defer func() {
					if syncUI != nil {
						syncUI.Close()
					}
				}()
				progress = syncUI
				logWriter = syncUI.LogWriter()
			}

			startuplog.WriteVersion(progress)
			st, err := store.OpenWithSemanticCacheOptions(cfg.DBPath, cfg.CacheDir, store.OpenOptions{
				MigrationReporter: startuplog.MigrationReporter(progress),
			})
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			options, err := syncOptionsFromFlags(cmd.Context(), cfg, resolvedFlags, newLogger(commandDebugEnabled(cmd), logWriter), progress)
			if err != nil {
				return err
			}
			options.Metrics = metricsRun
			stats, err := runSyncAll(cmd.Context(), cfg, st, options)
			if err != nil {
				return err
			}

			if err := st.Close(); err != nil {
				return err
			}
			st = nil
			if syncUI != nil {
				syncUI.Close()
				syncUI = nil
			}
			if err := closeMetrics(); err != nil {
				return err
			}
			closeMetrics = func() error { return nil }

			if completion != nil {
				completion.record(syncCommandCompleted{
					cfg:     cfg,
					stats:   stats,
					jsonOut: resolvedFlags.jsonOut,
					lock:    lock,
				})
				lock = nil
				return nil
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
