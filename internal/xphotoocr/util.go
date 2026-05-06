package xphotoocr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

func hashPhotoInputs(refs []model.ItemMediaRef) string {
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		parts = append(parts, strings.TrimSpace(ref.LocalPath)+"|"+strings.TrimSpace(ref.RemoteURL))
	}
	slices.Sort(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func collapseSet(values map[string]struct{}) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		keys = append(keys, value)
	}
	if len(keys) == 0 {
		return ""
	}
	slices.Sort(keys)
	if len(keys) == 1 {
		return keys[0]
	}
	return strings.Join(keys, ",")
}

func collapseToolVersion(values map[string]struct{}) string {
	versions := make([]string, 0, len(values))
	for value := range values {
		switch value {
		case openRouterVisionTool:
			versions = append(versions, openRouterVisionVersion)
		case ollamaVisionTool:
			versions = append(versions, ollamaVisionVersion)
		case tesseractTool:
			versions = append(versions, tesseractVersion)
		}
	}
	if len(versions) == 0 {
		return ""
	}
	slices.Sort(versions)
	return strings.Join(versions, ",")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isContextCanceled(err error) bool {
	return errors.Is(err, context.Canceled)
}

func debugLog(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Debug(msg, args...)
}
