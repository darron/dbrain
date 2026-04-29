package app

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/noterepair"
	"github.com/darron/dbrain/internal/store"
)

func newRepairFTSCommand(root *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "fts",
		Short: "Rebuild the full-text search index from brain.db",
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
			defer func() { _ = st.Close() }()

			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Rebuilding FTS index…")
			stats, err := st.RebuildFTS(cmd.Context())
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Rebuilt: %d\n", stats.Rebuilt)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Errors:  %d\n", stats.Errors)
			return nil
		},
	}
}

func newRepairNotesCommand(root *rootOptions) *cobra.Command {
	var items bool
	var sources bool
	var missingOnly bool
	var limit int
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "notes",
		Short: "Rebuild rendered Markdown notes from brain.db",
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

			stats, err := noterepair.Run(cmd.Context(), cfg, st, noterepair.Options{
				Items:       items,
				Sources:     sources,
				MissingOnly: missingOnly,
				Limit:       limit,
			})
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items considered: %d\n", stats.ItemsConsidered)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items written: %d\n", stats.ItemsWritten)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items already current: %d\n", stats.ItemsAlreadyCurrent)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items skipped missing-only: %d\n", stats.ItemsSkippedMissingOnly)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources considered: %d\n", stats.SourcesConsidered)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources written: %d\n", stats.SourcesWritten)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources already current: %d\n", stats.SourcesAlreadyCurrent)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources skipped missing-only: %d\n", stats.SourcesSkippedMissingOnly)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Errors: %d\n", stats.Errors)
			return nil
		},
	}

	cmd.Flags().BoolVar(&items, "items", false, "Repair item notes only")
	cmd.Flags().BoolVar(&sources, "sources", false, "Repair source notes only")
	cmd.Flags().BoolVar(&missingOnly, "missing-only", true, "Only rewrite notes that are currently missing from the vault")
	cmd.Flags().IntVar(&limit, "limit", 0, "Optional limit per note kind for smoke testing")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print repair stats as JSON")

	return cmd
}

func newRepairSourcesCommand(root *rootOptions) *cobra.Command {
	var domains []string
	var sourceLookups []string
	var dryRun bool
	var yes bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "sources",
		Short: "Reset source extraction and summary state so matching sources rerun",
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

			sourceIDs := make([]int64, 0, len(sourceLookups))
			for _, lookup := range sourceLookups {
				source, resolveErr := st.GetSource(cmd.Context(), lookup)
				if resolveErr != nil {
					return resolveErr
				}
				sourceIDs = append(sourceIDs, source.ID)
			}

			opts := store.ResetSourceEnrichmentOptions{
				Domains:   domains,
				SourceIDs: sourceIDs,
				DryRun:    true,
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
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Dry run: true")
				return nil
			}

			if !yes {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "This will reset extraction and summary state for %d sources. Continue? [y/N] ", preview.Matched)
				answer, readErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if readErr != nil && strings.TrimSpace(answer) == "" {
					return readErr
				}
				answer = strings.ToLower(strings.TrimSpace(answer))
				if answer != "y" && answer != "yes" {
					if jsonOut {
						return writeJSON(cmd.OutOrStdout(), store.ResetSourceEnrichmentStats{Matched: preview.Matched})
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
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&domains, "domain", nil, "Reset sources whose domain matches this host or subdomain; repeat or comma-separate")
	cmd.Flags().StringSliceVar(&sourceLookups, "source", nil, "Reset specific source lookups; repeat or comma-separate source_key, canonical_url, normalized_url, or note_path")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Only print how many sources would be reset")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print reset stats as JSON")

	return cmd
}
