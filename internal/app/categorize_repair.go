package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/categoryvocab"
	"github.com/darron/dbrain/internal/store"
)

func newCategorizeRepairCommand(root *rootOptions) *cobra.Command {
	var dryRun bool
	var clearSourceTagsWithoutEvidence bool

	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Repair category tags or clear unsupported source tags",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}

			if clearSourceTagsWithoutEvidence {
				st, err := store.OpenWithSemanticCache(cfg.DBPath, cfg.CacheDir)
				if err != nil {
					return err
				}
				defer func() { _ = st.Close() }()

				sources, err := st.ListCategorizedSourcesWithoutEvidence(cmd.Context())
				if err != nil {
					return err
				}

				const previewLimit = 20
				for i, source := range sources {
					if dryRun {
						if i < previewLimit {
							_, _ = fmt.Fprintf(cmd.OutOrStdout(), "[dry-run] %s\n  before: %s\n  after:  \n",
								source.SourceKey,
								source.UserTags,
							)
						}
						continue
					}
					if err := st.SaveSourceUserTags(cmd.Context(), source.ID, ""); err != nil {
						return fmt.Errorf("clear source tags %s: %w", source.SourceKey, err)
					}
				}
				if dryRun && len(sources) > previewLimit {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "[dry-run] ... %d more sources omitted from preview\n", len(sources)-previewLimit)
				}

				_, _ = fmt.Fprintln(cmd.OutOrStdout())
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Scanned:   %d\n", len(sources))
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated:   %d\n", len(sources))
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Unchanged: 0")
				if dryRun {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "(dry-run — no changes written)")
				}
				return nil
			}

			_, err = runCategorizeVocabRepair(cmd.Context(), cfg.DBPath, cfg.CacheDir, cfg.CategoriesPath, dryRun, cmd.OutOrStdout())
			return err
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would change without writing to the database")
	cmd.Flags().BoolVar(&clearSourceTagsWithoutEvidence, "clear-source-tags-without-evidence", false, "Clear source user_tags for sources that lack extracted text or summary evidence")

	return cmd
}

type categorizeVocabRepairStats struct {
	Scanned   int
	Updated   int
	Unchanged int
}

func runCategorizeVocabRepair(ctx context.Context, dbPath, cacheDir, categoriesPath string, dryRun bool, out io.Writer) (categorizeVocabRepairStats, error) {
	vocab, err := categoryvocab.Load(categoriesPath)
	if err != nil {
		return categorizeVocabRepairStats{}, fmt.Errorf("load categories.yaml: %w", err)
	}
	if vocab.Empty() {
		_, _ = fmt.Fprintln(out, "categories.yaml not found or empty — nothing to do.")
		return categorizeVocabRepairStats{}, nil
	}

	st, err := store.OpenWithSemanticCache(dbPath, cacheDir)
	if err != nil {
		return categorizeVocabRepairStats{}, err
	}
	defer func() { _ = st.Close() }()

	items, err := st.ListCategorizedItems(ctx)
	if err != nil {
		return categorizeVocabRepairStats{}, err
	}
	sources, err := st.ListCategorizedSources(ctx)
	if err != nil {
		return categorizeVocabRepairStats{}, err
	}

	stats := categorizeVocabRepairStats{Scanned: len(items) + len(sources)}
	for _, item := range items {
		newTags, changed := vocab.ApplyToCSV(item.UserTags)
		if !changed {
			stats.Unchanged++
			continue
		}
		if dryRun {
			_, _ = fmt.Fprintf(out, "[dry-run] %s\n  before: %s\n  after:  %s\n",
				item.SourceKey,
				formatTagDiff(item.UserTags, newTags),
				newTags,
			)
			stats.Updated++
			continue
		}
		if err := st.SaveItemUserTags(ctx, item.ID, newTags); err != nil {
			return stats, fmt.Errorf("save %s: %w", item.SourceKey, err)
		}
		stats.Updated++
	}
	for _, source := range sources {
		newTags, changed := vocab.ApplyToCSV(source.UserTags)
		if !changed {
			stats.Unchanged++
			continue
		}
		if dryRun {
			_, _ = fmt.Fprintf(out, "[dry-run] %s\n  before: %s\n  after:  %s\n",
				source.SourceKey,
				formatTagDiff(source.UserTags, newTags),
				newTags,
			)
			stats.Updated++
			continue
		}
		if err := st.SaveSourceUserTags(ctx, source.ID, newTags); err != nil {
			return stats, fmt.Errorf("save %s: %w", source.SourceKey, err)
		}
		stats.Updated++
	}

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "Scanned:   %d\n", stats.Scanned)
	_, _ = fmt.Fprintf(out, "Updated:   %d\n", stats.Updated)
	_, _ = fmt.Fprintf(out, "Unchanged: %d\n", stats.Unchanged)
	if dryRun {
		_, _ = fmt.Fprintln(out, "(dry-run — no changes written)")
	}
	return stats, nil
}

// formatTagDiff highlights which tokens changed by marking removed/added tokens.
func formatTagDiff(before, after string) string {
	bset := tokenSet(before)
	aset := tokenSet(after)
	var removed []string
	for t := range bset {
		if _, ok := aset[t]; !ok {
			removed = append(removed, "-"+t)
		}
	}
	if len(removed) == 0 {
		return before
	}
	return before + "  [" + strings.Join(removed, ", ") + "]"
}

func tokenSet(csv string) map[string]struct{} {
	m := make(map[string]struct{})
	for _, t := range strings.Split(csv, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			m[t] = struct{}{}
		}
	}
	return m
}
