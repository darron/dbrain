package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadVerifiedFileWritesOnlyMatchingContent(t *testing.T) {
	payload := []byte("verified model")
	sum := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "models", "model.bin")
	if err := downloadVerifiedFile(context.Background(), server.URL, path, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("got %q", got)
	}
}

func TestDownloadVerifiedFileRejectsChecksumMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("wrong")) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "model.bin")
	err := downloadVerifiedFile(context.Background(), server.URL, path, "0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("expected checksum error")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("destination should not exist: %v", statErr)
	}
}
