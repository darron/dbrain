package githubimport

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"
)

func normalizeTimestamp(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format(time.RFC3339)
}

func chooseYear(values ...string) string {
	for _, value := range values {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
		if err == nil {
			return fmt.Sprintf("%04d", parsed.UTC().Year())
		}
	}
	return "unknown"
}

func itemNoteID(fullName string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(fullName)), "/", "__")
}

func repoNoteSlug(fullName string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(fullName)), "/", "-")
}

func shortHash(value string) string {
	return hashText(value)[:12]
}

func hashText(value string) string {
	h := sha256.New()
	_, _ = io.WriteString(h, strings.TrimSpace(value))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func boolLabel(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func mapKeys(values map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}

func uniqueSorted(values []int64) []int64 {
	seen := map[int64]struct{}{}
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func debugLog(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Debug(msg, args...)
}
