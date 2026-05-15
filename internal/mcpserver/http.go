package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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
	Addr                     string
	Path                     string
	AllowedOrigins           []string
	MaxBodyBytes             int64
	RequireBearerAuth        bool
	BearerTokenValidator     BearerTokenValidator
	BearerTokenAuthenticator BearerTokenAuthenticator
	LogOutput                io.Writer
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

	if err := ApplyRuntimeAuthOptions(cfg, st, &opts); err != nil {
		logMCPServer("http_auth_config_failed", "duration", time.Since(start).String(), "error", err.Error())
		return err
	}
	if opts.RequireBearerAuth {
		logMCPServer("http_bearer_auth_enabled")
	} else {
		WriteOpenAuthWarning(os.Stderr, "MCP HTTP")
	}
	if opts.LogOutput == nil {
		opts.LogOutput = os.Stderr
	}
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
		logged := newMCPAccessLogWriter(w)
		access := mcpAccessLogIdentity{Auth: "disabled"}
		if opts.RequireBearerAuth {
			access.Auth = "bearer"
			if r.Method == http.MethodOptions {
				access.TokenStatus = "preflight"
			}
		}
		defer func() {
			logMCPAccess(opts.LogOutput, r, logged.statusCode(), access)
		}()

		if !originAllowed(r, opts.AllowedOrigins) {
			http.Error(logged, "forbidden origin", http.StatusForbidden)
			return
		}
		if opts.RequireBearerAuth && r.Method != http.MethodOptions {
			token, ok := bearerTokenFromRequest(r)
			if !ok {
				access.TokenStatus = "missing"
				writeBearerUnauthorized(logged)
				return
			}
			if opts.BearerTokenValidator == nil && opts.BearerTokenAuthenticator == nil {
				logMCPServer("http_bearer_auth_misconfigured")
				access.TokenStatus = "misconfigured"
				http.Error(logged, "mcp bearer auth is misconfigured", http.StatusInternalServerError)
				return
			}
			identity, allowed, err := authenticateBearerToken(r.Context(), opts, token)
			if err != nil {
				logMCPServer("http_bearer_auth_failed", "error", err.Error())
				access.TokenStatus = "error"
				http.Error(logged, "mcp bearer auth failed", http.StatusInternalServerError)
				return
			}
			if !allowed {
				access.TokenStatus = "invalid"
				writeBearerUnauthorized(logged)
				return
			}
			access.TokenStatus = "accepted"
			access.TokenName = identity.Name
			access.TokenFingerprint = identity.Fingerprint
		}

		switch r.Method {
		case http.MethodPost:
			s.handleHTTPPost(logged, r, maxBodyBytes)
		case http.MethodGet:
			logged.Header().Set("Allow", "POST, GET")
			logged.Header().Set("Content-Type", "text/plain; charset=utf-8")
			logged.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = fmt.Fprintln(logged, "dbrain MCP endpoint is reachable.")
			_, _ = fmt.Fprintln(logged)
			_, _ = fmt.Fprintln(logged, "This endpoint does not serve a browser UI and does not support SSE streams.")
			_, _ = fmt.Fprintln(logged, "Use JSON-RPC over HTTP POST with Content-Type: application/json.")
			_, _ = fmt.Fprintf(logged, "\nExample:\n")
			_, _ = fmt.Fprintf(logged, `curl -s %s -H 'content-type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'`+"\n", requestEndpointURL(r))
		case http.MethodOptions:
			logged.Header().Set("Allow", "POST, GET, OPTIONS")
			logged.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
			logged.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
			logged.WriteHeader(http.StatusNoContent)
		default:
			logged.Header().Set("Allow", "POST, GET, OPTIONS")
			http.Error(logged, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	return mux
}

func authenticateBearerToken(ctx context.Context, opts HTTPOptions, token string) (BearerTokenIdentity, bool, error) {
	if opts.BearerTokenAuthenticator != nil {
		return opts.BearerTokenAuthenticator(ctx, token)
	}
	allowed, err := opts.BearerTokenValidator(ctx, token)
	return BearerTokenIdentity{}, allowed, err
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
