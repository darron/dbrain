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
	}

	return s.newMux(), nil
}

func (s *server) newMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/get", s.handleGet)
	mux.HandleFunc("/api/stats/backlog", s.handleBacklog)
	mux.HandleFunc("/api/stats/activity", s.handleActivity)
	mux.HandleFunc("/api/stats/source-activity", s.handleSourceActivity)
	mux.HandleFunc("/api/scheduler/sync-all", s.handleSchedulerSyncAll)
	mux.HandleFunc("/api/doctor/full-disk-access", s.handleDoctorFullDiskAccess)
	mux.HandleFunc("/api/ask", handleRemovedAPI)
	mux.HandleFunc("/api/research", s.handleResearch)
	mux.HandleFunc("/api/research/synthesize", s.handleResearchSynthesize)
	mux.HandleFunc("/api/chat/transcripts", s.handleChatTranscriptSave)
	mux.HandleFunc("/api/links", s.handleLinks)
	mux.HandleFunc("/api/tag", s.handleTag)
	mux.HandleFunc("/api/media/signed-url", s.handleMediaSignedURL)
	mux.HandleFunc("/media/asset/", s.handleMediaAsset)
	mux.Handle("/", http.HandlerFunc(s.handleStatic))
	return mux
}

func handleRemovedAPI(w http.ResponseWriter, _ *http.Request) {
	writeMessage(w, http.StatusNotFound, "endpoint removed")
}

func DefaultAddr() string {
	return defaultAddr
}
