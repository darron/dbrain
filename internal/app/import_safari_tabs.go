package app

import (
	"fmt"
	"strings"
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
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			if err := cfg.EnsureDirs(); err != nil {
				return err
			}

			var st *store.Store
			if !dryRun {
				st, err = store.Open(cfg.DBPath)
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
	cmd.Flags().StringVar(&device, "device", "", "Safari iCloud device name or UUID to import, for example dfone")
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
			cfg, err := loadConfig(root.root)
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

func writeSafariTabsStats(dst interface{ Write([]byte) (int, error) }, stats safaritabs.Stats) {
	mode := "dry-run"
	if stats.Applied {
		mode = "applied"
	}
	_, _ = fmt.Fprintf(dst, "Mode:      %s\n", mode)
	_, _ = fmt.Fprintf(dst, "Device:    %s (%s)\n", emptyDash(stats.DeviceName), emptyDash(stats.DeviceUUID))
	_, _ = fmt.Fprintf(dst, "Seen:      %d\n", stats.TabsSeen)
	_, _ = fmt.Fprintf(dst, "Matched:   %d\n", stats.TabsMatched)
	_, _ = fmt.Fprintf(dst, "Imported:  %d\n", stats.TabsImported)
	_, _ = fmt.Fprintf(dst, "Created:   %d\n", stats.TabsCreated)
	_, _ = fmt.Fprintf(dst, "Updated:   %d\n", stats.TabsUpdated)
	_, _ = fmt.Fprintf(dst, "Unchanged: %d\n", stats.TabsUnchanged)
	_, _ = fmt.Fprintf(dst, "Rendered:  %d\n", stats.TabsRendered)
	_, _ = fmt.Fprintf(dst, "Skipped:   %d\n", stats.TabsSkipped)
	_, _ = fmt.Fprintf(dst, "Links:     %d\n", stats.LinksFound)
	_, _ = fmt.Fprintf(dst, "Errors:    %d\n", stats.Errors)
	if len(stats.SampleTitles) > 0 {
		_, _ = fmt.Fprintf(dst, "Sample titles:\n")
		for _, title := range stats.SampleTitles {
			_, _ = fmt.Fprintf(dst, "- %s\n", title)
		}
	}
}

func writeSafariTabsProgress(dst interface{ Write([]byte) (int, error) }, event safaritabs.ProgressEvent, showTitles bool) {
	if event.Phase == "" {
		return
	}
	if event.Phase == "loaded" {
		_, _ = fmt.Fprintf(dst, "Safari tabs loaded: candidates=%d\n", event.Total)
		return
	}
	position := ""
	if event.Index > 0 && event.Total > 0 {
		position = fmt.Sprintf(" %d/%d", event.Index, event.Total)
	}
	source := event.SourceKey
	if source == "" {
		source = "unknown"
	}
	title := ""
	if showTitles && strings.TrimSpace(event.Title) != "" {
		title = fmt.Sprintf(" title=%q", event.Title)
	}
	switch event.Phase {
	case "dry_run":
		_, _ = fmt.Fprintf(dst, "Safari Tab%s would_import source=%s url=%s%s\n", position, source, emptyDash(event.URL), title)
	case "imported":
		if event.Status == "unchanged" && !event.Rendered {
			return
		}
		_, _ = fmt.Fprintf(dst, "Safari Tab%s imported source=%s status=%s rendered=%t%s\n", position, source, emptyDash(event.Status), event.Rendered, title)
	case "skipped":
		if event.Reason == "newer_than_cutoff" {
			return
		}
		_, _ = fmt.Fprintf(dst, "Safari Tab%s skipped reason=%s url=%s%s\n", position, emptyDash(event.Reason), emptyDash(event.URL), title)
	}
}

func formatCLIOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
