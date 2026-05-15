package web

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

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
	logWebAccess(a.logOutput, r, status, auth, identity)
}

func logWebAccess(out io.Writer, r *http.Request, status int, auth string, identity string) {
	if out == nil {
		out = os.Stderr
	}
	if status == 0 {
		status = http.StatusOK
	}
	_, _ = fmt.Fprintf(out, "DEBUG %s web request method=%s path=%s status=%d auth=%q identity=%q remote=%q\n", time.Now().Format("15:04:05.000"), r.Method, r.URL.Path, status, auth, identity, r.RemoteAddr)
}
