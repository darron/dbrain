package app

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vaultfs"
)

func newGetCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "get <source-key-or-id>",
		Short: "Load an item or source note",
		Args:  cobra.ExactArgs(1),
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

			lookup := args[0]
			item, err := st.GetItem(cmd.Context(), lookup)
			if err == nil {
				if jsonOut {
					return writeJSON(cmd.OutOrStdout(), item)
				}

				root, err := vaultfs.Open(cfg.VaultDir)
				if err != nil {
					return err
				}
				defer func() { _ = root.Close() }()
				content, err := root.ReadFile(item.NotePath)
				if err != nil {
					return fmt.Errorf("read note %q: %w", item.NotePath, err)
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

			root, err := vaultfs.Open(cfg.VaultDir)
			if err != nil {
				return err
			}
			defer func() { _ = root.Close() }()
			content, err := root.ReadFile(source.NotePath)
			if err != nil {
				return fmt.Errorf("read note %q: %w", source.NotePath, err)
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), string(content))
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print the item as JSON")

	return cmd
}
