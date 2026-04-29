package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

const (
	DefaultHTTPAddr = "127.0.0.1:8743"
	DefaultHTTPPath = "/mcp"
)

type HTTPOptions struct {
	Addr           string
	Path           string
	AllowedOrigins []string
	MaxBodyBytes   int64
}

func ServeHTTP(ctx context.Context, cfg config.Config, opts HTTPOptions) error {
	start := time.Now()
	logMCPServer("starting_http", "db_path", cfg.DBPath, "addr", defaultString(opts.Addr, DefaultHTTPAddr), "path", defaultString(opts.Path, DefaultHTTPPath), "pid", fmt.Sprintf("%d", os.Getpid()))
	st, err := store.OpenReadOnly(cfg.DBPath)
	if err != nil {
		logMCPServer("store_open_failed", "duration", time.Since(start).String(), "error", err.Error())
		return err
	}
	logMCPServer("store_opened", "duration", time.Since(start).String())
	defer func() {
		_ = st.Close()
	}()

	server := New(cfg, st)
	httpServer := &http.Server{
		Addr:              defaultString(opts.Addr, DefaultHTTPAddr),
		Handler:           server.HTTPHandler(opts),
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		logMCPServer("http_listen_failed", "duration", time.Since(start).String(), "error", err.Error())
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()
	logMCPServer("ready_http", "addr", httpServer.Addr, "path", normalizeHTTPPath(opts.Path))

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logMCPServer("http_shutdown_failed", "duration", time.Since(start).String(), "error", err.Error())
			return err
		}
		logMCPServer("exiting_http", "duration", time.Since(start).String(), "error", "")
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			logMCPServer("exiting_http", "duration", time.Since(start).String(), "error", "")
			return nil
		}
		logMCPServer("exiting_http", "duration", time.Since(start).String(), "error", err.Error())
		return err
	}
}

func (s *Server) HTTPHandler(opts HTTPOptions) http.Handler {
	path := normalizeHTTPPath(opts.Path)
	maxBodyBytes := opts.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = 8 << 20
	}

	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if !originAllowed(r, opts.AllowedOrigins) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}

		switch r.Method {
		case http.MethodPost:
			s.handleHTTPPost(w, r, maxBodyBytes)
		case http.MethodGet:
			w.Header().Set("Allow", "POST, GET")
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = fmt.Fprintln(w, "dbrain MCP endpoint is reachable.")
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, "This endpoint does not serve a browser UI and does not support SSE streams.")
			_, _ = fmt.Fprintln(w, "Use JSON-RPC over HTTP POST with Content-Type: application/json.")
			_, _ = fmt.Fprintf(w, "\nExample:\n")
			_, _ = fmt.Fprintf(w, `curl -s %s -H 'content-type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'`+"\n", requestEndpointURL(r))
		case http.MethodOptions:
			w.Header().Set("Allow", "POST, GET, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "POST, GET, OPTIONS")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	return mux
}

func (s *Server) handleHTTPPost(w http.ResponseWriter, r *http.Request, maxBodyBytes int64) {
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if contentType != "" && contentType != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "request body too large or unreadable", http.StatusBadRequest)
		return
	}

	result, ok := s.processPayload(r.Context(), body)
	if !ok {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		logMCPServer("http_write_failed", "error", err.Error())
	}
}

func requestEndpointURL(r *http.Request) string {
	if r == nil {
		return DefaultHTTPPath
	}
	if strings.TrimSpace(r.Host) == "" {
		return r.URL.Path
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + r.URL.Path
}

func normalizeHTTPPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return DefaultHTTPPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func originAllowed(r *http.Request, allowed []string) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if originMatchesHost(origin, r.Host) {
		return true
	}
	normalizedOrigin := normalizeOrigin(origin)
	for _, entry := range allowed {
		entry = strings.TrimSpace(entry)
		if entry == "*" {
			return true
		}
		if normalizeOrigin(entry) == normalizedOrigin {
			return true
		}
	}
	return false
}

func originMatchesHost(origin string, host string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	originHost := strings.ToLower(parsed.Host)
	requestHost := strings.ToLower(host)
	if originHost == requestHost {
		return true
	}
	originName, originPort, errOrigin := net.SplitHostPort(originHost)
	requestName, requestPort, errRequest := net.SplitHostPort(requestHost)
	if errOrigin == nil && errRequest == nil && originPort == requestPort {
		return strings.EqualFold(originName, requestName)
	}
	return false
}

func normalizeOrigin(origin string) string {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return origin
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
