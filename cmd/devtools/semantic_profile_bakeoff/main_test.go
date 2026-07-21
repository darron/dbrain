package main

import (
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/darron/dbrain/internal/config"
)

func TestBakeoffRefusesLiveProductionXDGDatabase(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	live, err := defaultProductionDBPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(live), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if refused, err := refusesLiveProductionDB(live); err != nil || !refused {
		t.Fatal("live production XDG database must be refused")
	}
	restored := filepath.Join(t.TempDir(), "restored-brain.db")
	if err := os.WriteFile(restored, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if refused, err := refusesLiveProductionDB(restored); err != nil || refused {
		t.Fatal("explicit restored corpus must remain allowed")
	}
}

func TestBakeoffProductionRefusalFailsClosedForResolutionErrorAndAlias(t *testing.T) {
	original := loadProductionConfig
	t.Cleanup(func() { loadProductionConfig = original })
	loadProductionConfig = func(string) (config.Config, error) { return config.Config{}, errors.New("config unavailable") }
	if _, err := refusesLiveProductionDB(filepath.Join(t.TempDir(), "restored.db")); err == nil {
		t.Fatal("production resolution error must fail closed")
	}

	root := t.TempDir()
	live := filepath.Join(root, "brain.db")
	if err := os.WriteFile(live, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	loadProductionConfig = func(string) (config.Config, error) { return config.Config{DBPath: live}, nil }
	alias := filepath.Join(root, "alias.db")
	if err := os.Symlink(live, alias); err != nil {
		t.Fatal(err)
	}
	if refused, err := refusesLiveProductionDB(alias); err != nil || !refused {
		t.Fatalf("symlink alias refused=%v err=%v", refused, err)
	}
}

func TestBakeoffProviderChecksFiniteL2VectorsAtEachRequestedDimension(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"model":"fake","embeddings":[[0.6,0.8]]}`))
	}))
	defer server.Close()
	if err := verifyEmbedding(server.URL, "fake", 2, "projection text"); err != nil {
		t.Fatal(err)
	}
	if err := finiteL2([]float32{float32(math.NaN()), 1}); err == nil {
		t.Fatal("NaN vector must fail")
	}
}
