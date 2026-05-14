package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/schedulerstate"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
)

const (
	defaultAddr            = "127.0.0.1:8742"
	defaultSearchLimit     = 25
	defaultResearchLimit   = 8
	defaultSynthesisBytes  = 5 << 20
	defaultTranscriptBytes = 10 << 20
	defaultEventLimit      = 8
	defaultActivityWindow  = 24 * time.Hour
	defaultSSEHeartbeat    = 5 * time.Second
	defaultResearchTimeout = 45 * time.Second
	defaultWebCLI          = "codex"
	maxSearchLimit         = 50
	maxResearchLimit       = 50
	maxEventLimit          = 20
)

//go:embed all:ui/dist
var embeddedUI embed.FS

type server struct {
	cfg             config.Config
	store           *store.Store
	archive         archiveProxy
	proxyBase       string
	staticFS        fs.FS
	static          http.Handler
	indexHTML       []byte
	toolVersion     string
	schedulerStatus func() schedulerstate.SyncAllStatus
	fullDiskPath    string
	auth            *authManager
}

type ServeOptions struct {
	StoreOpenOptions store.OpenOptions
}

type HandlerOptions struct {
	SchedulerStatus    func() schedulerstate.SyncAllStatus
	FullDiskAccessPath string
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

	handler, err := NewHandler(cfg, st)
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
	archive, err := newArchiveProxy(cfg)
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
	authCfg, err := loadAuthConfig(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("load auth config: %w", err)
	}
	authManager, err := newAuthManager(authCfg, st)
	if err != nil {
		return nil, fmt.Errorf("init auth manager: %w", err)
	}

	s := &server{
		cfg:             cfg,
		store:           st,
		archive:         archive,
		proxyBase:       mediaProxyBaseURL(cfg),
		staticFS:        staticFS,
		static:          http.FileServerFS(staticFS),
		indexHTML:       indexHTML,
		toolVersion:     summarizecli.Version(context.Background(), ""),
		schedulerStatus: opts.SchedulerStatus,
		fullDiskPath:    opts.FullDiskAccessPath,
		auth:            authManager,
	}

	return s.newMux(), nil
}

func (s *server) newMux() http.Handler {
	appMux := http.NewServeMux()
	appMux.HandleFunc("/api/bootstrap", s.handleBootstrap)
	appMux.HandleFunc("/api/search", s.handleSearch)
	appMux.HandleFunc("/api/get", s.handleGet)
	appMux.HandleFunc("/api/stats/backlog", s.handleBacklog)
	appMux.HandleFunc("/api/stats/activity", s.handleActivity)
	appMux.HandleFunc("/api/stats/source-activity", s.handleSourceActivity)
	appMux.HandleFunc("/api/scheduler/sync-all", s.handleSchedulerSyncAll)
	appMux.HandleFunc("/api/doctor/full-disk-access", s.handleDoctorFullDiskAccess)
	appMux.HandleFunc("/api/ask", handleRemovedAPI)
	appMux.HandleFunc("/api/research", s.handleResearch)
	appMux.HandleFunc("/api/research/synthesize", s.handleResearchSynthesize)
	appMux.HandleFunc("/api/chat/transcripts", s.handleChatTranscriptSave)
	appMux.HandleFunc("/api/links", s.handleLinks)
	appMux.HandleFunc("/api/tag", s.handleTag)
	appMux.HandleFunc("/api/media/signed-url", s.handleMediaSignedURL)
	appMux.HandleFunc("/media/asset/", s.handleMediaAsset)
	appMux.Handle("/", http.HandlerFunc(s.handleStatic))

	if s.auth == nil {
		return appMux
	}

	mux := http.NewServeMux()
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
