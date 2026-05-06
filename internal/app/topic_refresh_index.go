package app

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/topics"
	"github.com/darron/dbrain/internal/vault"
)

func newTopicRefreshCommand(root *rootOptions) *cobra.Command {
	var sourceTypes []string
	var seedLimit int
	var relatedLimit int

	cmd := &cobra.Command{
		Use:   "refresh [topic]",
		Short: "Refresh generated topic notes from stored frontmatter settings",
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

			defs, err := resolveTopicRefreshDefinitions(cmd, cfg, args, sourceTypes, seedLimit, relatedLimit)
			if err != nil {
				return err
			}

			refreshed := 0
			for _, def := range defs {
				graph, err := topics.FromDefinition(cmd.Context(), st, def)
				if err != nil {
					return err
				}
				if err := vault.WriteTopic(cfg, graph); err != nil {
					return err
				}
				refreshed++
			}

			indexPath, topicCount, err := rebuildTopicIndex(cfg)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Topics refreshed: %d\n", refreshed)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Index: %s\n", indexPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Indexed topics: %d\n", topicCount)
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&sourceTypes, "source-type", nil, "Optional source type overrides")
	cmd.Flags().IntVar(&seedLimit, "seed-limit", 6, "Maximum number of primary seed nodes")
	cmd.Flags().IntVar(&relatedLimit, "related-limit", 2, "Maximum number of related nodes to expand from each seed")
	return cmd
}

func newTopicIndexCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Rebuild the topic index note from generated topic notes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}

			indexPath, topicCount, err := rebuildTopicIndex(cfg)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Index: %s\n", indexPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Indexed topics: %d\n", topicCount)
			return nil
		},
	}
	return cmd
}
