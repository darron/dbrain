package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dbrain/internal/ask"
	"dbrain/internal/config"
	"dbrain/internal/model"
	"dbrain/internal/sourceenrich"
	"dbrain/internal/store"
	"dbrain/internal/summarizecli"
)

const (
	defaultAddr           = "127.0.0.1:8742"
	defaultSearchLimit    = 10
	defaultAskLimit       = 8
	defaultActivityWindow = 24 * time.Hour
	maxSearchLimit        = 50
	maxAskLimit           = 20
)

//go:embed all:ui/dist
var embeddedUI embed.FS

type AppInfo struct {
	Name     string `json:"name"`
	RootDir  string `json:"root_dir"`
	VaultDir string `json:"vault_dir"`
	DBPath   string `json:"db_path"`
	HasFTS   bool   `json:"has_fts"`
}

type BootstrapResponse struct {
	App      AppInfo             `json:"app"`
	Backlog  store.BacklogStats  `json:"backlog"`
	Activity store.ActivityStats `json:"activity"`
}

type SearchResponse struct {
	Query   string               `json:"query"`
	Limit   int                  `json:"limit"`
	Results []model.SearchResult `json:"results"`
}

type GetResponse struct {
	Lookup        string                 `json:"lookup"`
	Kind          string                 `json:"kind"`
	Item          *model.Item            `json:"item,omitempty"`
	Source        *model.SourceDocument  `json:"source,omitempty"`
	LinkedSources []model.ItemSourceRef  `json:"linked_sources,omitempty"`
	Backlinks     []model.SourceBacklink `json:"backlinks,omitempty"`
	NoteContent   string                 `json:"note_content,omitempty"`
	NoteError     string                 `json:"note_error,omitempty"`
}

type AskRequest struct {
	Question       string   `json:"question"`
	Limit          int      `json:"limit"`
	SourceTypes    []string `json:"source_types"`
	IncludeRelated bool     `json:"include_related"`
	RelatedLimit   int      `json:"related_limit"`
	MaxCharsPerDoc int      `json:"max_chars_per_doc"`
}

type server struct {
	cfg         config.Config
	store       *store.Store
	static      http.Handler
	toolVersion string
}

func Serve(ctx context.Context, cfg config.Config, addr string) error {
	if strings.TrimSpace(addr) == "" {
		addr = defaultAddr
	}

	st, err := store.Open(cfg.DBPath)
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
	staticFS, err := fs.Sub(embeddedUI, "ui/dist")
	if err != nil {
		return nil, fmt.Errorf("load embedded ui: %w", err)
	}

	s := &server{
		cfg:         cfg,
		store:       st,
		static:      http.FileServerFS(staticFS),
		toolVersion: summarizecli.Version(context.Background(), ""),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/get", s.handleGet)
	mux.HandleFunc("/api/stats/backlog", s.handleBacklog)
	mux.HandleFunc("/api/stats/activity", s.handleActivity)
	mux.HandleFunc("/api/ask", s.handleAsk)
	mux.Handle("/", s.static)
	return mux, nil
}

func DefaultAddr() string {
	return defaultAddr
}

func (s *server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	backlog, err := s.backlog(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	activity, err := s.activity(r.Context(), defaultActivityWindow)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, BootstrapResponse{
		App: AppInfo{
			Name:     "dbrain",
			RootDir:  s.cfg.RootDir,
			VaultDir: s.cfg.VaultDir,
			DBPath:   s.cfg.DBPath,
			HasFTS:   s.store.HasFTS(),
		},
		Backlog:  backlog,
		Activity: activity,
	})
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := clampLimit(parseIntDefault(r.URL.Query().Get("limit"), defaultSearchLimit), 1, maxSearchLimit)
	if query == "" {
		writeJSON(w, http.StatusOK, SearchResponse{Query: "", Limit: limit, Results: []model.SearchResult{}})
		return
	}

	results, err := s.store.Search(r.Context(), query, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, SearchResponse{
		Query:   query,
		Limit:   limit,
		Results: results,
	})
}

func (s *server) handleGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	lookup := strings.TrimSpace(r.URL.Query().Get("lookup"))
	if lookup == "" {
		writeMessage(w, http.StatusBadRequest, "lookup is required")
		return
	}

	item, itemErr := s.store.GetItem(r.Context(), lookup)
	if itemErr == nil {
		noteContent, noteError := s.loadNote(item.NotePath)
		linkedSources, err := s.store.ListSourcesForItem(r.Context(), item.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, GetResponse{
			Lookup:        lookup,
			Kind:          "item",
			Item:          &item,
			LinkedSources: linkedSources,
			NoteContent:   noteContent,
			NoteError:     noteError,
		})
		return
	}

	source, sourceErr := s.store.GetSource(r.Context(), lookup)
	if sourceErr != nil {
		writeMessage(w, http.StatusNotFound, fmt.Sprintf("lookup not found: %s", lookup))
		return
	}

	noteContent, noteError := s.loadNote(source.NotePath)
	backlinks, err := s.store.ListBacklinksForSource(r.Context(), source.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, GetResponse{
		Lookup:      lookup,
		Kind:        "source",
		Source:      &source,
		Backlinks:   backlinks,
		NoteContent: noteContent,
		NoteError:   noteError,
	})
}

func (s *server) handleBacklog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	backlog, err := s.backlog(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, backlog)
}

func (s *server) handleActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	window := defaultActivityWindow
	if raw := strings.TrimSpace(r.URL.Query().Get("window")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			writeMessage(w, http.StatusBadRequest, "window must be a valid duration")
			return
		}
		window = parsed
	}

	activity, err := s.activity(r.Context(), window)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, activity)
}

func (s *server) handleAsk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req AskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}

	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		writeMessage(w, http.StatusBadRequest, "question is required")
		return
	}

	response, err := ask.Run(r.Context(), s.cfg, s.store, req.Question, ask.Options{
		Limit:          clampLimit(req.Limit, 1, maxAskLimit),
		RetrieveOnly:   true,
		MaxCharsPerDoc: req.MaxCharsPerDoc,
		SourceTypes:    req.SourceTypes,
		IncludeRelated: req.IncludeRelated,
		RelatedLimit:   req.RelatedLimit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *server) backlog(ctx context.Context) (store.BacklogStats, error) {
	return s.store.Backlog(ctx, sourceenrich.SummaryPromptVersion, summarizecli.ToolName, s.toolVersion)
}

func (s *server) activity(ctx context.Context, window time.Duration) (store.ActivityStats, error) {
	return s.store.Activity(ctx, time.Now().UTC(), window)
}

func (s *server) loadNote(notePath string) (string, string) {
	fullPath, err := s.resolveNotePath(notePath)
	if err != nil {
		return "", err.Error()
	}
	if fullPath == "" {
		return "", ""
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Sprintf("read note %s: %v", fullPath, err)
	}
	return string(content), ""
}

func (s *server) resolveNotePath(notePath string) (string, error) {
	notePath = strings.TrimSpace(notePath)
	if notePath == "" {
		return "", nil
	}

	fullPath := filepath.Clean(filepath.Join(s.cfg.VaultDir, filepath.FromSlash(notePath)))
	vaultRoot := filepath.Clean(s.cfg.VaultDir)
	if fullPath != vaultRoot && !strings.HasPrefix(fullPath, vaultRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("note path escapes vault: %s", notePath)
	}
	return fullPath, nil
}

func parseIntDefault(raw string, fallback int) int {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return value
}

func clampLimit(value int, minValue int, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(value)
}

func writeMessage(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": strings.TrimSpace(message)})
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeMessage(w, status, err.Error())
}

func writeMethodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	writeMessage(w, http.StatusMethodNotAllowed, "method not allowed")
}
