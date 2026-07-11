package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	if err := downloadVerifiedFile(context.Background(), server.URL, path, hex.EncodeToString(sum[:]), nil); err != nil {
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
	err := downloadVerifiedFile(context.Background(), server.URL, path, "0000000000000000000000000000000000000000000000000000000000000000", nil)
	if err == nil {
		t.Fatal("expected checksum error")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("destination should not exist: %v", statErr)
	}
}

func TestDownloadVerifiedFileReportsByteProgress(t *testing.T) {
	payload := []byte("verified model with visible progress")
	sum := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	var events []DownloadProgress
	path := filepath.Join(t.TempDir(), "model.bin")
	if err := downloadVerifiedFile(context.Background(), server.URL, path, hex.EncodeToString(sum[:]), func(event DownloadProgress) {
		events = append(events, event)
	}); err != nil {
		t.Fatal(err)
	}
	if len(events) < 3 || events[0].Kind != DownloadProgressStart || events[len(events)-1].Kind != DownloadProgressDone {
		t.Fatalf("unexpected progress events: %#v", events)
	}
	last := events[len(events)-1]
	if last.Current != int64(len(payload)) || last.Total != int64(len(payload)) {
		t.Fatalf("unexpected final progress: %#v", last)
	}
}
