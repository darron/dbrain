package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/runtimeenv"
	"github.com/darron/dbrain/web"
)

func buildWebAuditHandlerDependencies(ctx context.Context, cfg config.Config, meta auditConfigMeta) (web.AuditHandlerDependencies, error) {
	configSnapshot, err := runtimeenv.LoadConfigSnapshot(ctx, cfg.ConfigPath, auditConfigMaxBytes)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return web.AuditHandlerDependencies{}, fmt.Errorf("read bounded web audit config: %w", err)
		}
		configSnapshot = map[string]any{}
	}
	dotenvSnapshot, err := runtimeenv.LoadDotEnvSnapshot(ctx, cfg.RootDir, auditConfigMaxBytes)
	if err != nil {
		return web.AuditHandlerDependencies{}, fmt.Errorf("read bounded web audit dotenv: %w", err)
	}
	cleanupSnapshot := runtimeenv.RegisterConfigSnapshot(cfg.RootDir, configSnapshot, dotenvSnapshot)
	defer cleanupSnapshot()

	syncOptions, err := schedulerSyncConfigFromRuntime(cfg.RootDir)
	if err != nil {
		return web.AuditHandlerDependencies{}, err
	}
	auditOptions, err := schedulerAuditConfigFromRuntime(cfg.RootDir)
	if err != nil {
		return web.AuditHandlerDependencies{}, err
	}
	features, err := resolveAuditFeatures(cfg, meta)
	if err != nil {
		return web.AuditHandlerDependencies{}, fmt.Errorf("configure web audit features: %w", err)
	}
	runner, err := newScheduledAuditRunner(ctx, cfg, features)
	if err != nil {
		return web.AuditHandlerDependencies{}, fmt.Errorf("configure web audit runner: %w", err)
	}
	reports, err := audit.NewReportStore(cfg.LogDir)
	if err != nil {
		return web.AuditHandlerDependencies{}, fmt.Errorf("configure web audit report store: %w", err)
	}
	runs := web.NewAuditRunCoordinator(ctx, web.AuditRunCoordinatorOptions{
		Runner: func(runCtx context.Context, profile audit.Profile) (audit.Report, error) {
			return runner(runCtx, profile, auditOptions.Since)
		},
		Reports:          reports,
		SyncInterval:     syncOptions.Interval,
		StandardInterval: auditOptions.StandardInterval,
	})
	return web.AuditHandlerDependencies{Reports: reports, Runs: runs, SyncInterval: syncOptions.Interval, StandardInterval: auditOptions.StandardInterval}, nil
}
