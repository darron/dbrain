package app

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"dbrain/internal/sourceenrich"
	"dbrain/internal/store"
	"dbrain/internal/worker"
)

func newWorkerCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Run long-lived background-style worker loops",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newWorkerSourcesCommand(root))
	return cmd
}

func newWorkerSourcesCommand(root *rootOptions) *cobra.Command {
	var limit int
	var batchLimit int
	var concurrency int
	var force bool
	var summarize bool
	var model string
	var cliProvider string
	var length string
	var timeout time.Duration
	var watch bool
	var pollInterval time.Duration
	var idleExitAfter time.Duration
	var maxCycles int
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "sources",
		Short: "Drain the source enrichment backlog in repeated batches",
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

			logger := newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr())

			stats, err := worker.RunSources(
				cmd.Context(),
				func(ctx context.Context) (store.BacklogStats, error) {
					return st.Backlog(ctx, "", "", "")
				},
				func(ctx context.Context, remainingLimit int) (sourceenrich.Stats, error) {
					effectiveBatchLimit := batchLimit
					if limit > 0 && !cmd.Flags().Changed("batch-limit") {
						effectiveBatchLimit = limit
					}
					if remainingLimit > 0 && (effectiveBatchLimit <= 0 || remainingLimit < effectiveBatchLimit) {
						effectiveBatchLimit = remainingLimit
					}

					batchStats, _, err := sourceenrich.RunPending(ctx, cfg, st, sourceenrich.Options{
						Limit:       effectiveBatchLimit,
						Concurrency: concurrency,
						Force:       force,
						Summarize:   summarize,
						Model:       model,
						CLI:         cliProvider,
						Length:      length,
						Timeout:     timeout,
						Logger:      logger,
					})
					return batchStats, err
				},
				worker.SourceOptions{
					Watch:         watch,
					PollInterval:  pollInterval,
					IdleExitAfter: idleExitAfter,
					MaxCycles:     maxCycles,
					MaxSources:    limit,
					Logger:        logger,
				},
			)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}
			return writeWorkerSourceStats(cmd.OutOrStdout(), stats)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 0, "Optional maximum total queued sources to enrich before exiting")
	cmd.Flags().IntVar(&batchLimit, "batch-limit", 50, "Maximum queued sources to enrich per worker cycle")
	cmd.Flags().IntVar(&concurrency, "concurrency", 4, "Number of concurrent source extract/summarize jobs per batch")
	cmd.Flags().BoolVar(&force, "force", false, "Reprocess sources even if they already look current")
	cmd.Flags().BoolVar(&summarize, "summarize", true, "Run source summarization after extraction")
	cmd.Flags().StringVar(&model, "model", "", "Optional summary model override")
	cmd.Flags().StringVar(&cliProvider, "cli", defaultCLIProvider, "Summarize CLI provider")
	cmd.Flags().StringVar(&length, "length", "medium", "Summary length target")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Timeout for extraction and summarization")
	cmd.Flags().BoolVar(&watch, "watch", false, "Keep polling for new source backlog instead of exiting when drained")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", 30*time.Second, "How often to poll for new source backlog while watching")
	cmd.Flags().DurationVar(&idleExitAfter, "idle-exit-after", 0, "Optional maximum idle watch duration before exiting")
	cmd.Flags().IntVar(&maxCycles, "max-cycles", 0, "Optional maximum worker cycles before exiting")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print worker stats as JSON")

	return cmd
}

func writeWorkerSourceStats(dst interface{ Write([]byte) (int, error) }, stats worker.SourceStats) error {
	lines := []struct {
		label string
		value interface{}
	}{
		{"Cycles", stats.Cycles},
		{"Work cycles", stats.WorkCycles},
		{"Idle polls", stats.IdlePolls},
		{"Sources queued", stats.SourcesQueued},
		{"Sources extracted", stats.SourcesExtracted},
		{"Sources summarized", stats.SourcesSummarized},
		{"Sources rendered", stats.SourcesRendered},
		{"Source unchanged writes", stats.SourcesUnchanged},
		{"Errors", stats.Errors},
		{"Stopped", stats.StoppedReason},
		{"Final source extraction pending", stats.FinalBacklog.SourceExtractionPending},
		{"Final source summary pending", stats.FinalBacklog.SourceSummaryPending},
		{"Started at", formatWorkerTime(stats.StartedAt)},
		{"Completed at", formatWorkerTime(stats.CompletedAt)},
		{"Duration", stats.Duration},
	}
	for _, line := range lines {
		if _, err := fmt.Fprintf(dst, "%s: %v\n", line.label, line.value); err != nil {
			return err
		}
	}
	return nil
}

func formatWorkerTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
