package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/feedimport"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
)

func newFeedCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "feed",
		Short: "Manage RSS, Atom, and JSON Feed subscriptions",
		RunE:  helpCommand,
	}
	cmd.AddCommand(
		newFeedAddCommand(root),
		newFeedListCommand(root),
		newFeedStatusCommand(root),
		newFeedCheckCommand(root),
		newFeedRefreshCommand(root),
		newFeedEnableCommand(root, true),
		newFeedEnableCommand(root, false),
	)
	return cmd
}

func newFeedAddCommand(root *rootOptions) *cobra.Command {
	var noFetch bool
	var disabled bool
	var check bool
	var tags string
	var pollInterval time.Duration
	var jsonOut bool
	var allowPrivateNetwork bool
	cmd := &cobra.Command{
		Use:   "add URL",
		Short: "Subscribe to a feed URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			if err := cfg.EnsureDirs(); err != nil {
				return err
			}
			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			feed, created, stats, err := feedimport.Add(cmd.Context(), cfg, st, args[0], feedimport.AddOptions{
				Disabled:            disabled,
				Import:              check,
				PollInterval:        pollInterval,
				UserTags:            tags,
				Fetch:               !noFetch && !disabled,
				AllowPrivateNetwork: allowPrivateNetwork || feedAllowPrivateNetworkFromRuntime(cfg.RootDir),
				Logger:              newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr()),
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"feed": feed, "created": created, "stats": stats})
			}
			status := "updated"
			if created {
				status = "created"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s\n", status, feed.FeedKey, feed.NormalizedURL)
			if !noFetch && !disabled {
				return writeFeedStats(cmd.OutOrStdout(), stats)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noFetch, "no-fetch", false, "Subscribe without immediately fetching the feed")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "Add the subscription disabled")
	cmd.Flags().BoolVar(&check, "check", false, "Immediately fetch and import available entries")
	cmd.Flags().StringVar(&tags, "tags", "", "Optional comma-separated user tags for imported feed entries")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", feedimport.DefaultPollInterval, "How often sync all should check this feed")
	cmd.Flags().BoolVar(&allowPrivateNetwork, "allow-private-network", false, "Allow localhost/private/link-local feed URLs for local testing")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print result as JSON")
	return cmd
}

func newFeedListCommand(root *rootOptions) *cobra.Command {
	var all bool
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List subscribed feeds",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			feeds, err := st.ListFeeds(cmd.Context(), all)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), feeds)
			}
			if len(feeds) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No feeds subscribed.")
				return nil
			}
			for _, feed := range feeds {
				enabled := "enabled"
				if !feed.Enabled {
					enabled = "disabled"
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\tlast=%s\tnext=%s\t%s\t%s\n",
					feed.FeedKey,
					enabled,
					feed.HealthStatus,
					feedDueStatus(feed, time.Now().UTC()),
					formatFeedCLITime(feed.LastCheckedAt),
					formatFeedCLITime(feed.NextFetchAfter),
					feed.Title,
					feed.NormalizedURL,
				)
				if feed.LastError != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\terror=%s\n", truncateFeedError(feed.LastError))
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Include disabled feeds")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print feeds as JSON")
	return cmd
}

func newFeedStatusCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status FEED",
		Short: "Show one feed subscription",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			feed, err := st.GetFeed(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), feed)
			}
			return writeFeed(cmd.OutOrStdout(), feed)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print feed as JSON")
	return cmd
}

func newFeedCheckCommand(root *rootOptions) *cobra.Command {
	var all bool
	var force bool
	var limit int
	var jsonOut bool
	var allowPrivateNetwork bool
	cmd := &cobra.Command{
		Use:   "check [FEED]",
		Short: "Fetch subscribed feeds now",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			if err := cfg.EnsureDirs(); err != nil {
				return err
			}
			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			opts := feedimport.Options{
				Force:               force,
				Limit:               limit,
				AllowPrivateNetwork: allowPrivateNetwork || feedAllowPrivateNetworkFromRuntime(cfg.RootDir),
				Logger:              newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr()),
			}
			var stats feedimport.Stats
			if len(args) == 1 {
				feed, err := st.GetFeed(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				stats, err = feedimport.CheckFeed(cmd.Context(), cfg, st, feed, opts)
				if err != nil {
					return err
				}
			} else {
				if all {
					opts.IncludeBlocked = true
				}
				stats, err = feedimport.Run(cmd.Context(), cfg, st, opts)
				if err != nil {
					return err
				}
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}
			return writeFeedStats(cmd.OutOrStdout(), stats)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Include blocked and dead feeds when checking all due feeds")
	cmd.Flags().BoolVar(&force, "force", false, "Process feed entries even when the feed body hash is unchanged")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum due feeds to check when no feed is specified")
	cmd.Flags().BoolVar(&allowPrivateNetwork, "allow-private-network", false, "Allow localhost/private/link-local feed URLs for local testing")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print result as JSON")
	return cmd
}

func newFeedRefreshCommand(root *rootOptions) *cobra.Command {
	var force bool
	var summarize bool
	var model string
	var cliProvider string
	var length string
	var timeout time.Duration
	var concurrency int
	var jsonOut bool
	var allowPrivateNetwork bool
	cmd := &cobra.Command{
		Use:   "refresh FEED",
		Short: "Fetch one feed and enrich its linked sources",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			if err := cfg.EnsureDirs(); err != nil {
				return err
			}
			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			feed, err := st.GetFeed(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			feedStats, err := feedimport.CheckFeed(cmd.Context(), cfg, st, feed, feedimport.Options{
				Force:               force,
				AllowPrivateNetwork: allowPrivateNetwork || feedAllowPrivateNetworkFromRuntime(cfg.RootDir),
				Logger:              newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr()),
			})
			if err != nil {
				return err
			}

			sourceStats := sourceenrich.Stats{}
			if len(feedStats.SourceIDs) > 0 {
				sourceStats, _, err = runSourceEnrichSourceIDs(cmd.Context(), cfg, st, feedStats.SourceIDs, sourceenrich.Options{
					Concurrency: concurrency,
					Force:       force,
					Summarize:   summarize,
					Model:       model,
					CLI:         cliProvider,
					Length:      length,
					Timeout:     timeout,
					Logger:      newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr()),
				})
				if err != nil {
					return err
				}
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"feed": feedStats, "sources": sourceStats})
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Feed")
			if err := writeFeedStats(cmd.OutOrStdout(), feedStats); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "")
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Sources")
			return writeSourceEnrichStats(cmd.OutOrStdout(), sourceStats)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Refresh feed and linked sources even when they look current")
	cmd.Flags().BoolVar(&summarize, "summarize", true, "Summarize linked sources after extraction")
	cmd.Flags().StringVar(&model, "model", "", "Optional summarize model override")
	cmd.Flags().StringVar(&cliProvider, "cli", defaultCLIProvider, "Summarize CLI provider")
	cmd.Flags().StringVar(&length, "length", "medium", "Summary length for summarize.sh")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Timeout for source extraction and summarization")
	cmd.Flags().IntVar(&concurrency, "concurrency", defaultExtractConcurrency, "Number of concurrent source extract/summarize jobs")
	cmd.Flags().BoolVar(&allowPrivateNetwork, "allow-private-network", false, "Allow localhost/private/link-local feed URLs for local testing")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print result as JSON")
	return cmd
}

func feedAllowPrivateNetworkFromRuntime(rootDir string) bool {
	return firstEnvBool(rootDir, "DBRAIN_FEEDS_ALLOW_PRIVATE_NETWORK", "DBRAIN_FEEDS_ALLOW_PRIVATE_NETWORKS")
}

func newFeedEnableCommand(root *rootOptions, enabled bool) *cobra.Command {
	use := "enable FEED"
	short := "Enable a feed and reset its health diagnostics"
	if !enabled {
		use = "disable FEED"
		short = "Disable a feed without deleting local entries"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			if err := st.EnableFeed(cmd.Context(), args[0], enabled); err != nil {
				return err
			}
			action := "Enabled"
			if !enabled {
				action = "Disabled"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s feed: %s\n", action, args[0])
			return nil
		},
	}
}

func writeFeed(dst interface{ Write([]byte) (int, error) }, feed store.Feed) error {
	_, _ = fmt.Fprintf(dst, "key: %s\n", feed.FeedKey)
	_, _ = fmt.Fprintf(dst, "url: %s\n", feed.NormalizedURL)
	_, _ = fmt.Fprintf(dst, "resolved_url: %s\n", feed.ResolvedURL)
	_, _ = fmt.Fprintf(dst, "title: %s\n", feed.Title)
	_, _ = fmt.Fprintf(dst, "enabled: %t\n", feed.Enabled)
	_, _ = fmt.Fprintf(dst, "health_status: %s\n", feed.HealthStatus)
	_, _ = fmt.Fprintf(dst, "due: %s\n", feedDueStatus(feed, time.Now().UTC()))
	_, _ = fmt.Fprintf(dst, "last_checked_at: %s\n", formatFeedCLITime(feed.LastCheckedAt))
	_, _ = fmt.Fprintf(dst, "next_fetch_after: %s\n", formatFeedCLITime(feed.NextFetchAfter))
	_, _ = fmt.Fprintf(dst, "last_error: %s\n", feed.LastError)
	return nil
}

func feedDueStatus(feed store.Feed, now time.Time) string {
	if !feed.Enabled {
		return "disabled"
	}
	if feed.HealthStatus == store.FeedHealthBlocked || feed.HealthStatus == store.FeedHealthDead {
		return feed.HealthStatus
	}
	if feed.NextFetchAfter.IsZero() || !feed.NextFetchAfter.After(now) {
		return "due"
	}
	return "not_due"
}

func formatFeedCLITime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

func truncateFeedError(value string) string {
	value = strings.TrimSpace(value)
	const limit = 160
	if len(value) <= limit {
		return value
	}
	return value[:limit-3] + "..."
}

func writeFeedStats(dst interface{ Write([]byte) (int, error) }, stats feedimport.Stats) error {
	_, _ = fmt.Fprintf(dst, "Feeds checked: %d\n", stats.FeedsChecked)
	_, _ = fmt.Fprintf(dst, "Feeds changed: %d\n", stats.FeedsChanged)
	_, _ = fmt.Fprintf(dst, "Feeds unchanged: %d\n", stats.FeedsUnchanged)
	_, _ = fmt.Fprintf(dst, "Feeds failed: %d\n", stats.FeedsFailed)
	_, _ = fmt.Fprintf(dst, "Entries seen: %d\n", stats.EntriesSeen)
	_, _ = fmt.Fprintf(dst, "Items created: %d\n", stats.ItemsCreated)
	_, _ = fmt.Fprintf(dst, "Items updated: %d\n", stats.ItemsUpdated)
	_, _ = fmt.Fprintf(dst, "Items unchanged: %d\n", stats.ItemsUnchanged)
	_, _ = fmt.Fprintf(dst, "Sources created: %d\n", stats.SourcesCreated)
	_, _ = fmt.Fprintf(dst, "Sources linked: %d\n", stats.SourcesLinked)
	_, _ = fmt.Fprintf(dst, "Items rendered: %d\n", stats.ItemsRendered)
	_, _ = fmt.Fprintf(dst, "Identity conflicts: %d\n", stats.IdentityConflicts)
	_, _ = fmt.Fprintf(dst, "Errors: %d\n", stats.Errors)
	return nil
}
