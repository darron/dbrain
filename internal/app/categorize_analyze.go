package app

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/categoryvocab"
	"github.com/darron/dbrain/internal/store"
)

func newCategorizeAnalyzeCommand(root *rootOptions) *cobra.Command {
	var minCount int
	var sortAlpha bool
	var draft bool

	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Show tag/category frequency to help author categories.yaml",
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
			defer func() { _ = st.Close() }()

			items, err := st.ListCategorizedItems(cmd.Context())
			if err != nil {
				return err
			}
			sources, err := st.ListCategorizedSources(cmd.Context())
			if err != nil {
				return err
			}

			counts := categorizeAnalyzeTokenCounts(items, sources)

			// Apply existing vocab so analysis reflects current state.
			vocab, _ := categoryvocab.Load(cfg.CategoriesPath)

			if draft {
				return writeDraftYAML(cmd, counts, vocab, minCount)
			}

			type entry struct {
				token string
				count int
			}
			entries := make([]entry, 0, len(counts))
			for t, c := range counts {
				if c >= minCount {
					entries = append(entries, entry{t, c})
				}
			}

			if sortAlpha {
				sort.Slice(entries, func(i, j int) bool {
					return entries[i].token < entries[j].token
				})
			} else {
				sort.Slice(entries, func(i, j int) bool {
					if entries[i].count != entries[j].count {
						return entries[i].count > entries[j].count
					}
					return entries[i].token < entries[j].token
				})
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Categorized records: %d items, %d sources\nUnique tokens (count >= %d): %d\n\n",
				len(items), len(sources), minCount, len(entries))

			for _, e := range entries {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%5d  %s\n", e.count, e.token)
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&minCount, "min-count", 2, "Only show tokens appearing this many times or more")
	cmd.Flags().BoolVar(&sortAlpha, "alpha", false, "Sort alphabetically instead of by frequency")
	cmd.Flags().BoolVar(&draft, "draft", false, "Write a starter categories.yaml to stdout based on detected clusters")

	return cmd
}
