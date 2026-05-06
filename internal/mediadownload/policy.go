package mediadownload

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
)

func shouldDownload(ref model.ItemMediaRef, cfg config.Config, force bool) bool {
	if force {
		return true
	}

	status := strings.TrimSpace(ref.DownloadStatus)
	switch status {
	case "", model.MediaDownloadStatusPending:
		return true
	case model.MediaDownloadStatusError:
		return mediaDownloadRetryDue(ref, time.Now().UTC())
	case model.MediaDownloadStatusDownloaded:
		if strings.TrimSpace(ref.LocalPath) == "" {
			return true
		}
		if !ref.LocalPrunedAt.IsZero() && strings.TrimSpace(ref.ArchiveStatus) == model.MediaArchiveStatusArchived {
			return false
		}
		fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(ref.LocalPath))
		_, err := os.Stat(fullPath)
		return err != nil
	default:
		return false
	}
}

func mediaDownloadRetryDue(ref model.ItemMediaRef, now time.Time) bool {
	if ref.DownloadErrors >= model.MediaDownloadMaxConsecutiveErrors {
		return false
	}
	if ref.LastDownloadAt.IsZero() {
		return true
	}
	cutoff := now.Add(-model.MediaDownloadRetryCooldown)
	return !ref.LastDownloadAt.After(cutoff)
}

func applyRetryPolicy(ref model.ItemMediaRef, result model.MediaDownloadResult) model.MediaDownloadResult {
	if strings.TrimSpace(result.Status) != model.MediaDownloadStatusError {
		return result
	}
	if ref.DownloadErrors+1 < model.MediaDownloadMaxConsecutiveErrors {
		return result
	}
	count := ref.DownloadErrors + 1
	if count > model.MediaDownloadMaxConsecutiveErrors {
		count = model.MediaDownloadMaxConsecutiveErrors
	}
	result.Status = model.MediaDownloadStatusBlocked
	result.Error = terminalDownloadError(count, result.Error)
	return result
}

func terminalDownloadError(count int, errText string) string {
	errText = strings.TrimSpace(errText)
	prefix := "blocked after " + strconv.Itoa(count) + " failed media download attempts"
	if errText == "" {
		return prefix
	}
	if strings.HasPrefix(errText, "blocked after ") {
		return errText
	}
	return prefix + ": " + errText
}
