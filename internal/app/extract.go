package app

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
)

var (
	runSourceEnrichPending   = sourceenrich.RunPending
	runSourceEnrichSourceIDs = sourceenrich.RunSourceIDs
)

const defaultExtractConcurrency = 4

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
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "links",
		Short: "Discover and enrich outbound links from imported items",
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
				Logger:        newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr()),
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
	cmd.Flags().IntVar(&concurrency, "concurrency", defaultExtractConcurrency, "Number of concurrent source extract/summarize jobs")
	cmd.Flags().BoolVar(&force, "force", false, "Reprocess items and sources even if they were already discovered or enriched")
	cmd.Flags().BoolVar(&summarize, "summarize", true, "Run summarize.sh summarization after extraction")
	cmd.Flags().StringVar(&model, "model", "", "Optional summarize model override")
	cmd.Flags().StringVar(&cliProvider, "cli", defaultCLIProvider, "Summarize CLI provider")
	cmd.Flags().StringVar(&length, "length", "medium", "Summary length for summarize.sh")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Timeout for summarize.sh extraction and summarization")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print extraction stats as JSON")

	return cmd
}

func newExtractSourcesCommand(root *rootOptions) *cobra.Command {
	var limit int
	var concurrency int
	var force bool
	var summarize bool
	var sourceLookups []string
	var model string
	var cliProvider string
	var length string
	var timeout time.Duration
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "sources",
		Short: "Enrich already-known sources from the global backlog",
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

			opts := sourceenrich.Options{
				Limit:       limit,
				Concurrency: concurrency,
				Force:       force,
				Summarize:   summarize,
				Model:       model,
				CLI:         cliProvider,
				Length:      length,
				Timeout:     timeout,
				Logger:      newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr()),
			}
			if len(sourceLookups) > 0 && !cmd.Flags().Changed("limit") {
				opts.Limit = 0
			}

			var (
				stats  sourceenrich.Stats
				runErr error
			)
			if len(sourceLookups) > 0 {
				sourceIDs := make([]int64, 0, len(sourceLookups))
				for _, lookup := range sourceLookups {
					source, resolveErr := st.GetSource(cmd.Context(), lookup)
					if resolveErr != nil {
						return resolveErr
					}
					sourceIDs = append(sourceIDs, source.ID)
				}
				stats, _, runErr = runSourceEnrichSourceIDs(cmd.Context(), cfg, st, sourceIDs, opts)
			} else {
				stats, _, runErr = runSourceEnrichPending(cmd.Context(), cfg, st, opts)
			}
			if runErr != nil {
				return runErr
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}

			return writeSourceEnrichStats(cmd.OutOrStdout(), stats)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum queued sources to enrich")
	cmd.Flags().IntVar(&concurrency, "concurrency", defaultExtractConcurrency, "Number of concurrent source extract/summarize jobs")
	cmd.Flags().BoolVar(&force, "force", false, "Reprocess sources even if they already look current")
	cmd.Flags().BoolVar(&summarize, "summarize", true, "Run summarize.sh summarization after extraction")
	cmd.Flags().StringSliceVar(&sourceLookups, "source", nil, "Specific source lookups to enrich; repeat or comma-separate source_key, canonical_url, normalized_url, or note_path values")
	cmd.Flags().StringVar(&model, "model", "", "Optional summarize model override")
	cmd.Flags().StringVar(&cliProvider, "cli", defaultCLIProvider, "Summarize CLI provider")
	cmd.Flags().StringVar(&length, "length", "medium", "Summary length for summarize.sh")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Timeout for summarize.sh extraction and summarization")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print enrichment stats as JSON")

	return cmd
}

func writeSourceEnrichStats(out interface {
	Write([]byte) (int, error)
}, stats sourceenrich.Stats) error {
	_, _ = fmt.Fprintf(out, "Sources queued: %d\n", stats.SourcesQueued)
	_, _ = fmt.Fprintf(out, "Sources extracted: %d\n", stats.SourcesExtracted)
	_, _ = fmt.Fprintf(out, "Sources summarized: %d\n", stats.SourcesSummarized)
	_, _ = fmt.Fprintf(out, "Sources rendered: %d\n", stats.SourcesRendered)
	_, _ = fmt.Fprintf(out, "Source unchanged writes: %d\n", stats.SourcesUnchanged)
	_, _ = fmt.Fprintf(out, "Errors: %d\n", stats.Errors)
	return nil
}
