package remote

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type HandlerOptions struct {
	Web        bool
	MCP        bool
	MCPPath    string
	TLS        bool
	WebHandler http.Handler
	MCPHandler http.Handler
}

func NewHandler(opts HandlerOptions) (http.Handler, error) {
	mcpPath, err := ValidateMCPPath(opts.MCPPath)
	if err != nil {
		return nil, err
	}
	if opts.MCP && opts.MCPHandler == nil {
		return nil, fmt.Errorf("mcp handler is required when MCP is enabled")
	}
	if opts.Web && opts.WebHandler == nil {
		return nil, fmt.Errorf("web handler is required when web is enabled")
	}
	return securityHeaders(opts.TLS, originGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if opts.MCP && (r.URL.Path == mcpPath || strings.HasPrefix(r.URL.Path, mcpPath+"/")) {
			if r.URL.Path != mcpPath {
				writeMCPHTTPError(w, http.StatusNotFound, "MCP endpoint not found")
				return
			}
			opts.MCPHandler.ServeHTTP(w, r)
			return
		}
		if opts.Web {
			opts.WebHandler.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	}))), nil
}

func securityHeaders(tls bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tls {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" || sameOrigin(origin, requestOrigin(r)) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "forbidden origin", http.StatusForbidden)
	})
}

func sameOrigin(origin string, expected string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(normalizeOrigin(parsed.Scheme+"://"+parsed.Host), normalizeOrigin(expected))
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
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

func writeMCPHTTPError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"jsonrpc": "2.0",
		"error": map[string]interface{}{
			"code":    -32000,
			"message": message,
		},
	})
}
