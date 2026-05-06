package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

func newEntityCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "entity",
		Short: "Derive and render entities from the local brain",
		RunE:  helpCommand,
	}
	cmd.AddCommand(
		newEntityMapCommand(root),
		newEntityGenerateCommand(root),
		newEntityIndexCommand(root),
	)
	return cmd
}

func newEntityMapCommand(root *rootOptions) *cobra.Command {
	var kind string
	var limit int
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "map [query]",
		Short: "Search derived entities from the local brain",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}

			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			query := ""
			if len(args) > 0 {
				query = strings.Join(args, " ")
			}
			results, err := entities.Search(cmd.Context(), st, query, entities.SearchOptions{
				Kind:  kind,
				Limit: limit,
			})
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), results)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), entities.FormatText(results))
			return err
		},
	}

	cmd.Flags().StringVar(&kind, "kind", "", "Optional entity kind filter: person, org, project, or site")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum number of entities to return")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print entities as JSON")
	return cmd
}

func newEntityGenerateCommand(root *rootOptions) *cobra.Command {
	var kind string
	var limit int

	cmd := &cobra.Command{
		Use:   "generate <query>",
		Short: "Generate entity notes for matching derived entities",
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
			defer func() { _ = st.Close() }()

			results, err := entities.Search(cmd.Context(), st, strings.Join(args, " "), entities.SearchOptions{
				Kind:  kind,
				Limit: limit,
			})
			if err != nil {
				return err
			}

			written := 0
			for _, entity := range results {
				if err := vault.WriteEntity(cfg, entity); err != nil {
					return err
				}
				written++
			}

			allEntities, err := entities.BuildIndex(cmd.Context(), st)
			if err != nil {
				return err
			}
			if err := vault.WriteEntityIndex(cfg, allEntities); err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Entities matched: %d\n", len(results))
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Entities written: %d\n", written)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Index: %s\n", filepath.Join(cfg.VaultDir, filepath.FromSlash(vault.EntityIndexRelativePath())))
			return nil
		},
	}

	cmd.Flags().StringVar(&kind, "kind", "", "Optional entity kind filter: person, org, project, or site")
	cmd.Flags().IntVar(&limit, "limit", 10, "Maximum number of matching entities to write")
	return cmd
}

func newEntityIndexCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Generate all derived entity notes and rebuild the entity index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}

			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()

			allEntities, err := entities.BuildIndex(cmd.Context(), st)
			if err != nil {
				return err
			}
			for _, entity := range allEntities {
				if err := vault.WriteEntity(cfg, entity); err != nil {
					return err
				}
			}
			if err := vault.WriteEntityIndex(cfg, allEntities); err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Entities indexed: %d\n", len(allEntities))
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Index: %s\n", filepath.Join(cfg.VaultDir, filepath.FromSlash(vault.EntityIndexRelativePath())))
			return nil
		},
	}
	return cmd
}
