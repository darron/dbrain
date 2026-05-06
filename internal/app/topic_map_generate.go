package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/topics"
	"github.com/darron/dbrain/internal/vault"
)

func newTopicMapCommand(root *rootOptions) *cobra.Command {
	var sourceTypes []string
	var seedLimit int
	var relatedLimit int
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "map <topic>",
		Short: "Build a topic map from the local brain",
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

			graph, err := topics.Build(cmd.Context(), st, strings.Join(args, " "), topics.Options{
				SourceTypes:  sourceTypes,
				SeedLimit:    seedLimit,
				RelatedLimit: relatedLimit,
			})
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), graph)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), topics.FormatText(graph))
			return err
		},
	}

	cmd.Flags().StringSliceVar(&sourceTypes, "source-type", nil, "Optional source type filters")
	cmd.Flags().IntVar(&seedLimit, "seed-limit", 6, "Maximum number of primary seed nodes")
	cmd.Flags().IntVar(&relatedLimit, "related-limit", 2, "Maximum number of related nodes to expand from each seed")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print the topic map as JSON")
	return cmd
}

func newTopicGenerateCommand(root *rootOptions) *cobra.Command {
	var sourceTypes []string
	var seedLimit int
	var relatedLimit int
	var stdout bool

	cmd := &cobra.Command{
		Use:   "generate <topic>",
		Short: "Generate a topic note under vault/topics",
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

			graph, err := topics.Build(cmd.Context(), st, strings.Join(args, " "), topics.Options{
				SourceTypes:  sourceTypes,
				SeedLimit:    seedLimit,
				RelatedLimit: relatedLimit,
			})
			if err != nil {
				return err
			}

			body := vault.RenderTopic(graph)
			if stdout {
				_, err = fmt.Fprint(cmd.OutOrStdout(), body)
				return err
			}

			if err := vault.WriteTopic(cfg, graph); err != nil {
				return err
			}
			indexPath, topicCount, err := rebuildTopicIndex(cfg)
			if err != nil {
				return err
			}
			relPath := vault.TopicNoteRelativePath(graph.Topic)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Topic: %s\n", graph.Topic)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Nodes: %d\n", len(graph.Nodes))
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Edges: %d\n", len(graph.Edges))
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Note: %s\n", filepath.Join(cfg.VaultDir, filepath.FromSlash(relPath)))
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Index: %s\n", indexPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Indexed topics: %d\n", topicCount)
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&sourceTypes, "source-type", nil, "Optional source type filters")
	cmd.Flags().IntVar(&seedLimit, "seed-limit", 6, "Maximum number of primary seed nodes")
	cmd.Flags().IntVar(&relatedLimit, "related-limit", 2, "Maximum number of related nodes to expand from each seed")
	cmd.Flags().BoolVar(&stdout, "stdout", false, "Print the rendered topic note instead of writing it")
	return cmd
}
