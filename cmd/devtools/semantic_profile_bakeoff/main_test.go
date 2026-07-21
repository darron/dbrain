package main

import (
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
