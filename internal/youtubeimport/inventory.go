package youtubeimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/audit"
)

const (
	youtubeAuditMaxStdoutBytes = 16 << 20
	youtubeAuditMaxStderrBytes = 64 << 10
)

type AuditInventory struct {
	feed    feed
	browser string
	profile string
	binary  string
	environ func() []string
}

func NewLikedAuditInventory(browser, profile string) *AuditInventory {
	return newYouTubeAuditInventory(selectedFeeds(Options{Liked: true})[0], browser, profile, "yt-dlp", os.Environ)
}

func NewWatchLaterAuditInventory(browser, profile string) *AuditInventory {
	return newYouTubeAuditInventory(selectedFeeds(Options{WatchLater: true})[0], browser, profile, "yt-dlp", os.Environ)
}

func newYouTubeAuditInventory(currentFeed feed, browser, profile, binary string, environ func() []string) *AuditInventory {
	return &AuditInventory{
		feed: currentFeed, browser: browser, profile: profile,
		binary: strings.TrimSpace(binary), environ: environ,
	}
}

func (i *AuditInventory) Inventory(ctx context.Context, budget audit.InventoryBudget) (audit.InventoryResult, error) {
	if err := validateYouTubeAuditBudget(budget); err != nil {
		return audit.InventoryResult{}, err
	}
	if i == nil || strings.TrimSpace(i.binary) == "" || i.environ == nil {
		return audit.InventoryResult{}, fmt.Errorf("%w: youtube audit inventory unavailable", audit.ErrInventoryInvalid)
	}
	source, err := auditSourceForFeed(i.feed)
	if err != nil {
		return audit.InventoryResult{}, err
	}

	envelope, err := i.load(ctx, budget.MaxIdentities+1)
	if err != nil {
		return audit.InventoryResult{}, err
	}
	result := audit.InventoryResult{PageCount: 1}
	if len(envelope.Entries) > budget.MaxIdentities+1 {
		return result, fmt.Errorf("%w: youtube playlist exceeded requested cap", audit.ErrInventoryInvalid)
	}
	truncated := len(envelope.Entries) == budget.MaxIdentities+1
	seen := make(map[string]struct{}, min(len(envelope.Entries), budget.MaxIdentities))
	for _, entry := range envelope.Entries {
		identity, identityErr := youtubeSourceKey(i.feed, entry.ID)
		if identityErr != nil {
			return result, fmt.Errorf("%w: youtube playlist identity", audit.ErrInventoryInvalid)
		}
		hash, hashErr := audit.HashUpstreamIdentity(source, identity)
		if hashErr != nil {
			return result, fmt.Errorf("%w: youtube playlist identity", audit.ErrInventoryInvalid)
		}
		if _, exists := seen[hash]; exists {
			continue
		}
		if len(seen) == budget.MaxIdentities {
			return result, fmt.Errorf("%w: youtube identity cap", audit.ErrInventoryBudget)
		}
		seen[hash] = struct{}{}
		result.IdentityHashes = append(result.IdentityHashes, hash)
	}
	if truncated {
		return result, fmt.Errorf("%w: youtube playlist end not observed", audit.ErrInventoryBudget)
	}
	result.Complete = true
	return result, nil
}

func (i *AuditInventory) load(ctx context.Context, playlistEnd int) (playlistEnvelope, error) {
	var lastErr error
	for _, cookiesArg := range cookiesFromBrowserArgs(i.browser, i.profile) {
		if err := ctx.Err(); err != nil {
			return playlistEnvelope{}, err
		}
		args := []string{
			"--ignore-config",
			"--dump-single-json",
			"--flat-playlist",
			"--cookies-from-browser", cookiesArg,
			"--playlist-end", strconv.Itoa(playlistEnd),
			i.feed.url,
		}
		cmd := exec.CommandContext(ctx, i.binary, args...)
		cmd.Env = scrubProxyEnvironment(i.environ())
		cmd.WaitDelay = 2 * time.Second
		stdout := newCappedDrainWriter(youtubeAuditMaxStdoutBytes)
		stderr := newCappedDrainWriter(youtubeAuditMaxStderrBytes)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return playlistEnvelope{}, ctxErr
			}
			lastErr = errors.New("yt-dlp audit inventory attempt failed")
			continue
		}
		if stdout.Overflowed() || stderr.Overflowed() {
			return playlistEnvelope{}, fmt.Errorf("%w: yt-dlp output cap", audit.ErrInventoryBudget)
		}
		envelope, err := decodeYouTubeAuditEnvelope(stdout.Bytes())
		if err != nil {
			lastErr = err
			continue
		}
		return envelope, nil
	}
	if err := ctx.Err(); err != nil {
		return playlistEnvelope{}, err
	}
	if lastErr == nil {
		lastErr = errors.New("yt-dlp audit inventory has no browser candidates")
	}
	return playlistEnvelope{}, lastErr
}

func decodeYouTubeAuditEnvelope(data []byte) (playlistEnvelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var raw struct {
		ID      string          `json:"id"`
		Title   string          `json:"title"`
		Entries json.RawMessage `json:"entries"`
	}
	if err := decoder.Decode(&raw); err != nil {
		return playlistEnvelope{}, fmt.Errorf("%w: decode youtube playlist", audit.ErrInventoryInvalid)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return playlistEnvelope{}, fmt.Errorf("%w: trailing youtube playlist data", audit.ErrInventoryInvalid)
	}
	if len(raw.Entries) == 0 || bytes.Equal(bytes.TrimSpace(raw.Entries), []byte("null")) {
		return playlistEnvelope{}, fmt.Errorf("%w: youtube playlist entries missing", audit.ErrInventoryInvalid)
	}
	var entries []videoEntry
	if err := json.Unmarshal(raw.Entries, &entries); err != nil || entries == nil {
		return playlistEnvelope{}, fmt.Errorf("%w: decode youtube playlist entries", audit.ErrInventoryInvalid)
	}
	envelope := playlistEnvelope{ID: raw.ID, Title: raw.Title, Entries: entries}
	return envelope, nil
}

func auditSourceForFeed(currentFeed feed) (audit.Source, error) {
	switch currentFeed.name {
	case "liked":
		return audit.SourceYouTubeLiked, nil
	case "watch_later":
		return audit.SourceYouTubeWatchLater, nil
	default:
		return "", fmt.Errorf("%w: unsupported youtube feed", audit.ErrInventoryInvalid)
	}
}

func validateYouTubeAuditBudget(budget audit.InventoryBudget) error {
	if budget.MaxIdentities <= 0 || budget.MaxIdentities > audit.InventoryMaxIdentities || budget.MaxPages <= 0 || budget.MaxPages > audit.InventoryMaxPages {
		return fmt.Errorf("%w: youtube audit budget", audit.ErrInventoryInvalid)
	}
	return nil
}

var proxyEnvironmentNames = map[string]struct{}{
	"http_proxy": {}, "https_proxy": {}, "all_proxy": {}, "no_proxy": {}, "ftp_proxy": {},
}

func scrubProxyEnvironment(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		name, _, found := strings.Cut(value, "=")
		if found {
			if _, scrub := proxyEnvironmentNames[strings.ToLower(strings.TrimSpace(name))]; scrub {
				continue
			}
		}
		out = append(out, value)
	}
	return out
}

type cappedDrainWriter struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func newCappedDrainWriter(limit int) *cappedDrainWriter {
	return &cappedDrainWriter{limit: max(0, limit)}
}

func (w *cappedDrainWriter) Write(p []byte) (int, error) {
	original := len(p)
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		keep := min(remaining, len(p))
		_, _ = w.buffer.Write(p[:keep])
	}
	if original > remaining {
		w.overflow = true
	}
	return original, nil
}

func (w *cappedDrainWriter) Bytes() []byte    { return w.buffer.Bytes() }
func (w *cappedDrainWriter) String() string   { return w.buffer.String() }
func (w *cappedDrainWriter) Overflowed() bool { return w.overflow }
