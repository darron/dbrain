package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
)

func newSearchCommand(root *rootOptions) *cobra.Command {
	var limit int
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search items and sources",
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

			results, err := st.Search(cmd.Context(), strings.Join(args, " "), limit)
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), results)
			}

			if len(results) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No results.")
				return nil
			}

			for _, result := range results {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), result.SourceKey)
				if result.Title != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  title: %s\n", result.Title)
				}
				if result.AuthorHandle != "" || result.AuthorName != "" {
					label := strings.TrimSpace(result.AuthorName)
					if result.AuthorHandle != "" {
						if label != "" {
							label += " "
						}
						label += "@" + strings.TrimSpace(result.AuthorHandle)
					}
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  author: %s\n", label)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  url: %s\n", result.CanonicalURL)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  note: %s\n", filepath.Join(cfg.VaultDir, filepath.FromSlash(result.NotePath)))
				if result.Snippet != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  snippet: %s\n", result.Snippet)
				}
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 10, "Maximum results")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print search results as JSON")

	return cmd
}
