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
	StoreOpenOptions         store.OpenOptions
	MaxBodyBytes             int64
	RequireBearerAuth        bool
	BearerTokenValidator     BearerTokenValidator
	BearerTokenAuthenticator BearerTokenAuthenticator
	LogOutput                io.Writer
}

func ServeHTTP(ctx context.Context, cfg config.Config, opts HTTPOptions, dependencies ...ServerDependencies) error {
	start := time.Now()
	logMCPServer("starting_http", "db_path", cfg.DBPath, "addr", defaultString(opts.Addr, DefaultHTTPAddr), "path", defaultString(opts.Path, DefaultHTTPPath), "pid", fmt.Sprintf("%d", os.Getpid()))
	st, err := store.OpenReadOnlyWithOptions(cfg.DBPath, opts.StoreOpenOptions)
	if err != nil {
		logMCPServer("store_open_failed", "duration", time.Since(start).String(), "error", err.Error())
		return err
	}
	logMCPServer("store_opened", "duration", time.Since(start).String())

	if err := ApplyRuntimeAuthOptions(cfg, st, &opts); err != nil {
		logMCPServer("http_auth_config_failed", "duration", time.Since(start).String(), "error", err.Error())
		return errors.Join(err, st.Close())
	}
	if opts.RequireBearerAuth {
		logMCPServer("http_bearer_auth_enabled")
	} else {
		WriteOpenAuthWarning(os.Stderr, "MCP HTTP")
	}
	if opts.LogOutput == nil {
		opts.LogOutput = os.Stderr
	}
	server := NewWithDependencies(cfg, st, firstServerDependencies(dependencies))
	httpServer := &http.Server{
		Addr:              defaultString(opts.Addr, DefaultHTTPAddr),
		Handler:           server.HTTPHandler(opts),
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		logMCPServer("http_listen_failed", "duration", time.Since(start).String(), "error", err.Error())
		return errors.Join(err, closeServerStore(server, st, 5*time.Second))
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
			if shutdownCtx.Err() != nil && errors.Is(err, shutdownCtx.Err()) {
				go func() {
					if err := httpServer.Shutdown(context.Background()); err != nil && !errors.Is(err, http.ErrServerClosed) {
						logMCPServer("async_cleanup_failed", "component", "http_server", "error", err.Error())
					}
					if err := server.Close(); err != nil {
						logMCPServer("async_cleanup_failed", "component", "runtime", "error", err.Error())
					}
					if err := st.Close(); err != nil {
						logMCPServer("async_cleanup_failed", "component", "store", "error", err.Error())
					}
				}()
				return err
			}
			return errors.Join(err, closeServerStore(server, st, 5*time.Second))
		}
		if err := closeServerStore(server, st, 5*time.Second); err != nil {
			return err
		}
		logMCPServer("exiting_http", "duration", time.Since(start).String(), "error", "")
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		cleanupErr := closeServerStore(server, st, 5*time.Second)
		joined := errors.Join(err, cleanupErr)
		if joined == nil {
			logMCPServer("exiting_http", "duration", time.Since(start).String(), "error", "")
			return nil
		}
		logMCPServer("exiting_http", "duration", time.Since(start).String(), "error", joined.Error())
		return joined
	}
}

func (s *Server) HTTPHandler(opts HTTPOptions) http.Handler {
	transportServer := s.withTransportCapabilities(transportCapabilities{audit: opts.RequireBearerAuth})
	path := normalizeHTTPPath(opts.Path)
	maxBodyBytes := opts.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = 8 << 20
	}

	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		r = r.WithContext(context.WithValue(r.Context(), mcpRequestStartedAtKey{}, started))
		logged := newMCPAccessLogWriter(w)
		access := mcpAccessLogIdentity{Auth: "disabled"}
		if opts.RequireBearerAuth {
			access.Auth = "bearer"
			if r.Method == http.MethodOptions {
				access.TokenStatus = "preflight"
			}
		}
		defer func() {
			logMCPAccess(opts.LogOutput, r, logged.statusCode(), access, transportServer.st.PoolStats())
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
			transportServer.handleHTTPPost(logged, r, maxBodyBytes)
		case http.MethodGet:
			logged.Header().Set("Allow", "POST, OPTIONS")
			logged.Header().Set("Content-Type", "text/plain; charset=utf-8")
			logged.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = fmt.Fprintln(logged, "dbrain MCP endpoint is reachable.")
			_, _ = fmt.Fprintln(logged)
			_, _ = fmt.Fprintln(logged, "This endpoint does not serve a browser UI and does not support SSE streams.")
			_, _ = fmt.Fprintln(logged, "Use JSON-RPC over HTTP POST with Content-Type: application/json.")
			_, _ = fmt.Fprintf(logged, "\nExample:\n")
			_, _ = fmt.Fprintf(logged, `curl -s %s -H 'content-type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'`+"\n", requestEndpointURL(r))
		case http.MethodDelete:
			logged.Header().Set("Allow", "POST, OPTIONS")
			logged.WriteHeader(http.StatusMethodNotAllowed)
		case http.MethodOptions:
			logged.Header().Set("Allow", "POST, OPTIONS")
			logged.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			logged.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, MCP-Protocol-Version, Mcp-Method, Mcp-Name")
			logged.WriteHeader(http.StatusNoContent)
		default:
			logged.Header().Set("Allow", "POST, OPTIONS")
			http.Error(logged, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	return transportServer.withRequestAdmission(mux)
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

	modern := modernPayloadMarked(body) || modernHeadersMarked(r.Header)
	if modern {
		if contentType != "application/json" {
			http.Error(w, "Content-Type must be application/json for modern MCP requests", http.StatusUnsupportedMediaType)
			return
		}
		result, ok, status := s.processModernHTTPPayload(r.Context(), body, r.Header)
		if !ok {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if status != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
		}
		if status == http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
		}
		if err := json.NewEncoder(w).Encode(result); err != nil {
			logMCPServer("http_write_failed", "error", err.Error())
		}
		return
	}

	result, ok := s.processLegacyPayload(r.Context(), body)
	if !ok {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		logMCPServer("http_write_failed", "error", err.Error())
	}
}
