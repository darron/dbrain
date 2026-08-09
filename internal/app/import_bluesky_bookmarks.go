package app

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/bskyapi"
	"github.com/darron/dbrain/internal/store"
)

func newImportBlueskyCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bluesky",
		Short: "Import Bluesky signals",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newImportBlueskyBookmarksCommand(root))
	return cmd
}

func newImportBlueskyBookmarksCommand(root *rootOptions) *cobra.Command {
	var browser string
	var profile string
	var limit int
	var pageSize int
	var maxPages int
	var timeout time.Duration
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "bookmarks",
		Short: "Import Bluesky bookmarks from a Chrome profile session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			stats, err := bskyapi.RunBookmarks(cmd.Context(), cfg, st, bskyapi.BookmarkOptions{
				Browser:  browser,
				Profile:  profile,
				Limit:    limit,
				PageSize: pageSize,
				MaxPages: maxPages,
				Timeout:  timeout,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Pages fetched: %d\n", stats.PagesFetched)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Bookmarks seen: %d\n", stats.Seen)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Bookmarks processed: %d\n", stats.Processed)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Skipped: %d\n", stats.Skipped)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created: %d\n", stats.Created)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated: %d\n", stats.Updated)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Unchanged: %d\n", stats.Unchanged)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Rendered notes: %d\n", stats.Rendered)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media discovered: %d\n", stats.MediaDiscovered)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media linked: %d\n", stats.MediaLinked)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media unavailable: %d\n", stats.MediaUnavailable)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media downloaded: %d\n", stats.MediaDownloaded)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media gone: %d\n", stats.MediaGone)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media errors: %d\n", stats.MediaErrors)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media blocked: %d\n", stats.MediaBlocked)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Stopped: %s\n", stats.StoppedReason)
			return nil
		},
	}

	cmd.Flags().StringVar(&browser, "browser", "chrome", "Chrome browser profile used for Bluesky session storage")
	cmd.Flags().StringVar(&profile, "profile", "", "Chrome profile name or absolute profile directory")
	cmd.Flags().IntVar(&limit, "limit", 0, "Optional maximum number of bookmark entries to examine")
	cmd.Flags().IntVar(&pageSize, "page-size", 100, "Bookmarks to request per Bluesky page (maximum 100)")
	cmd.Flags().IntVar(&maxPages, "max-pages", 0, "Optional maximum number of pages to fetch")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "Timeout for Bluesky API requests")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print import stats as JSON")
	return cmd
}
