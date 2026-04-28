package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"dbrain/internal/categoryvocab"
	"dbrain/internal/store"
)

func newCategorizeRepairCommand(root *rootOptions) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Apply categories.yaml aliases and drops to all existing user_tags",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}

			vocabPath := filepath.Join(cfg.RootDir, "categories.yaml")
			vocab, err := categoryvocab.Load(vocabPath)
			if err != nil {
				return fmt.Errorf("load categories.yaml: %w", err)
			}
			if vocab.Empty() {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "categories.yaml not found or empty — nothing to do.")
				return nil
			}

			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			items, err := st.ListCategorizedItems(cmd.Context())
			if err != nil {
				return err
			}

			var updated, unchanged int
			for _, item := range items {
				newTags, changed := vocab.ApplyToCSV(item.UserTags)
				if !changed {
					unchanged++
					continue
				}
				if dryRun {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "[dry-run] %s\n  before: %s\n  after:  %s\n",
						item.SourceKey,
						formatTagDiff(item.UserTags, newTags),
						newTags,
					)
					updated++
					continue
				}
				if err := st.SaveItemUserTags(cmd.Context(), item.ID, newTags); err != nil {
					return fmt.Errorf("save %s: %w", item.SourceKey, err)
				}
				updated++
			}

			_, _ = fmt.Fprintln(cmd.OutOrStdout())
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Scanned:   %d\n", len(items))
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated:   %d\n", updated)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Unchanged: %d\n", unchanged)
			if dryRun {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "(dry-run — no changes written)")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would change without writing to the database")

	return cmd
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
