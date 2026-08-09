package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/mastodonapi"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/store"
)

func newImportMastodonCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mastodon",
		Short: "Import Mastodon signals",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newImportMastodonBookmarksCommand(root))
	return cmd
}

func newImportMastodonBookmarksCommand(root *rootOptions) *cobra.Command {
	var accountKey string
	var limit int
	var timeout time.Duration
	var force bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "bookmarks",
		Short: "Import bookmarks from a configured Mastodon account",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			raw, ok := runtimeenv.ConfigMap(cfg.RootDir, "mastodon")
			if !ok {
				return fmt.Errorf("mastodon configuration is missing")
			}
			mastodonConfig, err := mastodonapi.ParseConfig(raw)
			if err != nil {
				return err
			}
			if !mastodonConfig.Enabled {
				return fmt.Errorf("mastodon is disabled in configuration")
			}
			account, err := mastodonAccountByKey(mastodonConfig, accountKey)
			if err != nil {
				return err
			}
			if !account.Enabled {
				return fmt.Errorf("mastodon account %q is disabled in configuration", account.Key)
			}
			accessToken, err := mastodonapi.ResolveTypedSecretRef(cmd.Context(), account.AccessTokenRef)
			if err != nil {
				return fmt.Errorf("resolve Mastodon access token for %q: %w", account.Key, err)
			}
			client, err := mastodonapi.NewClient(account.Origin, accessToken, nil)
			if err != nil {
				return err
			}
			st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			stats, err := mastodonapi.RunBookmarksWithClient(cmd.Context(), cfg, st, client, mastodonapi.BookmarkOptions{
				AccountKey: account.Key,
				Limit:      limit,
				Timeout:    timeout,
				Force:      force,
			})
			if err != nil {
				// Preserve the partial counters in both output modes. Cobra still
				// returns the operation error to the caller after the snapshot.
				if jsonOut {
					if writeErr := writeJSON(cmd.OutOrStdout(), stats); writeErr != nil {
						return fmt.Errorf("write partial Mastodon stats: %w (import error: %v)", writeErr, err)
					}
				} else {
					_ = writeMastodonBookmarkStats(cmd.OutOrStdout(), stats)
				}
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}
			if err := writeMastodonBookmarkStats(cmd.OutOrStdout(), stats); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&accountKey, "account", "", "Configured Mastodon account key")
	cmd.Flags().IntVar(&limit, "limit", 0, "Optional maximum number of bookmark statuses to process")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "Per-request timeout for Mastodon API calls; media downloads use their own bounded timeout")
	cmd.Flags().BoolVar(&force, "force", false, "Restart the account's bookmark backfill from the endpoint head and retry terminal blocked Mastodon media")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print import stats as JSON")
	_ = cmd.MarkFlagRequired("account")
	return cmd
}

func writeMastodonBookmarkStats(dst interface{ Write([]byte) (int, error) }, stats mastodonapi.BookmarkStats) error {
	for _, line := range []string{
		fmt.Sprintf("Account: %s (%s)\n", stats.AccountKey, stats.Origin),
		fmt.Sprintf("Pages fetched: %d\n", stats.PagesFetched),
		fmt.Sprintf("Bookmarks seen: %d\n", stats.Seen),
		fmt.Sprintf("Bookmarks processed: %d\n", stats.Processed),
		fmt.Sprintf("Skipped: %d\n", stats.Skipped),
		fmt.Sprintf("Skipped unsupported: %d\n", stats.SkippedUnsupported),
		fmt.Sprintf("Skipped malformed: %d\n", stats.SkippedMalformed),
		fmt.Sprintf("Created: %d\n", stats.Created),
		fmt.Sprintf("Updated: %d\n", stats.Updated),
		fmt.Sprintf("Unchanged: %d\n", stats.Unchanged),
		fmt.Sprintf("Rendered notes: %d\n", stats.Rendered),
		fmt.Sprintf("Media discovered: %d\n", stats.MediaDiscovered),
		fmt.Sprintf("Media linked: %d\n", stats.MediaLinked),
		fmt.Sprintf("Media unavailable: %d\n", stats.MediaUnavailable),
		fmt.Sprintf("Media downloaded: %d\n", stats.MediaDownloaded),
		fmt.Sprintf("Media gone: %d\n", stats.MediaGone),
		fmt.Sprintf("Media errors: %d\n", stats.MediaErrors),
		fmt.Sprintf("Media blocked: %d\n", stats.MediaBlocked),
		fmt.Sprintf("API errors: %d\n", stats.APIErrors),
		fmt.Sprintf("Rate limits: %d\n", stats.RateLimits),
		fmt.Sprintf("Retries: %d\n", stats.Retries),
		fmt.Sprintf("Stopped: %s\n", stats.StoppedReason),
	} {
		if _, err := dst.Write([]byte(line)); err != nil {
			return err
		}
	}
	return nil
}

func mastodonAccountByKey(cfg mastodonapi.Config, key string) (mastodonapi.AccountConfig, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return mastodonapi.AccountConfig{}, fmt.Errorf("mastodon account key is required")
	}
	for _, account := range cfg.Accounts {
		if account.Key == key {
			return account, nil
		}
	}
	return mastodonapi.AccountConfig{}, fmt.Errorf("mastodon account %q is not configured", key)
}
