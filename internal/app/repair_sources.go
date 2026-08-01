package app

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
)

func newRepairSourcesCommand(root *rootOptions) *cobra.Command {
	var domains []string
	var sourceLookups []string
	var sourceTypes []string
	var extractStatuses []string
	var summaryStatuses []string
	var failureKinds []string
	var minFailures int
	var rehydrateXArticles bool
	var dryRun bool
	var yes bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "sources",
		Short: "Reset source extraction and summary state so matching sources rerun",
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

			sourceIDs := make([]int64, 0, len(sourceLookups))
			for _, lookup := range sourceLookups {
				source, resolveErr := st.GetSource(cmd.Context(), lookup)
				if resolveErr != nil {
					return resolveErr
				}
				sourceIDs = append(sourceIDs, source.ID)
			}

			opts := store.ResetSourceEnrichmentOptions{
				Domains:            domains,
				SourceIDs:          sourceIDs,
				SourceTypes:        sourceTypes,
				ExtractStatuses:    extractStatuses,
				SummaryStatuses:    summaryStatuses,
				FailureKinds:       failureKinds,
				MinFailures:        minFailures,
				RehydrateXArticles: rehydrateXArticles,
				DryRun:             true,
			}
			preview, err := st.ResetSourceEnrichment(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if dryRun {
				if jsonOut {
					return writeJSON(cmd.OutOrStdout(), preview)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources matched: %d\n", preview.Matched)
				if rehydrateXArticles {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Linked X items matched: %d\n", preview.XItemsMatched)
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Dry run: true")
				return nil
			}

			if !yes {
				if rehydrateXArticles {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "This will reset extraction/summary state for %d sources and mark %d linked X items for hydration. Continue? [y/N] ", preview.Matched, preview.XItemsMatched)
				} else {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "This will reset extraction and summary state for %d sources. Continue? [y/N] ", preview.Matched)
				}
				answer, readErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if readErr != nil && strings.TrimSpace(answer) == "" {
					return readErr
				}
				answer = strings.ToLower(strings.TrimSpace(answer))
				if answer != "y" && answer != "yes" {
					if jsonOut {
						return writeJSON(cmd.OutOrStdout(), store.ResetSourceEnrichmentStats{Matched: preview.Matched, XItemsMatched: preview.XItemsMatched})
					}
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
					return nil
				}
			}

			opts.DryRun = false
			stats, err := st.ResetSourceEnrichment(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources matched: %d\n", stats.Matched)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources reset: %d\n", stats.Reset)
			if rehydrateXArticles {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Linked X items matched: %d\n", stats.XItemsMatched)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Linked X items marked for hydration: %d\n", stats.XItemsReset)
			}
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&domains, "domain", nil, "Reset sources whose domain matches this host or subdomain; repeat or comma-separate")
	cmd.Flags().StringSliceVar(&sourceLookups, "source", nil, "Reset specific source lookups; repeat or comma-separate source_key, canonical_url, normalized_url, or note_path")
	cmd.Flags().StringSliceVar(&sourceTypes, "source-type", nil, "Reset sources with this source_type; repeat or comma-separate, for example x_article")
	cmd.Flags().StringSliceVar(&extractStatuses, "extract-status", nil, "Reset sources with this extract_status; repeat or comma-separate, for example error or dead")
	cmd.Flags().StringSliceVar(&summaryStatuses, "summary-status", nil, "Reset sources with this summary_status; repeat or comma-separate")
	cmd.Flags().StringSliceVar(&failureKinds, "failure-kind", nil, "Reset sources with this extract_failure_kind; repeat or comma-separate, for example x_article_shell")
	cmd.Flags().IntVar(&minFailures, "min-failures", 0, "Only reset sources with at least this extract_failure_count")
	cmd.Flags().BoolVar(&rehydrateXArticles, "rehydrate-x-articles", false, "For matching x_article sources, also clear linked X hydration cache so hydrate x refetches article metadata")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Only print how many sources would be reset")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print reset stats as JSON")

	return cmd
}
