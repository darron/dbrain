package app

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"dbrain/internal/linkextract"
	"dbrain/internal/sourceenrich"
	"dbrain/internal/store"
)

func newExtractLinksCommand(root *rootOptions) *cobra.Command {
	var discoverLimit int
	var limit int
	var concurrency int
	var force bool
	var summarize bool
	var model string
	var cliProvider string
	var length string
	var timeout time.Duration
	var debug bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "links",
		Short: "Discover and enrich outbound links from imported items",
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

			stats, err := linkextract.Run(cmd.Context(), cfg, st, linkextract.Options{
				DiscoverLimit: discoverLimit,
				Limit:         limit,
				Concurrency:   concurrency,
				Force:         force,
				Summarize:     summarize,
				Model:         model,
				CLI:           cliProvider,
				Length:        length,
				Timeout:       timeout,
				Logger:        newLogger(debug, cmd.ErrOrStderr()),
			})
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items scanned: %d\n", stats.ItemsScanned)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items marked: %d\n", stats.ItemsMarked)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Links found: %d\n", stats.LinksFound)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources created: %d\n", stats.SourcesCreated)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Links created: %d\n", stats.LinksCreated)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources queued: %d\n", stats.SourcesQueued)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources extracted: %d\n", stats.SourcesExtracted)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources summarized: %d\n", stats.SourcesSummarized)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources rendered: %d\n", stats.SourcesRendered)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Source unchanged writes: %d\n", stats.SourcesUnchanged)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Errors: %d\n", stats.Errors)
			return nil
		},
	}

	cmd.Flags().IntVar(&discoverLimit, "discover-limit", 500, "Maximum bookmark items to scan for outbound links")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum deduped sources to enrich")
	cmd.Flags().IntVar(&concurrency, "concurrency", 1, "Number of concurrent source extract/summarize jobs")
	cmd.Flags().BoolVar(&force, "force", false, "Reprocess items and sources even if they were already discovered or enriched")
	cmd.Flags().BoolVar(&summarize, "summarize", true, "Run summarize.sh summarization after extraction")
	cmd.Flags().StringVar(&model, "model", "", "Optional summarize model override")
	cmd.Flags().StringVar(&cliProvider, "cli", defaultCLIProvider, "Summarize CLI provider")
	cmd.Flags().StringVar(&length, "length", "medium", "Summary length for summarize.sh")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Timeout for summarize.sh extraction and summarization")
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable structured debug logging to stderr")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print extraction stats as JSON")

	return cmd
}

func newExtractSourcesCommand(root *rootOptions) *cobra.Command {
	var limit int
	var concurrency int
	var force bool
	var summarize bool
	var model string
	var cliProvider string
	var length string
	var timeout time.Duration
	var debug bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "sources",
		Short: "Enrich already-known sources from the global backlog",
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

			stats, _, err := sourceenrich.RunPending(cmd.Context(), cfg, st, sourceenrich.Options{
				Limit:       limit,
				Concurrency: concurrency,
				Force:       force,
				Summarize:   summarize,
				Model:       model,
				CLI:         cliProvider,
				Length:      length,
				Timeout:     timeout,
				Logger:      newLogger(debug, cmd.ErrOrStderr()),
			})
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources queued: %d\n", stats.SourcesQueued)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources extracted: %d\n", stats.SourcesExtracted)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources summarized: %d\n", stats.SourcesSummarized)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources rendered: %d\n", stats.SourcesRendered)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Source unchanged writes: %d\n", stats.SourcesUnchanged)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Errors: %d\n", stats.Errors)
			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum queued sources to enrich")
	cmd.Flags().IntVar(&concurrency, "concurrency", 1, "Number of concurrent source extract/summarize jobs")
	cmd.Flags().BoolVar(&force, "force", false, "Reprocess sources even if they already look current")
	cmd.Flags().BoolVar(&summarize, "summarize", true, "Run summarize.sh summarization after extraction")
	cmd.Flags().StringVar(&model, "model", "", "Optional summarize model override")
	cmd.Flags().StringVar(&cliProvider, "cli", defaultCLIProvider, "Summarize CLI provider")
	cmd.Flags().StringVar(&length, "length", "medium", "Summary length for summarize.sh")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Timeout for summarize.sh extraction and summarization")
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable structured debug logging to stderr")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print enrichment stats as JSON")

	return cmd
}
