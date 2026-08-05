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
var closeSyncStore = func(st *store.Store) error { return st.Close() }

func newSyncCommand(root *rootOptions) *cobra.Command {
	return newSyncCommandWithSemanticDeps(root, defaultSemanticRefreshDeps())
}

func newSyncCommandWithSemanticDeps(root *rootOptions, deps semanticRefreshDeps) *cobra.Command {
	completion := &syncCommandCompletion{}
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Run multi-stage refresh flows",
		RunE:  helpCommand,
		PersistentPostRunE: func(cmd *cobra.Command, _ []string) (returnErr error) {
			completed := completion.consume()
			if completed == nil {
				return nil
			}
			defer func() {
				returnErr = errors.Join(returnErr, completed.close())
			}()
			progressOut := cmd.ErrOrStderr()
			if completed.ui != nil {
				progressOut = completed.ui
			}
			reporter := newSemanticProgressReporter(progressOut, time.Now)
			semanticStartedAt := time.Now().UTC()
			metricsErr := emitSemanticRefreshStarted(completed.metrics, semanticStartedAt)
			refreshDeps := deps
			refreshDeps.startedAt = semanticStartedAt
			result, err := runConfiguredSemanticRefresh(
				cmd.Context(),
				completed.cfg,
				refreshDeps,
				reporter.Callback(),
			)
			err = errors.Join(err, reporter.Finish(err == nil))
			elapsed := result.Duration
			completed.stats = completeSyncStatsWithSemantic(completed.stats, result)
			metricsErr = errors.Join(metricsErr, emitFullSyncCompletion(completed.metrics, completed.stats, result, err))
			if completed.closeMetrics != nil {
				metricsErr = errors.Join(metricsErr, completed.closeMetrics())
				completed.closeMetrics = nil
			}
			if completed.ui != nil {
				completed.ui.Close()
				completed.ui = nil
			}
			if err != nil {
				var refreshErr *semanticrefresh.RefreshError
				if !errors.As(err, &refreshErr) {
					return errors.Join(err, metricsErr)
				}
				if completed.jsonOut {
					if writeErr := writeSyncSemanticErrorJSON(
						cmd.OutOrStdout(),
						completed.stats,
						result,
						refreshErr,
					); writeErr != nil {
						return errors.Join(writeErr, metricsErr)
					}
					return &ExitError{Code: 1, Err: errors.Join(refreshErr, metricsErr), Silent: true}
				}
				if writeErr := writeSyncStatsWithSemantic(cmd.OutOrStdout(), completed.stats, result, refreshErr); writeErr != nil {
					return errors.Join(writeErr, metricsErr)
				}
				return &ExitError{
					Code:   1,
					Err:    errors.Join(semanticRefreshHumanError{refreshErr: refreshErr, elapsed: elapsed}, metricsErr),
					Silent: false,
				}
			}
			if metricsErr != nil {
				return metricsErr
			}

			if completed.jsonOut {
				return writeSyncSemanticResultJSON(cmd.OutOrStdout(), completed.stats, result)
			}
			if err := writeSyncStatsWithSemantic(cmd.OutOrStdout(), completed.stats, result, nil); err != nil {
				return err
			}
			return nil
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
			defer func() {
				if lock != nil {
					_ = lock.Close()
				}
			}()

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
			defer func() { _ = closeSyncStore(st) }()

			options, err := syncOptionsFromFlags(cmd.Context(), cfg, resolvedFlags, newLogger(commandDebugEnabled(cmd), logWriter), progress)
			if err != nil {
				return err
			}
			options.Metrics = metricsRun
			options.ParentOwnsRunCompletion = completion != nil
			stats, err := runSyncAll(cmd.Context(), cfg, st, options)
			if err != nil {
				if completion != nil {
					syncjob.EmitRunCompleted(metricsRun, stats, err)
				}
				if closeErr := closeMetrics(); closeErr != nil {
					closeMetrics = func() error { return nil }
					return errors.Join(err, closeErr)
				}
				closeMetrics = func() error { return nil }
				return err
			}

			if closeErr := closeSyncStore(st); closeErr != nil {
				if completion != nil {
					syncjob.EmitRunCompleted(metricsRun, stats, closeErr)
				}
				if metricsCloseErr := closeMetrics(); metricsCloseErr != nil {
					closeErr = errors.Join(closeErr, metricsCloseErr)
				}
				closeMetrics = func() error { return nil }
				return closeErr
			}
			st = nil
			if completion != nil {
				completion.record(syncCommandCompleted{
					cfg:          cfg,
					stats:        stats,
					jsonOut:      resolvedFlags.jsonOut,
					lock:         lock,
					metrics:      metricsRun,
					closeMetrics: closeMetrics,
					ui:           syncUI,
				})
				lock = nil
				syncUI = nil
				closeMetrics = func() error { return nil }
				return nil
			}
			if syncUI != nil {
				syncUI.Close()
				syncUI = nil
			}
			if err := closeMetrics(); err != nil {
				return err
			}
			closeMetrics = func() error { return nil }
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
