package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"dbrain/internal/store"
)

func newGetCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "get <source-key-or-id>",
		Short: "Load an item or source note",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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

			lookup := args[0]
			item, err := st.GetItem(cmd.Context(), lookup)
			if err == nil {
				if jsonOut {
					return writeJSON(cmd.OutOrStdout(), item)
				}

				fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(item.NotePath))
				content, err := os.ReadFile(fullPath)
				if err != nil {
					return fmt.Errorf("read note %s: %w", fullPath, err)
				}
				_, _ = fmt.Fprint(cmd.OutOrStdout(), string(content))
				return nil
			}

			source, sourceErr := st.GetSource(cmd.Context(), lookup)
			if sourceErr != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), source)
			}

			fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(source.NotePath))
			content, err := os.ReadFile(fullPath)
			if err != nil {
				return fmt.Errorf("read note %s: %w", fullPath, err)
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), string(content))
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print the item as JSON")

	return cmd
}
