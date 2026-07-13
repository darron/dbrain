package mediaarchive

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
)

func TestS3UploadRejectsVaultEscape(t *testing.T) {
	rootDir := t.TempDir()
	cfg, err := config.Load(rootDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	outside := filepath.Join(filepath.Dir(cfg.VaultDir), "upload-sentinel.txt")
	if err := os.WriteFile(outside, []byte("outside upload sentinel"), 0o600); err != nil {
		t.Fatalf("write outside sentinel: %v", err)
	}
	localPath, err := filepath.Rel(cfg.VaultDir, outside)
	if err != nil {
		t.Fatalf("relative outside path: %v", err)
	}

	var requests atomic.Int64
	var uploadedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		uploadedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("ETag", `"test-etag"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	uploader, err := NewS3Uploader(Options{
		Endpoint:    server.URL,
		Region:      "auto",
		AccessKeyID: "test-key",
		SecretKey:   "test-secret",
		PathStyle:   true,
	})
	if err != nil {
		t.Fatalf("NewS3Uploader: %v", err)
	}
	_, uploaded, err := uploader.Upload(t.Context(), cfg, model.MediaAsset{
		LocalPath: filepath.ToSlash(localPath),
		MIMEType:  "text/plain",
	}, Options{Provider: "s3", Bucket: "test-bucket"})
	if err == nil {
		t.Error("expected escaping upload path to be rejected")
	}
	if uploaded {
		t.Error("escaping path reported uploaded")
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("archive endpoint received %d requests", got)
	}
	if string(uploadedBody) == "outside upload sentinel" {
		t.Error("outside sentinel was uploaded")
	}
}

func TestS3UploadReadsContainedVaultFile(t *testing.T) {
	rootDir := t.TempDir()
	cfg, err := config.Load(rootDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	const localPath = "media/contained.txt"
	const wantBody = "contained upload control"
	if err := os.MkdirAll(filepath.Join(cfg.VaultDir, "media"), 0o700); err != nil {
		t.Fatalf("create media directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfg.VaultDir, filepath.FromSlash(localPath)), []byte(wantBody), 0o600); err != nil {
		t.Fatalf("write contained media: %v", err)
	}

	var requests atomic.Int64
	var uploadedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		uploadedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("ETag", `"contained-etag"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	uploader, err := NewS3Uploader(Options{
		Endpoint:    server.URL,
		Region:      "auto",
		AccessKeyID: "test-key",
		SecretKey:   "test-secret",
		PathStyle:   true,
	})
	if err != nil {
		t.Fatalf("NewS3Uploader: %v", err)
	}
	result, uploaded, err := uploader.Upload(t.Context(), cfg, model.MediaAsset{
		LocalPath: localPath,
		MIMEType:  "text/plain",
	}, Options{Provider: "s3", Bucket: "test-bucket"})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !uploaded {
		t.Fatal("contained file was not uploaded")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("archive endpoint received %d requests, want 1", got)
	}
	if got := string(uploadedBody); got != wantBody {
		t.Fatalf("uploaded body = %q, want %q", got, wantBody)
	}
	if result.ETag != `"contained-etag"` {
		t.Fatalf("ETag = %q, want quoted contained-etag", result.ETag)
	}
}
