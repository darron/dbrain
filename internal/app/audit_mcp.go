package app

import (
	"context"
	"errors"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/mcpserver"
	"github.com/darron/dbrain/internal/store"
)

const mcpAuditSince = 7 * 24 * time.Hour

var errMCPAuditRemoteAuthority = errors.New("refuse MCP audit runner with remote archive authority")

func resolveMCPAuditServerDependencies(ctx context.Context, cfg config.Config, meta auditConfigMeta) (mcpserver.ServerDependencies, error) {
	features, err := resolveAuditFeatures(cfg, meta)
	if err != nil {
		return mcpserver.ServerDependencies{}, err
	}
	syncOptions, err := schedulerSyncConfigFromRuntime(cfg.RootDir)
	if err != nil {
		return mcpserver.ServerDependencies{}, err
	}
	auditOptions, err := schedulerAuditConfigFromRuntime(cfg.RootDir)
	if err != nil {
		return mcpserver.ServerDependencies{}, err
	}
	return newMCPAuditServerDependencies(ctx, cfg, features, syncOptions.Interval, auditOptions.StandardInterval)
}

func newMCPAuditServerDependencies(ctx context.Context, cfg config.Config, features audit.Features, syncInterval, standardInterval time.Duration) (mcpserver.ServerDependencies, error) {
	base, err := buildAuditDependencies(ctx, cfg, nil, audit.Request{Profile: audit.ProfileFast, Since: mcpAuditSince}, features)
	if err != nil {
		return mcpserver.ServerDependencies{}, err
	}
	// Fast-profile dependency construction must remain local-only. Refuse to
	// hand MCP a runner closure if future changes accidentally attach remote
	// object metadata authority here.
	if base.Archives != nil || base.Media != nil {
		return mcpserver.ServerDependencies{}, errMCPAuditRemoteAuthority
	}
	reports, err := audit.NewReportReader(cfg.LogDir)
	if err != nil {
		return mcpserver.ServerDependencies{}, err
	}
	runFast := func(runCtx context.Context) (audit.Report, error) {
		st, err := store.OpenReadOnlyContext(runCtx, cfg.DBPath)
		if err != nil {
			return audit.Report{}, err
		}
		defer func() { _ = st.Close() }()
		snapshot, err := st.BeginAuditReadSnapshot(runCtx)
		if err != nil {
			return audit.Report{}, err
		}
		defer func() { _ = snapshot.Close() }()
		deps := base
		deps.Store = auditSnapshotAdapter{snapshot: snapshot}
		deps.Features.DatabaseOpenedQueryOnly = true
		return audit.Run(runCtx, audit.Request{Profile: audit.ProfileFast, Since: mcpAuditSince}, deps)
	}
	return mcpserver.ServerDependencies{Audit: mcpserver.AuditDependencies{
		RunFast:          runFast,
		Reports:          reports,
		SyncInterval:     syncInterval,
		StandardInterval: standardInterval,
	}}, nil
}
