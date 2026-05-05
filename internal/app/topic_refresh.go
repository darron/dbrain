package app

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/topics"
	"github.com/darron/dbrain/internal/vault"
)

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
