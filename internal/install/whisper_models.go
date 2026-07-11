package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type whisperModelDownload struct {
	URL, Path, SHA256 string
}

func whisperModelDownloads(selections Selections) []whisperModelDownload {
	return []whisperModelDownload{
		{"https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin", selections.WhisperModelPath, "60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe"},
		{"https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v6.2.0.bin", selections.WhisperVADModelPath, "2aa269b785eeb53a82983a20501ddf7c1d9c48e33ab63a41391ac6c9f7fb6987"},
	}
}

func downloadVerifiedFile(ctx context.Context, url, path, wantSHA string, progress func(DownloadProgress)) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("whisper.cpp model destination is empty")
	}
	if data, err := os.ReadFile(path); err == nil {
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) == wantSHA {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create whisper.cpp model directory: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download whisper.cpp model: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download whisper.cpp model: HTTP %s", resp.Status)
	}
	total := resp.ContentLength
	if total < 0 {
		total = 0
	}
	emitDownloadProgress(progress, DownloadProgress{Kind: DownloadProgressStart, Path: path, Total: total})
	tmp, err := os.CreateTemp(filepath.Dir(path), ".whisper-model-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	hash := sha256.New()
	reader := &downloadProgressReader{reader: resp.Body, total: total, path: path, progress: progress}
	if _, err := io.Copy(io.MultiWriter(tmp, hash), reader); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != wantSHA {
		return fmt.Errorf("whisper.cpp model checksum mismatch: got %s want %s", got, wantSHA)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	emitDownloadProgress(progress, DownloadProgress{Kind: DownloadProgressDone, Path: path, Current: reader.current, Total: total})
	return nil
}

type downloadProgressReader struct {
	reader   io.Reader
	path     string
	current  int64
	total    int64
	progress func(DownloadProgress)
}

func (r *downloadProgressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.current += int64(n)
		emitDownloadProgress(r.progress, DownloadProgress{Kind: DownloadProgressUpdate, Path: r.path, Current: r.current, Total: r.total})
	}
	return n, err
}

func emitDownloadProgress(progress func(DownloadProgress), event DownloadProgress) {
	if progress != nil {
		progress(event)
	}
}
