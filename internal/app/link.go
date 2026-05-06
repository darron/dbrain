package app

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/linkadd"
	"github.com/darron/dbrain/internal/store"
)

func newLinkCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Add and manage manually submitted links",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newLinkAddCommand(root))
	return cmd
}

func newLinkAddCommand(root *rootOptions) *cobra.Command {
	var enrich bool
	var force bool
	var summarize bool
	var model string
	var cliProvider string
	var length string
	var timeout time.Duration
	var concurrency int
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "add URL [URL...]",
		Short: "Queue one or more links for extraction and summarization",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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

			stats, err := linkadd.Run(cmd.Context(), cfg, st, args, linkadd.Options{
				Enrich:      enrich,
				Force:       force,
				Summarize:   summarize,
				Model:       model,
				CLI:         cliProvider,
				Length:      length,
				Timeout:     timeout,
				Concurrency: concurrency,
				Logger:      newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr()),
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}
			return writeLinkAddStats(cmd.OutOrStdout(), stats)
		},
	}

	cmd.Flags().BoolVar(&enrich, "enrich", false, "Immediately extract and summarize the added links")
	cmd.Flags().BoolVar(&force, "force", false, "Reprocess sources even if they already have current extraction or summary output")
	cmd.Flags().BoolVar(&summarize, "summarize", true, "Run summarization when --enrich is enabled")
	cmd.Flags().StringVar(&model, "model", "", "Optional summarize model override")
	cmd.Flags().StringVar(&cliProvider, "cli", defaultCLIProvider, "Summarize CLI provider")
	cmd.Flags().StringVar(&length, "length", "medium", "Summary length")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, "Timeout for immediate enrichment")
	cmd.Flags().IntVar(&concurrency, "concurrency", 1, "Number of concurrent immediate enrichment jobs")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print add-link result as JSON")
	return cmd
}

func writeLinkAddStats(dst interface{ Write([]byte) (int, error) }, stats linkadd.Stats) error {
	if _, err := fmt.Fprintf(dst, "Submitted: %d\n", stats.Submitted); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Queued: %d\n", stats.Queued); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Sources created: %d\n", stats.SourcesCreated); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(dst, "Sources existing: %d\n", stats.SourcesExisting); err != nil {
		return err
	}
	for _, result := range stats.Results {
		if result.Error != "" {
			if _, err := fmt.Fprintf(dst, "ERROR %s: %s\n", result.URL, result.Error); err != nil {
				return err
			}
			continue
		}
		status := "existing"
		if result.SourceCreated {
			status = "created"
		}
		if _, err := fmt.Fprintf(dst, "%s %s %s (%s)\n", status, result.SourceKey, result.CanonicalURL, result.SourceType); err != nil {
			return err
		}
	}
	if stats.SourcesExtracted > 0 || stats.SourcesSummarized > 0 || stats.SourcesRendered > 0 || stats.SourcesUnchanged > 0 {
		if _, err := fmt.Fprintf(dst, "Sources extracted: %d\n", stats.SourcesExtracted); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(dst, "Sources summarized: %d\n", stats.SourcesSummarized); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(dst, "Sources rendered: %d\n", stats.SourcesRendered); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(dst, "Sources unchanged: %d\n", stats.SourcesUnchanged); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(dst, "Errors: %d\n", stats.Errors); err != nil {
		return err
	}
	return nil
}
