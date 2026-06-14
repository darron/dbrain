package app

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/okf"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/version"
)

func newOKFCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "okf",
		Short: "Export and inspect Open Knowledge Format bundles",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newOKFExportCommand(root), newOKFValidateCommand())
	return cmd
}

func newOKFExportCommand(root *rootOptions) *cobra.Command {
	var outDir string
	var profile string
	var itemsOnly bool
	var sourcesOnly bool
	var includeEntities bool
	var includeTopics bool
	var sourceTypes []string
	var limit int
	var includeRaw bool
	var maxRawChars int
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export a private OKF bundle from the local brain",
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

			includeItems := true
			includeSources := true
			if cmd.Flags().Changed("items") || cmd.Flags().Changed("sources") {
				includeItems = itemsOnly
				includeSources = sourcesOnly
			}

			result, err := okf.Export(cmd.Context(), cfg, st, okf.ExportOptions{
				OutDir:          outDir,
				Profile:         profile,
				IncludeItems:    includeItems,
				IncludeSources:  includeSources,
				IncludeEntities: includeEntities,
				IncludeTopics:   includeTopics,
				SourceTypes:     sourceTypes,
				Limit:           limit,
				IncludeRaw:      includeRaw,
				MaxRawChars:     maxRawChars,
				DbrainVersion:   version.Current().Short,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			writeOKFExportResult(cmd, result)
			return nil
		},
	}
	cmd.Flags().StringVar(&outDir, "out", "", "Output bundle directory (defaults to configured okf/current)")
	cmd.Flags().StringVar(&profile, "profile", okf.ProfilePrivate, "Export profile; MVP supports private only")
	cmd.Flags().BoolVar(&itemsOnly, "items", false, "Include item concepts")
	cmd.Flags().BoolVar(&sourcesOnly, "sources", false, "Include source concepts")
	cmd.Flags().BoolVar(&includeEntities, "entities", false, "Include derived entity concepts")
	cmd.Flags().BoolVar(&includeTopics, "topics", false, "Include derived topic concepts from vault topic definitions")
	cmd.Flags().StringArrayVar(&sourceTypes, "source-type", nil, "Filter by source type; may be repeated")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit exported items and sources for smoke tests")
	cmd.Flags().BoolVar(&includeRaw, "include-raw", true, "Include raw evidence sections in private export")
	cmd.Flags().IntVar(&maxRawChars, "max-raw-chars", 0, "Maximum raw evidence characters per section; 0 means unlimited")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print export stats as JSON")
	return cmd
}

func newOKFValidateCommand() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "validate <dir>",
		Short: "Validate a generated OKF bundle",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := okf.ValidateBundle(args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			writeOKFValidationResult(cmd, result)
			if !result.Conformant {
				return fmt.Errorf("okf bundle is not conformant")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print validation result as JSON")
	return cmd
}

func writeOKFExportResult(cmd *cobra.Command, result okf.ExportResult) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Bundle: %s\n", result.Bundle)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Profile: %s\n", result.Profile)
	if strings.TrimSpace(result.Profile) == okf.ProfilePrivate {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Private bundle: includes raw local evidence and archive/upload URLs; review before sharing.")
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Items written: %d\n", result.ItemsWritten)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sources written: %d\n", result.SourcesWritten)
	if result.EntitiesWritten > 0 {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Entities written: %d\n", result.EntitiesWritten)
	}
	if result.TopicsWritten > 0 {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Topics written: %d\n", result.TopicsWritten)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Indexes written: %d\n", result.IndexesWritten)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Broken internal links: %d\n", result.BrokenInternalLinks)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Omitted-by-filter links: %d\n", result.OmittedByFilterLinks)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Errors: %d\n", len(result.Errors))
}

func writeOKFValidationResult(cmd *cobra.Command, result okf.ValidationResult) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Bundle: %s\n", result.Bundle)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Conformant: %t\n", result.Conformant)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Concepts: %d\n", result.Concepts)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Indexes: %d\n", result.Indexes)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Broken internal links: %d\n", result.BrokenInternalLinks)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Omitted-by-filter links: %d\n", result.OmittedByFilterLinks)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Errors: %d\n", len(result.Errors))
	for _, msg := range result.Errors {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "- %s\n", msg)
	}
}
