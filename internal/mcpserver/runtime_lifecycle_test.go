package mcpserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

func TestServerTransportCloneSharesIdempotentRuntimeClose(t *testing.T) {
	cfg, st := newMCPServerStore(t)
	server := New(cfg, st)
	clone := server.withTransportCapabilities(transportCapabilities{audit: true})
	if clone.lifecycle != server.lifecycle {
		t.Fatal("transport clone copied lifecycle state instead of sharing its pointer")
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(target *Server) {
			defer wg.Done()
			errs <- target.Close()
		}([]*Server{server, clone}[i%2])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("idempotent close: %v", err)
		}
	}
	if _, err := server.BuildResearchPack(context.Background(), ResearchPackOptions{Question: "closed runtime"}); err == nil {
		t.Fatal("closed server admitted a new research build")
	}
}

func newMCPServerStore(t *testing.T) (config.Config, *store.Store) {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return cfg, st
}

func TestServerCloseSurfacesSharedRuntimeError(t *testing.T) {
	cfg, st := newMCPServerStore(t)
	server := New(cfg, st)
	want := errors.New("runtime close failed")
	server.lifecycle.shutdownRuntime = func(context.Context) error { return want }

	if err := server.Close(); !errors.Is(err, want) {
		t.Fatalf("Close error = %v, want %v", err, want)
	}
	if err := server.withTransportCapabilities(transportCapabilities{}).Close(); !errors.Is(err, want) {
		t.Fatalf("clone Close error = %v, want shared %v", err, want)
	}
}

func TestServerShutdownWaitsForConcurrentResearchRequest(t *testing.T) {
	cfg, st := newMCPServerStore(t)
	server := New(cfg, st)
	started := make(chan struct{})
	release := make(chan struct{})
	server.lifecycle.buildResearchPack = func(context.Context, brainresearch.Options) (brainresearch.Pack, error) {
		close(started)
		<-release
		return brainresearch.Pack{Question: "concurrent"}, nil
	}

	buildDone := make(chan error, 1)
	go func() {
		_, err := server.BuildResearchPack(context.Background(), ResearchPackOptions{Question: "concurrent"})
		buildDone <- err
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := server.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want deadline while request is active", err)
	}
	close(release)
	if err := <-buildDone; err != nil {
		t.Fatalf("concurrent build: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("Close after request drain: %v", err)
	}
}

func TestMCPServerStoreClosesOnlyAfterRuntimeDrain(t *testing.T) {
	cfg, st := newMCPServerStore(t)
	server := New(cfg, st)
	started := make(chan struct{})
	release := make(chan struct{})
	server.lifecycle.buildResearchPack = func(context.Context, brainresearch.Options) (brainresearch.Pack, error) {
		close(started)
		<-release
		return brainresearch.Pack{Question: "drain"}, nil
	}

	buildDone := make(chan error, 1)
	go func() {
		_, err := server.BuildResearchPack(context.Background(), ResearchPackOptions{Question: "drain"})
		buildDone <- err
	}()
	<-started

	err := closeServerStore(server, st, 10*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleanup error = %v, want runtime drain deadline", err)
	}
	if _, err := st.RetrievalDatabaseID(context.Background()); err != nil {
		t.Fatalf("store closed before runtime drain: %v", err)
	}

	close(release)
	if err := <-buildDone; err != nil {
		t.Fatalf("build after release: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := st.RetrievalDatabaseID(context.Background()); err != nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("store remained open after runtime drain")
}
