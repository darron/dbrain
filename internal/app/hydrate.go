package app

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/mediadownload"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/xapi"
)

func newHydrateXCommand(root *rootOptions) *cobra.Command {
	var limit int
	var concurrency int
	var force bool
	var browser string
	var profile string
	var ct0 string
	var authToken string
	var timeout time.Duration
	var mediaTimeout time.Duration
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "x",
		Short: "Hydrate canonical X post content",
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
			defer func() {
				_ = st.Close()
			}()

			stats, err := xapi.Run(cmd.Context(), cfg, st, xapi.Options{
				Limit:        limit,
				Force:        force,
				Concurrency:  concurrency,
				Browser:      browser,
				Profile:      profile,
				CT0:          ct0,
				AuthToken:    authToken,
				Timeout:      timeout,
				MediaTimeout: mediaTimeout,
				Logger:       newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr()),
			})
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Hydration candidates: %d\n", stats.Candidates)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Requested: %d\n", stats.Requested)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Hydrated: %d\n", stats.Hydrated)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Missing: %d\n", stats.Missing)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "API errors: %d\n", stats.APIErrors)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media candidates: %d\n", stats.MediaCandidates)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media requested: %d\n", stats.MediaRequested)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media downloaded: %d\n", stats.MediaDownloaded)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media gone: %d\n", stats.MediaGone)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media errors: %d\n", stats.MediaErrors)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Media blocked: %d\n", stats.MediaBlocked)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Rendered notes: %d\n", stats.Rendered)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Unchanged: %d\n", stats.Unchanged)
			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum items to hydrate")
	cmd.Flags().IntVar(&concurrency, "concurrency", 4, "Number of concurrent post fetches")
	cmd.Flags().BoolVar(&force, "force", false, "Refetch items even if they already have X API hydration")
	cmd.Flags().StringVar(&browser, "browser", "", "Preferred browser for cookie lookup (chrome, brave, chromium, edge, firefox, safari)")
	cmd.Flags().StringVar(&profile, "profile", "", "Browser profile override; requires --browser")
	cmd.Flags().StringVar(&ct0, "ct0", "", "Manual ct0 cookie override")
	cmd.Flags().StringVar(&authToken, "auth-token", "", "Manual auth_token cookie override")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "Timeout for browser helpers and X HTTP requests")
	cmd.Flags().DurationVar(&mediaTimeout, "media-timeout", mediadownload.DefaultTimeout, "Timeout for each X media file download")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print hydration stats as JSON")

	return cmd
}
