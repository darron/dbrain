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

	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/linkadd"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/summarizecli"
)

const (
	defaultAddr           = "127.0.0.1:8742"
	defaultSearchLimit    = 25
	defaultResearchLimit  = 8
	defaultSynthesisBytes = 5 << 20
	defaultEventLimit     = 8
	defaultActivityWindow = 24 * time.Hour
	defaultSSEHeartbeat   = 5 * time.Second
	defaultWebCLI         = "codex"
	maxSearchLimit        = 50
	maxResearchLimit      = 50
	maxEventLimit         = 20
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
	App            AppInfo                  `json:"app"`
	Backlog        store.BacklogStats       `json:"backlog"`
	Activity       store.ActivityStats      `json:"activity"`
	SourceActivity store.SourceActivityFeed `json:"source_activity"`
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
	QuotedPosts   []model.Item           `json:"quoted_posts,omitempty"`
	NoteContent   string                 `json:"note_content,omitempty"`
	NoteError     string                 `json:"note_error,omitempty"`
}

type ResearchRequest struct {
	Question          string   `json:"question"`
	Topic             string   `json:"topic"`
	Limit             int      `json:"limit"`
	SourceTypes       []string `json:"source_types"`
	IncludeRelated    bool     `json:"include_related"`
	RelatedLimit      int      `json:"related_limit"`
	SeedLimit         int      `json:"seed_limit"`
	IncludeTopicBrief *bool    `json:"include_topic_brief"`
	MaxCharsPerDoc    int      `json:"max_chars_per_doc"`
}

type ResearchSynthesisRequest struct {
	Question         string             `json:"question"`
	ResearchPack     brainresearch.Pack `json:"research_pack"`
	Model            string             `json:"model"`
	MaxEvidenceChars int                `json:"max_evidence_chars"`
}

type LinkAddRequest struct {
	URL    string   `json:"url"`
	URLs   []string `json:"urls"`
	Enrich bool     `json:"enrich"`
}

type server struct {
	cfg         config.Config
	store       *store.Store
	archive     archiveProxy
	proxyBase   string
	staticFS    fs.FS
	static      http.Handler
	indexHTML   []byte
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
		cfg:         cfg,
		store:       st,
		archive:     archive,
		proxyBase:   mediaProxyBaseURL(cfg),
		staticFS:    staticFS,
		static:      http.FileServerFS(staticFS),
		indexHTML:   indexHTML,
		toolVersion: summarizecli.Version(context.Background(), ""),
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
	mux.HandleFunc("/api/ask", handleRemovedAPI)
	mux.HandleFunc("/api/research", s.handleResearch)
	mux.HandleFunc("/api/research/synthesize", s.handleResearchSynthesize)
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

func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeMethodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}

	cleaned := strings.TrimPrefix(filepath.Clean(r.URL.Path), "/")
	if cleaned == "." || cleaned == "" {
		s.serveIndex(w, r)
		return
	}

	if s.hasStaticAsset(cleaned) {
		s.static.ServeHTTP(w, r)
		return
	}

	if filepath.Ext(cleaned) != "" {
		http.NotFound(w, r)
		return
	}

	s.serveIndex(w, r)
}

func (s *server) hasStaticAsset(name string) bool {
	file, err := s.staticFS.Open(name)
	if err != nil {
		return false
	}
	defer func() {
		_ = file.Close()
	}()

	info, err := file.Stat()
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func (s *server) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(s.indexHTML)
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
	sourceActivity, err := s.sourceActivity(r.Context(), store.SourceActivityFilter{Limit: defaultEventLimit})
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
		Backlog:        backlog,
		Activity:       activity,
		SourceActivity: sourceActivity,
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
		quotedPosts := s.loadQuotedPosts(r.Context(), item.ID)
		writeJSON(w, http.StatusOK, GetResponse{
			Lookup:        lookup,
			Kind:          "item",
			Item:          &item,
			LinkedSources: linkedSources,
			QuotedPosts:   quotedPosts,
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

type TagRequest struct {
	Lookup string `json:"lookup"`
	Tags   string `json:"tags"`
}

func (s *server) handleTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req TagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Lookup == "" {
		writeMessage(w, http.StatusBadRequest, "lookup is required")
		return
	}
	item, err := s.store.GetItem(r.Context(), req.Lookup)
	if err == nil {
		if err := s.store.SaveItemUserTags(r.Context(), item.ID, req.Tags); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		item.UserTags = req.Tags
		writeJSON(w, http.StatusOK, item)
		return
	}

	source, err := s.store.GetSource(r.Context(), req.Lookup)
	if err != nil {
		writeMessage(w, http.StatusNotFound, fmt.Sprintf("item or source not found: %s", req.Lookup))
		return
	}
	if err := s.store.SaveSourceUserTags(r.Context(), source.ID, req.Tags); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	source.UserTags = req.Tags
	writeJSON(w, http.StatusOK, source)
}

func (s *server) loadQuotedPosts(ctx context.Context, itemID int64) []model.Item {
	childIDs, err := s.store.ListItemChildLinks(ctx, itemID, "quoted_post")
	if err != nil || len(childIDs) == 0 {
		return nil
	}
	posts := make([]model.Item, 0, len(childIDs))
	for _, id := range childIDs {
		child, err := s.store.GetItemByID(ctx, id)
		if err == nil {
			posts = append(posts, child)
		}
	}
	return posts
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

func (s *server) handleResearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req ResearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}

	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		writeMessage(w, http.StatusBadRequest, "question is required")
		return
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultResearchLimit
	}
	pack, err := brainresearch.Build(r.Context(), s.cfg, s.store, brainresearch.Options{
		Question:       req.Question,
		Topic:          req.Topic,
		Limit:          clampLimit(limit, 1, maxResearchLimit),
		SourceTypes:    req.SourceTypes,
		IncludeRelated: req.IncludeRelated,
		RelatedLimit:   req.RelatedLimit,
		SeedLimit:      req.SeedLimit,
		IncludeTopic:   req.IncludeTopicBrief,
		MaxCharsPerDoc: req.MaxCharsPerDoc,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, pack)
}

func (s *server) handleResearchSynthesize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req ResearchSynthesisRequest
	limitedBody := http.MaxBytesReader(w, r.Body, defaultSynthesisBytes)
	if err := json.NewDecoder(limitedBody).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}

	prepared, err := brainresearch.PrepareSynthesis(s.cfg, brainresearch.SynthesisOptions{
		Question:         req.Question,
		Pack:             req.ResearchPack,
		Model:            req.Model,
		CLI:              defaultWebCLI,
		MaxEvidenceChars: req.MaxEvidenceChars,
	})
	if err != nil {
		if errors.Is(err, brainresearch.ErrSynthesisUnavailable) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"error":           err.Error(),
				"answer_status":   "unavailable",
				"answer_warnings": []string{"model_unavailable"},
			})
			return
		}
		writeMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeMessage(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	writeSSE(w, flusher, "start", map[string]interface{}{
		"schema_version":        prepared.SchemaVersion,
		"model":                 prepared.Model,
		"prompt_version":        prepared.PromptVersion,
		"evidence_budget_chars": prepared.Truncation.EvidenceBudgetChars,
		"truncation":            prepared.Truncation,
		"answer_warnings":       prepared.Warnings,
		"answer_status":         prepared.Status,
	})

	if prepared.Status == "no_evidence" {
		writeSSE(w, flusher, "done", brainresearch.SynthesisResult{
			SchemaVersion: prepared.SchemaVersion,
			Question:      prepared.Question,
			AnswerStatus:  "no_evidence",
			Warnings:      prepared.Warnings,
			Truncation:    prepared.Truncation,
			Citations:     prepared.Citations,
			PromptVersion: prepared.PromptVersion,
			Model:         prepared.Model,
		})
		return
	}

	type synthesisOutcome struct {
		Result brainresearch.SynthesisResult
		Err    error
	}
	resultCh := make(chan synthesisOutcome, 1)
	go func() {
		result, err := brainresearch.RunPreparedSynthesis(r.Context(), s.cfg, prepared, brainresearch.SynthesisOptions{
			CLI:              defaultWebCLI,
			Model:            req.Model,
			MaxEvidenceChars: req.MaxEvidenceChars,
		})
		resultCh <- synthesisOutcome{Result: result, Err: err}
	}()

	ticker := time.NewTicker(defaultSSEHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			writeSSE(w, flusher, "heartbeat", map[string]interface{}{
				"ts": time.Now().UTC().Format(time.RFC3339),
			})
		case outcome := <-resultCh:
			if outcome.Err != nil {
				writeSSE(w, flusher, "error", map[string]interface{}{
					"answer_status":   "error",
					"answer_warnings": []string{"model_error"},
					"error":           outcome.Err.Error(),
				})
				return
			}
			writeSSE(w, flusher, "answer", map[string]interface{}{
				"text": outcome.Result.Answer,
			})
			for _, citation := range outcome.Result.Citations {
				writeSSE(w, flusher, "citation", citation)
			}
			writeSSE(w, flusher, "done", outcome.Result)
			return
		}
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, data interface{}) {
	payload, err := json.Marshal(data)
	if err != nil {
		payload = []byte(`{"error":"encode event"}`)
	}
	_, _ = fmt.Fprintf(w, "event: %s\n", event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	flusher.Flush()
}

func (s *server) handleLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req LinkAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}
	urls := append([]string{}, req.URLs...)
	if strings.TrimSpace(req.URL) != "" {
		urls = append(urls, req.URL)
	}
	if len(urls) == 0 {
		writeMessage(w, http.StatusBadRequest, "url is required")
		return
	}

	stats, err := linkadd.Run(r.Context(), s.cfg, s.store, urls, linkadd.Options{
		Enrich:    req.Enrich,
		Summarize: true,
		CLI:       defaultWebCLI,
		Length:    "medium",
		Timeout:   2 * time.Minute,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if stats.Errors > 0 && stats.Queued == 0 {
		writeJSON(w, http.StatusBadRequest, stats)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *server) handleSourceActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	filter := parseSourceActivityFilter(r)
	feed, err := s.sourceActivity(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, feed)
}

func (s *server) backlog(ctx context.Context) (store.BacklogStats, error) {
	return s.store.Backlog(ctx, sourceenrich.SummaryPromptVersion, summarizecli.ToolName, s.toolVersion)
}

func (s *server) activity(ctx context.Context, window time.Duration) (store.ActivityStats, error) {
	return s.store.Activity(ctx, time.Now().UTC(), window)
}

func (s *server) sourceActivity(ctx context.Context, filter store.SourceActivityFilter) (store.SourceActivityFeed, error) {
	return s.store.SourceActivityFeedFiltered(ctx, filter)
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

func parseSourceActivityFilter(r *http.Request) store.SourceActivityFilter {
	window := defaultActivityWindow
	if raw := strings.TrimSpace(r.URL.Query().Get("window")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			window = parsed
		}
	}
	return store.SourceActivityFilter{
		Limit:         clampLimit(parseIntDefault(r.URL.Query().Get("limit"), defaultEventLimit), 1, maxEventLimit),
		FailureOffset: maxInt(parseIntDefault(r.URL.Query().Get("failure_offset"), 0), 0),
		FailureSort:   strings.TrimSpace(r.URL.Query().Get("failure_sort")),
		SourceType:    strings.TrimSpace(r.URL.Query().Get("source_type")),
		Domain:        strings.TrimSpace(r.URL.Query().Get("domain")),
		Status:        strings.TrimSpace(r.URL.Query().Get("status")),
		FailureKind:   strings.TrimSpace(r.URL.Query().Get("failure_kind")),
		Message:       strings.TrimSpace(r.URL.Query().Get("message")),
		Window:        window,
	}
}

func maxInt(value int, minValue int) int {
	if value < minValue {
		return minValue
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

func writeMethodNotAllowed(w http.ResponseWriter, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeMessage(w, http.StatusMethodNotAllowed, "method not allowed")
}
