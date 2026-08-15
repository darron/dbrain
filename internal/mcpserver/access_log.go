package mcpserver

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/darron/dbrain/internal/store"
)

type mcpRequestStartedAtKey struct{}

type mcpAccessLogWriter struct {
	http.ResponseWriter
	status int
}

type mcpAccessLogIdentity struct {
	Auth             string
	TokenStatus      string
	TokenName        string
	TokenFingerprint string
}

func newMCPAccessLogWriter(w http.ResponseWriter) *mcpAccessLogWriter {
	return &mcpAccessLogWriter{ResponseWriter: w}
}

func (w *mcpAccessLogWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *mcpAccessLogWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func (w *mcpAccessLogWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *mcpAccessLogWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *mcpAccessLogWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func logMCPAccess(out io.Writer, r *http.Request, status int, identity mcpAccessLogIdentity, pool store.PoolStats) {
	if out == nil {
		return
	}
	if status == 0 {
		status = http.StatusOK
	}
	if identity.Auth == "" {
		identity.Auth = "unknown"
	}
	duration := time.Duration(0)
	if started, ok := r.Context().Value(mcpRequestStartedAtKey{}).(time.Time); ok {
		duration = time.Since(started)
	}
	_, _ = fmt.Fprintf(out, "DEBUG %s mcp request method=%s path=%s status=%d duration=%s db_max_open=%d db_open=%d db_in_use=%d db_idle=%d db_wait_count=%d db_wait_duration=%s auth=%q token_status=%q token_name=%q token_fingerprint=%q remote=%q\n", time.Now().Format("15:04:05.000"), r.Method, r.URL.Path, status, duration, pool.MaxOpenConnections, pool.OpenConnections, pool.InUse, pool.Idle, pool.WaitCount, pool.WaitDuration, identity.Auth, identity.TokenStatus, identity.TokenName, identity.TokenFingerprint, r.RemoteAddr)
}
