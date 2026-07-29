package app

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/safaritabs"
	"github.com/darron/dbrain/internal/store"
)

func newImportSafariTabsCommand(root *rootOptions) *cobra.Command {
	var dbPath string
	var device string
	var limit int
	var olderThan time.Duration
	var dryRun bool
	var force bool
	var showTitles bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "safari-tabs",
		Short: "Import Safari iCloud tabs from the local CloudTabs SQLite store",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			if err := cfg.EnsureDirs(); err != nil {
				return err
			}

			var st *store.Store
			if !dryRun {
				st, err = store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
				if err != nil {
					return err
				}
				defer func() {
					_ = st.Close()
				}()
			}

			var progress safaritabs.ProgressFunc
			if !jsonOut {
				progress = func(event safaritabs.ProgressEvent) {
					writeSafariTabsProgress(cmd.OutOrStdout(), event, showTitles)
				}
			}

			stats, err := safaritabs.Run(cmd.Context(), cfg, st, safaritabs.Options{
				DBPath:     dbPath,
				Device:     device,
				Limit:      limit,
				OlderThan:  olderThan,
				DryRun:     dryRun,
				Force:      force,
				ShowTitles: showTitles,
				Progress:   progress,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}
			writeSafariTabsStats(cmd.OutOrStdout(), stats)
			return nil
		},
	}
	cmd.AddCommand(newImportSafariTabsDevicesCommand(root))
	cmd.Flags().StringVar(&dbPath, "db", "", "Safari CloudTabs.db path override")
	cmd.Flags().StringVar(&device, "device", "", "Safari iCloud device name or UUID to import, for example phone")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum tabs to import after filtering")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0, "Only import tabs last viewed before this duration ago; 0 imports all matching tabs")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview matching Safari tabs without storing content")
	cmd.Flags().BoolVar(&force, "force", false, "Re-render matching Safari tab notes even when unchanged")
	cmd.Flags().BoolVar(&showTitles, "show-titles", false, "Allow output to show Safari tab titles")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print import stats as JSON")
	return cmd
}

func newImportSafariTabsDevicesCommand(root *rootOptions) *cobra.Command {
	var dbPath string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "devices",
		Short: "List Safari iCloud tab devices visible on this Mac",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			if err := cfg.EnsureDirs(); err != nil {
				return err
			}
			devices, snapshot, err := safaritabs.ListDevices(cmd.Context(), cfg, safaritabs.Options{DBPath: dbPath})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), struct {
					SourceDBPath string                  `json:"source_db_path"`
					Snapshot     safaritabs.SnapshotInfo `json:"snapshot"`
					Devices      []safaritabs.Device     `json:"devices"`
				}{
					SourceDBPath: snapshot.SourceDBPath,
					Snapshot:     snapshot,
					Devices:      devices,
				})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Safari CloudTabs DB: %s\n", snapshot.SourceDBPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Devices:\n")
			for _, device := range devices {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "- name=%q uuid=%s type=%s tabs=%d oldest=%s newest=%s\n",
					device.Name,
					device.UUID,
					emptyDash(device.TypeIdentifier),
					device.TabCount,
					emptyDash(formatCLIOptionalTime(device.OldestTabLastViewed)),
					emptyDash(formatCLIOptionalTime(device.NewestTabLastViewed)),
				)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "Safari CloudTabs.db path override")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print devices as JSON")
	return cmd
}
