package app

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"dbrain/internal/store"
	"dbrain/internal/xapi"
)

func newImportXBookmarksCommand(root *rootOptions) *cobra.Command {
	var browser string
	var profile string
	var ct0 string
	var authToken string
	var limit int
	var pageSize int
	var maxPages int
	var stalePageLimit int
	var force bool
	var timeout time.Duration
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "x-bookmarks",
		Short: "Import X bookmarks directly with browser session cookies",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}

			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			stats, err := xapi.RunBookmarks(cmd.Context(), cfg, st, xapi.BookmarkOptions{
				Browser:        browser,
				Profile:        profile,
				CT0:            ct0,
				AuthToken:      authToken,
				Limit:          limit,
				PageSize:       pageSize,
				MaxPages:       maxPages,
				StalePageLimit: stalePageLimit,
				Force:          force,
				Timeout:        timeout,
				Logger:         newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr()),
			})
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Bookmarks processed: %d\n", stats.Processed)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created: %d\n", stats.Created)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated: %d\n", stats.Updated)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Unchanged: %d\n", stats.Unchanged)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Rendered notes: %d\n", stats.Rendered)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Pages fetched: %d\n", stats.PagesFetched)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Stopped: %s\n", stats.StoppedReason)
			return nil
		},
	}

	cmd.Flags().StringVar(&browser, "browser", "chrome", "Preferred browser for cookie lookup")
	cmd.Flags().StringVar(&profile, "profile", "", "Browser profile override; requires --browser")
	cmd.Flags().StringVar(&ct0, "ct0", "", "Manual ct0 cookie override")
	cmd.Flags().StringVar(&authToken, "auth-token", "", "Manual auth_token cookie override")
	cmd.Flags().IntVar(&limit, "limit", 0, "Optional maximum number of bookmarks to process")
	cmd.Flags().IntVar(&pageSize, "page-size", 20, "Bookmarks to request per GraphQL page")
	cmd.Flags().IntVar(&maxPages, "max-pages", 0, "Optional maximum number of pages to fetch")
	cmd.Flags().IntVar(&stalePageLimit, "stale-page-limit", 2, "Stop after this many consecutive pages with no newly created bookmarks")
	cmd.Flags().BoolVar(&force, "force", false, "Ignore overlap stop logic and continue fetching until the limit or max-pages is reached")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "Timeout for browser cookie lookup and X requests")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print import stats as JSON")

	return cmd
}
