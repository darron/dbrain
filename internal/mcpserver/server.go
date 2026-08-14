package mcpserver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/brainresearch"
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
	lifecycle     *serverLifecycle
}

var errServerClosed = errors.New("mcp server is shut down")

type serverLifecycle struct {
	runtime           *brainresearch.Runtime
	buildResearchPack func(context.Context, brainresearch.Options) (brainresearch.Pack, error)
	shutdownRuntime   func(context.Context) error

	mu           sync.Mutex
	active       int
	closing      bool
	requestsDone chan struct{}
	requestsOnce sync.Once
	closeOnce    sync.Once
	closeDone    chan struct{}
	closeErr     error
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
	runtime := brainresearch.NewRuntime(cfg, st)
	lifecycle := &serverLifecycle{
		runtime:           runtime,
		buildResearchPack: runtime.Build,
		shutdownRuntime:   runtime.Shutdown,
		requestsDone:      make(chan struct{}),
		closeDone:         make(chan struct{}),
	}
	return &Server{cfg: cfg, st: st, deps: deps, newAuditTimer: newWallClockAuditTimer, lifecycle: lifecycle}
}

func (l *serverLifecycle) beginRequest() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closing {
		return errServerClosed
	}
	l.active++
	return nil
}

func (l *serverLifecycle) endRequest() {
	l.mu.Lock()
	if l.active > 0 {
		l.active--
	}
	l.signalRequestsDoneLocked()
	l.mu.Unlock()
}

func (l *serverLifecycle) signalRequestsDoneLocked() {
	if l.closing && l.active == 0 {
		l.requestsOnce.Do(func() { close(l.requestsDone) })
	}
}

func (l *serverLifecycle) startClose() {
	l.mu.Lock()
	l.closing = true
	l.signalRequestsDoneLocked()
	l.mu.Unlock()
	l.closeOnce.Do(func() {
		go func() {
			<-l.requestsDone
			l.closeErr = l.shutdownRuntime(context.Background())
			close(l.closeDone)
		}()
	})
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.lifecycle == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.lifecycle.startClose()
	select {
	case <-s.lifecycle.closeDone:
		return s.lifecycle.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) Close() error {
	return s.Shutdown(context.Background())
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
