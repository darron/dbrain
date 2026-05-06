package app

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/githubimport"
	"github.com/darron/dbrain/internal/store"
)

func newImportGitHubCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github",
		Short: "Import GitHub signals",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newImportGitHubStarsCommand(root))
	return cmd
}

func newImportGitHubStarsCommand(root *rootOptions) *cobra.Command {
	var limit int
	var summarize bool
	var force bool
	var model string
	var cliProvider string
	var length string
	var timeout time.Duration
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "stars",
		Short: "Import starred repositories via the GitHub API",
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
			defer func() {
				_ = st.Close()
			}()

			stats, err := githubimport.Run(cmd.Context(), cfg, st, githubimport.Options{
				Limit:     limit,
				Force:     force,
				Summarize: summarize,
				Model:     model,
				CLI:       cliProvider,
				Length:    length,
				Timeout:   timeout,
				Logger:    newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr()),
			})
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Viewer: %s\n", stats.Viewer)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Pages fetched: %d\n", stats.PagesFetched)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Stars processed: %d\n", stats.StarsProcessed)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items created: %d\n", stats.ItemsCreated)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items updated: %d\n", stats.ItemsUpdated)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items unchanged: %d\n", stats.ItemsUnchanged)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items rendered: %d\n", stats.ItemsRendered)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources created: %d\n", stats.SourcesCreated)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Source links created: %d\n", stats.LinksCreated)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Homepage sources discovered: %d\n", stats.HomepageDiscovered)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources queued: %d\n", stats.SourcesQueued)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources extracted: %d\n", stats.SourcesExtracted)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources summarized: %d\n", stats.SourcesSummarized)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources rendered: %d\n", stats.SourcesRendered)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Source unchanged writes: %d\n", stats.SourcesUnchanged)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Errors: %d\n", stats.Errors)
			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum starred repositories to process before stopping")
	cmd.Flags().BoolVar(&summarize, "summarize", true, "Run summarize.sh summarization for repo and homepage sources")
	cmd.Flags().BoolVar(&force, "force", false, "Reprocess existing stars and linked sources instead of stopping at the first already-seen star")
	cmd.Flags().StringVar(&model, "model", "", "Optional summarize model override")
	cmd.Flags().StringVar(&cliProvider, "cli", defaultCLIProvider, "Summarize CLI provider")
	cmd.Flags().StringVar(&length, "length", "medium", "Summary length for summarize.sh")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Timeout for GitHub API requests and summarize.sh")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print import stats as JSON")

	return cmd
}
