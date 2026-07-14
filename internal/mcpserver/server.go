package mcpserver

import (
	"context"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/version"
)

const protocolVersion = "2025-03-26"

type Server struct {
	cfg           config.Config
	st            *store.Store
	deps          ServerDependencies
	capabilities  transportCapabilities
	newAuditTimer func(time.Duration) auditTimer
}

func New(cfg config.Config, st *store.Store) *Server {
	return NewWithDependencies(cfg, st, ServerDependencies{})
}

type AuditReportReader interface {
	Latest(audit.Profile) (*audit.Report, error)
}

type AuditDependencies struct {
	RunFast          func(context.Context) (audit.Report, error)
	Reports          AuditReportReader
	SyncInterval     time.Duration
	StandardInterval time.Duration
	Now              func() time.Time
}

type ServerDependencies struct {
	Audit AuditDependencies
}

type transportCapabilities struct {
	audit bool
}

type auditTimer struct {
	done <-chan time.Time
	stop func() bool
}

func newWallClockAuditTimer(timeout time.Duration) auditTimer {
	timer := time.NewTimer(timeout)
	return auditTimer{done: timer.C, stop: timer.Stop}
}

func NewWithDependencies(cfg config.Config, st *store.Store, deps ServerDependencies) *Server {
	return &Server{cfg: cfg, st: st, deps: deps, newAuditTimer: newWallClockAuditTimer}
}

func firstServerDependencies(values []ServerDependencies) ServerDependencies {
	if len(values) == 0 {
		return ServerDependencies{}
	}
	return values[0]
}

func (s *Server) withTransportCapabilities(capabilities transportCapabilities) *Server {
	clone := *s
	clone.capabilities = capabilities
	return &clone
}

func serverVersion() string {
	details := version.Current()
	if short := strings.TrimSpace(details.Short); short != "" && short != "unknown" {
		if strings.TrimSpace(strings.ToLower(details.GitStatus)) == "modified" && !strings.Contains(short, "+dirty") {
			return short + "+dirty"
		}
		return short
	}
	if moduleVersion := strings.TrimSpace(details.ModuleVersion); moduleVersion != "" && moduleVersion != "(devel)" {
		return moduleVersion
	}
	return "unknown"
}
