package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/store"
)

const (
	defaultSemanticLimit     = 100
	defaultSemanticBatchSize = 16
)

type semanticDeps struct {
	loadReadConfig    func(context.Context, string, string) (config.Config, error)
	loadWriteConfig   func(string, ...string) (config.Config, error)
	openReadOnly      func(string) (*store.Store, error)
	openWritable      func(string) (*store.Store, error)
	resolve           func(string) (semanticconfig.Config, error)
	resolveDiagnostic func(string) (semanticconfig.Config, error)
	provider          func(semanticconfig.Config) (embedding.Provider, error)
}

func defaultSemanticDeps() semanticDeps {
	return semanticDeps{
		loadReadConfig: func(ctx context.Context, root, configFile string) (config.Config, error) {
			cfg, _, err := loadAuditConfigContext(ctx, root, configFile)
			return cfg, err
		},
		loadWriteConfig:   loadConfig,
		openReadOnly:      store.OpenReadOnly,
		openWritable:      store.Open,
		resolve:           semanticconfig.Resolve,
		resolveDiagnostic: semanticconfig.ResolveDiagnostic,
		provider: func(cfg semanticconfig.Config) (embedding.Provider, error) {
			return embedding.NewOllama(embedding.OllamaOptions{
				BaseURL: cfg.OllamaBaseURL, Model: cfg.Model, Dimensions: cfg.Dimensions,
			})
		},
	}
}

func newSemanticCommand(root *rootOptions) *cobra.Command {
	return newSemanticCommandWithDeps(root, defaultSemanticDeps())
}

func newSemanticCommandWithDeps(root *rootOptions, deps semanticDeps) *cobra.Command {
	cmd := &cobra.Command{Use: "semantic", Short: "Build and inspect semantic retrieval state", RunE: helpCommand}
	cmd.AddCommand(
		newSemanticStatusCommand(root, deps),
		newSemanticChunkCommand(root, deps),
		newSemanticEmbedCommand(root, deps),
	)
	return cmd
}

func newSemanticStatusCommand(root *rootOptions, deps semanticDeps) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "status", Short: "Inspect semantic configuration and derived-state readiness", Args: cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := deps.loadReadConfig(cmd.Context(), root.root, root.configFile)
			if err != nil {
				return err
			}
			runtimeenv.RegisterConfigFile(cfg.RootDir, cfg.ConfigPath)
			semantic, err := deps.resolveDiagnostic(cfg.RootDir)
			if err != nil {
				return err
			}
			configured := strings.TrimSpace(semantic.Model) != "" && semantic.Dimensions > 0
			if !configured {
				status, err := semanticbuild.ReadStatus(cmd.Context(), nil, "", false, semantic.Mode != semanticconfig.ModeOff, time.Now().UTC())
				if err != nil {
					return err
				}
				status.Mode = string(semantic.Mode)
				return outputSemanticStatus(cmd, status, jsonOut)
			}
			profileID, err := semanticbuild.Profile(embedding.Info{
				Provider: string(semantic.Provider), Model: semantic.Model, Dimensions: semantic.Dimensions,
			}).ID()
			if err != nil {
				return err
			}
			st, err := deps.openReadOnly(cfg.DBPath)
			if err != nil {
				state, reason := "unavailable", "semantic storage is unavailable: "+err.Error()
				if semantic.Mode == semanticconfig.ModeOff {
					state, reason = "disabled", "semantic retrieval mode is off; storage diagnostics are unavailable: "+err.Error()
				}
				status := semanticbuild.Status{Status: state, Reason: reason, Mode: string(semantic.Mode), ProfileID: profileID, Problems: []string{err.Error()}, Next: make([]string, 0)}
				return outputSemanticStatus(cmd, status, jsonOut)
			}
			defer func() { _ = st.Close() }()
			status, err := semanticbuild.ReadStatus(cmd.Context(), st, profileID, configured, semantic.Mode != semanticconfig.ModeOff, time.Now().UTC())
			if err != nil {
				return err
			}
			status.Mode, status.ProfileID = string(semantic.Mode), profileID
			return outputSemanticStatus(cmd, status, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print status as JSON")
	return cmd
}

func outputSemanticStatus(cmd *cobra.Command, status semanticbuild.Status, jsonOut bool) error {
	if jsonOut {
		return writeJSON(cmd.OutOrStdout(), status)
	}
	return writeSemanticStatus(cmd.OutOrStdout(), status)
}

func newSemanticChunkCommand(root *rootOptions, deps semanticDeps) *cobra.Command {
	var limit int
	var afterSourceKey string
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "chunk", Short: "Build deterministic retrieval chunks", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit <= 0 {
				return fmt.Errorf("limit must be positive")
			}
			cfg, err := deps.loadWriteConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			st, err := deps.openWritable(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			chunkOpts := semanticbuild.ChunkOptions{Limit: limit, AfterSourceKey: afterSourceKey}
			if !jsonOut {
				chunkOpts.Progress = func(snapshot semanticbuild.ChunkProgress) error {
					return writeSemanticProgressSnapshot(cmd.OutOrStdout(), snapshot.Progress)
				}
			}
			progress, err := semanticbuild.RunChunk(cmd.Context(), st, chunkOpts)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), progress)
			}
			return writeSemanticChunkProgress(cmd.OutOrStdout(), progress)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", defaultSemanticLimit, "Maximum dirty parents to process")
	cmd.Flags().StringVar(&afterSourceKey, "after-source-key", "", "Deprecated source-key cursor")
	if err := cmd.Flags().MarkDeprecated("after-source-key", "the durable dirty queue resumes automatically; rerun without this flag"); err != nil {
		panic(err)
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print progress as JSON")
	return cmd
}

func newSemanticEmbedCommand(root *rootOptions, deps semanticDeps) *cobra.Command {
	var limit, batchSize int
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "embed", Short: "Generate missing embeddings for the configured profile", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit <= 0 {
				return fmt.Errorf("limit must be positive")
			}
			if batchSize <= 0 {
				return fmt.Errorf("batch-size must be positive")
			}
			cfg, err := deps.loadWriteConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			semantic, err := deps.resolve(cfg.RootDir)
			if err != nil {
				return err
			}
			if strings.TrimSpace(semantic.Model) == "" || semantic.Dimensions <= 0 {
				return fmt.Errorf("semantic embedding model and positive dimensions are not configured")
			}
			provider, err := deps.provider(semantic)
			if err != nil {
				return err
			}
			st, err := deps.openWritable(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			embedOpts := semanticbuild.EmbedOptions{Limit: limit, BatchSize: batchSize}
			if !jsonOut {
				embedOpts.Progress = func(snapshot semanticbuild.Progress) error {
					return writeSemanticProgressSnapshot(cmd.OutOrStdout(), snapshot)
				}
			}
			progress, err := semanticbuild.RunEmbed(cmd.Context(), st, provider, embedOpts)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), progress)
			}
			return writeSemanticProgress(cmd.OutOrStdout(), progress)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", defaultSemanticLimit, "Maximum chunk rows to process")
	cmd.Flags().IntVar(&batchSize, "batch-size", defaultSemanticBatchSize, "Embedding request batch size")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print progress as JSON")
	return cmd
}
