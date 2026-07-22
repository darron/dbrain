package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/httpsecurity"
	"github.com/darron/dbrain/internal/schedulerstate"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
)

const (
	defaultAddr                        = "127.0.0.1:8742"
	defaultSearchLimit                 = 25
	defaultResearchLimit               = 8
	defaultSynthesisBytes              = 5 << 20
	defaultTranscriptBytes             = 10 << 20
	defaultEventLimit                  = 8
	defaultActivityWindow              = 24 * time.Hour
	defaultSSEHeartbeat                = 5 * time.Second
	defaultResearchTimeout             = 2 * time.Minute
	defaultWebResearchRunnerTimeout    = 6 * time.Minute
	defaultWebResearchStageTimeout     = 2 * time.Minute
	defaultWebResearchSynthesisTimeout = 2 * time.Minute
	defaultWebCLI                      = "codex"
	maxSearchLimit                     = 50
	maxResearchLimit                   = 50
	maxEventLimit                      = 20
)

//go:embed all:ui/dist
var embeddedUI embed.FS

type server struct {
	cfg                   config.Config
	store                 *store.Store
	archive               archiveProxy
	proxyBase             string
	staticFS              fs.FS
	static                http.Handler
	indexHTML             []byte
	toolVersion           string
	schedulerStatus       func() schedulerstate.SyncAllStatus
	fullDiskPath          string
	auth                  *authManager
	auditReports          AuditReportReader
	auditRuns             *AuditRunCoordinator
	auditSyncInterval     time.Duration
	auditStandardInterval time.Duration
	auditNow              func() time.Time
}

type ServeOptions struct {
	StoreOpenOptions store.OpenOptions
	HandlerOptions   HandlerOptions
}

type AuditHandlerDependencies struct {
	Reports          AuditReportReader
	Runs             *AuditRunCoordinator
	SyncInterval     time.Duration
	StandardInterval time.Duration
}

type HandlerOptions struct {
	SchedulerStatus       func() schedulerstate.SyncAllStatus
	FullDiskAccessPath    string
	Context               context.Context
	LogOutput             io.Writer
	AuditReports          AuditReportReader
	AuditRuns             *AuditRunCoordinator
	AuditSyncInterval     time.Duration
	AuditStandardInterval time.Duration
	AuditNow              func() time.Time
}

func Serve(ctx context.Context, cfg config.Config, addr string) error {
	return ServeWithOptions(ctx, cfg, addr, ServeOptions{})
}

func ServeWithOptions(ctx context.Context, cfg config.Config, addr string, opts ServeOptions) error {
	if strings.TrimSpace(addr) == "" {
		addr = defaultAddr
	}

	st, err := store.OpenWithOptions(cfg.DBPath, opts.StoreOpenOptions)
	if err != nil {
		return err
	}
	defer func() {
		_ = st.Close()
	}()

	handlerOptions := opts.HandlerOptions
	handlerOptions.Context = ctx
	if handlerOptions.LogOutput == nil {
		handlerOptions.LogOutput = os.Stderr
	}
	handler, err := NewHandlerWithOptions(cfg, st, handlerOptions)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		errCh <- httpServer.Shutdown(shutdownCtx)
	}()

	listenErr := httpServer.ListenAndServe()
	if errors.Is(listenErr, http.ErrServerClosed) {
		listenErr = nil
	}

	select {
	case shutdownErr := <-errCh:
		if listenErr != nil {
			return listenErr
		}
		if shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) {
			return shutdownErr
		}
	default:
	}

	return listenErr
}

func NewHandler(cfg config.Config, st *store.Store) (http.Handler, error) {
	return NewHandlerWithOptions(cfg, st, HandlerOptions{})
}

func NewHandlerWithOptions(cfg config.Config, st *store.Store, opts HandlerOptions) (http.Handler, error) {
	startupCtx := opts.Context
	if startupCtx == nil {
		startupCtx = context.Background()
	}

	archive, err := newArchiveProxy(startupCtx, cfg)
	if err != nil {
		return nil, fmt.Errorf("configure archive proxy: %w", err)
	}

	staticFS, err := fs.Sub(embeddedUI, "ui/dist")
	if err != nil {
		return nil, fmt.Errorf("load embedded ui: %w", err)
	}
	indexHTML, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read embedded ui index: %w", err)
	}
	authCfg, err := loadAuthConfig(startupCtx, cfg)
	if err != nil {
		return nil, fmt.Errorf("load auth config: %w", err)
	}
	authManager, err := newAuthManagerWithContext(startupCtx, authCfg, st)
	if err != nil {
		return nil, fmt.Errorf("init auth manager: %w", err)
	}
	if authManager != nil {
		authManager.logOutput = opts.LogOutput
	}
	writeAuthStartupStatus(opts.LogOutput, authCfg)

	s := &server{
		cfg:                   cfg,
		store:                 st,
		archive:               archive,
		proxyBase:             mediaProxyBaseURL(cfg),
		staticFS:              staticFS,
		static:                http.FileServerFS(staticFS),
		indexHTML:             indexHTML,
		toolVersion:           summarizecli.Version(context.Background(), ""),
		schedulerStatus:       opts.SchedulerStatus,
		fullDiskPath:          opts.FullDiskAccessPath,
		auth:                  authManager,
		auditReports:          opts.AuditReports,
		auditRuns:             opts.AuditRuns,
		auditSyncInterval:     opts.AuditSyncInterval,
		auditStandardInterval: opts.AuditStandardInterval,
		auditNow:              opts.AuditNow,
	}

	return httpsecurity.OriginGuard(s.newMux()), nil
}

func writeAuthStartupStatus(out io.Writer, authCfg authConfig) {
	if out == nil {
		return
	}
	if !authCfg.Enabled {
		_, _ = fmt.Fprintln(out, "WARNING web auth disabled; all web routes are unauthenticated.")
		return
	}
	provider := "unknown"
	if len(authCfg.Providers) > 0 {
		provider = strings.Join(authCfg.Providers, ",")
	}
	_, _ = fmt.Fprintf(out, "Web auth enabled (provider: %s); sessions are in-memory and expire after %s; restarting the web process logs users out.\n", provider, authCfg.SessionTTL)
}

func (s *server) newMux() http.Handler {
	appMux := http.NewServeMux()
	appMux.HandleFunc("/api/audit", http.NotFound)
	appMux.HandleFunc("/api/audit/", http.NotFound)
	if s.auth != nil {
		appMux.HandleFunc("/api/audit/latest", s.handleAuditLatest)
		appMux.HandleFunc("/api/audit/history", s.handleAuditHistory)
		appMux.HandleFunc("/api/audit/run", s.handleAuditRun)
		appMux.HandleFunc("/api/audit/runs/", s.handleAuditRunStatus)
	}
	appMux.HandleFunc("/api/bootstrap", s.handleBootstrap)
	appMux.HandleFunc("/api/search", s.handleSearch)
	appMux.HandleFunc("/api/get", s.handleGet)
	appMux.HandleFunc("/api/whats-new", s.handleWhatsNew)
	appMux.HandleFunc("/api/stats/backlog", s.handleBacklog)
	appMux.HandleFunc("/api/stats/activity", s.handleActivity)
	appMux.HandleFunc("/api/stats/source-activity", s.handleSourceActivity)
	appMux.HandleFunc("/api/scheduler/sync-all", s.handleSchedulerSyncAll)
	appMux.HandleFunc("/api/doctor/full-disk-access", s.handleDoctorFullDiskAccess)
	appMux.HandleFunc("/api/ask", handleRemovedAPI)
	appMux.HandleFunc("/api/research", s.handleResearch)
	appMux.HandleFunc("/api/research/run", s.handleResearchRun)
	appMux.HandleFunc("/api/research/synthesize", s.handleResearchSynthesize)
	appMux.HandleFunc("/api/research/traces", s.handleResearchTraces)
	appMux.HandleFunc("/api/research/trace-compare", s.handleResearchTraceCompare)
	appMux.HandleFunc("/api/chat/transcripts", s.handleChatTranscriptSave)
	appMux.HandleFunc("/api/chat/shares", s.handleChatShares)
	appMux.HandleFunc("/api/links", s.handleLinks)
	appMux.HandleFunc("/api/tag", s.handleTag)
	appMux.HandleFunc("/api/media/signed-url", s.handleMediaSignedURL)
	appMux.HandleFunc("/media/asset/", s.handleMediaAsset)
	appMux.HandleFunc("/share/", s.handlePublicShare)
	appMux.Handle("/", http.HandlerFunc(s.handleStatic))

	if s.auth == nil {
		return appMux
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/share/", s.handlePublicShare)
	mux.HandleFunc("/login", s.auth.handleLogin)
	mux.HandleFunc("/logout", s.auth.handleLogout)
	mux.HandleFunc("/auth/", s.auth.handleAuth)
	mux.Handle("/", s.auth.requireAuth(appMux))
	return mux
}

func handleRemovedAPI(w http.ResponseWriter, _ *http.Request) {
	writeMessage(w, http.StatusNotFound, "endpoint removed")
}

func DefaultAddr() string {
	return defaultAddr
}
