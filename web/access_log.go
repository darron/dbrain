package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/store"
)

type webRequestStartedAtKey struct{}
type webAccessLoggedKey struct{}

func (s *server) withAccessLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		state := &webAccessLogState{}
		ctx := context.WithValue(r.Context(), webRequestStartedAtKey{}, started)
		ctx = context.WithValue(ctx, webAccessLoggedKey{}, state)
		logged := newWebAccessLogWriter(w)
		next.ServeHTTP(logged, r.WithContext(ctx))
		if s != nil && !state.logged {
			auth := "disabled"
			if s.auth != nil {
				auth = "bypass"
			}
			logWebAccess(s.logOutput, r.WithContext(ctx), logged.statusCode(), auth, "", s.store.PoolStats())
			state.logged = true
		}
	})
}

type webAccessLogState struct {
	logged bool
}

type webAccessLogWriter struct {
	http.ResponseWriter
	status int
}

func newWebAccessLogWriter(w http.ResponseWriter) *webAccessLogWriter {
	return &webAccessLogWriter{ResponseWriter: w}
}

func (w *webAccessLogWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *webAccessLogWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func (w *webAccessLogWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *webAccessLogWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *webAccessLogWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (a *authManager) logAccess(r *http.Request, status int, user authUser, authenticated bool) {
	if a == nil || a.logOutput == nil {
		return
	}
	identity := ""
	auth := "none"
	if authenticated {
		auth = strings.TrimSpace(user.Provider)
		if auth == "" {
			auth = "authenticated"
		}
		identity = strings.TrimSpace(user.Username)
		if identity == "" {
			identity = strings.TrimSpace(user.ID)
		}
	}
	logWebAccess(a.logOutput, r, status, auth, identity, a.store.PoolStats())
	if state, ok := r.Context().Value(webAccessLoggedKey{}).(*webAccessLogState); ok {
		state.logged = true
	}
}

func logWebAccess(out io.Writer, r *http.Request, status int, auth string, identity string, pool store.PoolStats) {
	if out == nil {
		out = os.Stderr
	}
	if status == 0 {
		status = http.StatusOK
	}
	duration := time.Duration(0)
	if started, ok := r.Context().Value(webRequestStartedAtKey{}).(time.Time); ok {
		duration = time.Since(started)
	}
	_, _ = fmt.Fprintf(out, "DEBUG %s web request method=%s path=%s status=%d duration=%s db_max_open=%d db_open=%d db_in_use=%d db_idle=%d db_wait_count=%d db_wait_duration=%s auth=%q identity=%q remote=%q\n", time.Now().Format("15:04:05.000"), r.Method, r.URL.Path, status, duration, pool.MaxOpenConnections, pool.OpenConnections, pool.InUse, pool.Idle, pool.WaitCount, pool.WaitDuration, auth, identity, r.RemoteAddr)
}
