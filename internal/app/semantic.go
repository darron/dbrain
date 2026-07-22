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
		newSemanticVerifyCommand(root, deps),
	)
	return cmd
}

func newSemanticVerifyCommand(root *rootOptions, deps semanticDeps) *cobra.Command {
	var limit int
	var resume string
	var repairCounters bool
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "verify", Short: "Verify a bounded page of stored semantic vectors", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit <= 0 || limit > 5_000 {
				return fmt.Errorf("limit must be between 1 and 5000")
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
			profile := semanticbuild.Profile(embedding.Info{Provider: string(semantic.Provider), Model: semantic.Model, Dimensions: semantic.Dimensions})
			_, err = profile.ID()
			if err != nil {
				return err
			}
			st, err := deps.openWritable(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			progress, err := semanticbuild.RunVerify(cmd.Context(), st, semanticbuild.VerifyOptions{Profile: profile, Limit: limit, Resume: resume, RepairCounters: repairCounters})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), progress)
			}
			return writeSemanticVerifyProgress(cmd.OutOrStdout(), progress)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", defaultSemanticLimit, "Maximum ready vector rows to verify (maximum 5000)")
	cmd.Flags().StringVar(&resume, "resume", "", "Resume after this chunk ID")
	cmd.Flags().BoolVar(&repairCounters, "repair-counters", false, "Atomically rebuild semantic readiness counters before verification")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print progress as JSON")
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
				status, err := semanticbuild.ReadStatus(cmd.Context(), nil, embedding.Profile{}, false, semantic.Mode != semanticconfig.ModeOff, semantic.ExactFallbackMaxChunks, time.Now().UTC())
				if err != nil {
					return err
				}
				status.Mode = string(semantic.Mode)
				return outputSemanticStatus(cmd, status, jsonOut)
			}
			profile := semanticbuild.Profile(embedding.Info{
				Provider: string(semantic.Provider), Model: semantic.Model, Dimensions: semantic.Dimensions,
			})
			profileID, err := profile.ID()
			if err != nil {
				return err
			}
			st, err := deps.openReadOnly(cfg.DBPath)
			if err != nil {
				status, statusErr := semanticbuild.ReadStatus(cmd.Context(), nil, profile, configured, semantic.Mode != semanticconfig.ModeOff, semantic.ExactFallbackMaxChunks, time.Now().UTC())
				if statusErr != nil {
					return statusErr
				}
				status.Mode, status.ProfileID = string(semantic.Mode), profileID
				status.Problems = append(status.Problems, err.Error())
				status.Reason += "; storage diagnostics are unavailable: " + err.Error()
				return outputSemanticStatus(cmd, status, jsonOut)
			}
			defer func() { _ = st.Close() }()
			status, err := semanticbuild.ReadStatus(cmd.Context(), st, profile, configured, semantic.Mode != semanticconfig.ModeOff, semantic.ExactFallbackMaxChunks, time.Now().UTC())
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
	var untilIdle bool
	var maxDuration time.Duration
	var jsonOut bool
	cmd := &cobra.Command{
		Use: "chunk", Short: "Build deterministic retrieval chunks", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit <= 0 {
				return fmt.Errorf("limit must be positive")
			}
			if maxDuration < 0 {
				return fmt.Errorf("max-duration must not be negative")
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
			chunkOpts := semanticbuild.ChunkOptions{Limit: limit, AfterSourceKey: afterSourceKey, UntilIdle: untilIdle, MaxDuration: maxDuration}
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
	cmd.Flags().BoolVar(&untilIdle, "until-idle", false, "Continue through the durable dirty queue until no work remains")
	cmd.Flags().DurationVar(&maxDuration, "max-duration", 0, "Maximum command runtime before a graceful resumable stop (0 is unlimited)")
	cmd.Flags().StringVar(&afterSourceKey, "after-source-key", "", "Deprecated source-key cursor")
	if err := cmd.Flags().MarkDeprecated("after-source-key", "the durable dirty queue resumes automatically; rerun without this flag"); err != nil {
		panic(err)
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print progress as JSON")
	return cmd
}

func newSemanticEmbedCommand(root *rootOptions, deps semanticDeps) *cobra.Command {
	var limit, batchSize int
	var untilIdle bool
	var maxDuration time.Duration
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
			if maxDuration < 0 {
				return fmt.Errorf("max-duration must not be negative")
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
			embedOpts := semanticbuild.EmbedOptions{Limit: limit, BatchSize: batchSize, UntilIdle: untilIdle, MaxDuration: maxDuration}
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
	cmd.Flags().BoolVar(&untilIdle, "until-idle", false, "Continue through durable embedding work until no eligible chunks remain")
	cmd.Flags().DurationVar(&maxDuration, "max-duration", 0, "Maximum command runtime before a graceful resumable stop (0 is unlimited)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print progress as JSON")
	return cmd
}
