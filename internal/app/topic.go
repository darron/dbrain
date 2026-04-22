package app

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"dbrain/internal/config"
	"dbrain/internal/store"
	"dbrain/internal/topics"
	"dbrain/internal/vault"
)

func newTopicCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "topic",
		Short: "Build and write topic maps from the local brain",
		RunE:  helpCommand,
	}
	cmd.AddCommand(
		newTopicMapCommand(root),
		newTopicGenerateCommand(root),
		newTopicRefreshCommand(root),
		newTopicIndexCommand(root),
	)
	return cmd
}

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
			cfg, err := loadConfig(root.root)
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
			cfg, err := loadConfig(root.root)
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

func newTopicRefreshCommand(root *rootOptions) *cobra.Command {
	var sourceTypes []string
	var seedLimit int
	var relatedLimit int

	cmd := &cobra.Command{
		Use:   "refresh [topic]",
		Short: "Refresh generated topic notes from stored frontmatter settings",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(root.root)
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
			cfg, err := loadConfig(root.root)
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

func resolveTopicRefreshDefinitions(cmd *cobra.Command, cfg config.Config, args []string, sourceTypes []string, seedLimit int, relatedLimit int) ([]topics.Definition, error) {
	if len(args) == 0 {
		defs, err := vault.ListTopicDefinitions(cfg)
		if err != nil {
			return nil, err
		}
		for idx := range defs {
			defs[idx] = applyTopicOverrides(cmd, defs[idx], sourceTypes, seedLimit, relatedLimit)
		}
		return defs, nil
	}

	def, err := vault.ReadTopicDefinition(cfg, strings.Join(args, " "))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("topic note not found; use `dbrain topic generate %q` first", strings.Join(args, " "))
		}
		return nil, err
	}
	def = applyTopicOverrides(cmd, def, sourceTypes, seedLimit, relatedLimit)
	return []topics.Definition{def}, nil
}

func applyTopicOverrides(cmd *cobra.Command, def topics.Definition, sourceTypes []string, seedLimit int, relatedLimit int) topics.Definition {
	if cmd.Flags().Changed("source-type") {
		def.SourceTypes = append([]string(nil), sourceTypes...)
	}
	if cmd.Flags().Changed("seed-limit") {
		def.SeedLimit = seedLimit
	}
	if cmd.Flags().Changed("related-limit") {
		def.RelatedLimit = relatedLimit
	}
	return def
}

func rebuildTopicIndex(cfg config.Config) (string, int, error) {
	defs, err := vault.ListTopicDefinitions(cfg)
	if err != nil {
		return "", 0, err
	}
	if err := vault.WriteTopicIndex(cfg, defs); err != nil {
		return "", 0, err
	}
	return filepath.Join(cfg.VaultDir, filepath.FromSlash(vault.TopicIndexRelativePath())), len(defs), nil
}
