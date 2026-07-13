package remote

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/darron/dbrain/internal/httpsecurity"
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
	return securityHeaders(opts.TLS, httpsecurity.OriginGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
