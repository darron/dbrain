package main

import (
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBakeoffRefusesLiveProductionXDGDatabase(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if !refusesLiveProductionDB(defaultProductionDBPath()) {
		t.Fatal("live production XDG database must be refused")
	}
	if refusesLiveProductionDB(t.TempDir() + "/restored-brain.db") {
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
