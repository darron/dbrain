package web

import (
	"net/http"
	"path/filepath"
	"strings"
)

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
