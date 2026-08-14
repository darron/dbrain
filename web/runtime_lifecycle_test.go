package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/researchtrace"
)

func TestCloseableHandlerWaitsForConcurrentRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var closed atomic.Bool
	handler := newCloseableHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}), func() error {
		closed.Store(true)
		return nil
	})

	requestDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		close(requestDone)
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := handler.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want deadline", err)
	}
	if closed.Load() {
		t.Fatal("runtime closed before active request drained")
	}
	close(release)
	<-requestDone
	if err := handler.Close(); err != nil {
		t.Fatalf("Close after drain: %v", err)
	}
	if !closed.Load() {
		t.Fatal("runtime was not closed after request drain")
	}
}

func TestCloseableHandlerSurfacesIdempotentCloseError(t *testing.T) {
	want := errors.New("web runtime close failed")
	handler := newCloseableHandler(http.NotFoundHandler(), func() error { return want })
	if err := handler.Close(); !errors.Is(err, want) {
		t.Fatalf("Close error = %v, want %v", err, want)
	}
	if err := handler.Close(); !errors.Is(err, want) {
		t.Fatalf("second Close error = %v, want shared %v", err, want)
	}
}

func TestWebResearchSurfacesReuseServerRuntime(t *testing.T) {
	cfg, st := openTestStore(t)
	runtime := brainresearch.NewRuntime(cfg, st)
	if err := runtime.Close(); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
	s := &server{cfg: cfg, store: st, researchRuntime: runtime}

	direct := httptest.NewRecorder()
	directRequest := httptest.NewRequest(http.MethodPost, "/api/research", strings.NewReader(`{"question":"Alpha","disable_planner":true}`))
	s.handleResearch(direct, directRequest)
	if direct.Code != http.StatusInternalServerError || !strings.Contains(direct.Body.String(), "runtime is shut down") {
		t.Fatalf("direct research ignored server runtime: status=%d body=%s", direct.Code, direct.Body.String())
	}

	runner := httptest.NewRecorder()
	runnerRequest := httptest.NewRequest(http.MethodPost, "/api/research/run", strings.NewReader(`{"question":"Alpha","disable_planner":true,"trace_enabled":false}`))
	s.handleResearchRun(runner, runnerRequest)
	if !strings.Contains(runner.Body.String(), "runtime is shut down") {
		t.Fatalf("research runner ignored server runtime: %s", runner.Body.String())
	}

	_, err := runTraceCurrentHarness(
		httptest.NewRequest(http.MethodPost, "/api/research/trace-compare", nil),
		s,
		researchtrace.ResearchTrace{Question: "Alpha"},
	)
	if err == nil || !strings.Contains(err.Error(), "runtime is shut down") {
		t.Fatalf("trace-current harness ignored server runtime: %v", err)
	}
}

func TestWebStoreClosesOnlyAfterHandlerRuntimeDrain(t *testing.T) {
	_, st := openTestStore(t)
	started := make(chan struct{})
	release := make(chan struct{})
	handler := newCloseableHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	}), func() error { return nil })

	requestDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		close(requestDone)
	}()
	<-started

	err := closeWebHandlerStore(handler, st, 10*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleanup error = %v, want handler drain deadline", err)
	}
	if _, err := st.RetrievalDatabaseID(context.Background()); err != nil {
		t.Fatalf("store closed before handler drain: %v", err)
	}

	close(release)
	<-requestDone
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := st.RetrievalDatabaseID(context.Background()); err != nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("store remained open after handler drain")
}
