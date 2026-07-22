package youtubeimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/audit"
)

func TestYouTubeSourceKeyIsSharedWithNormalImporter(t *testing.T) {
	liked := selectedFeeds(Options{Liked: true})[0]
	key, err := youtubeSourceKey(liked, " abc123 ")
	if err != nil {
		t.Fatal(err)
	}
	if key != "yt:liked:abc123" {
		t.Fatalf("source key = %q", key)
	}
	item, skip, err := toItem(videoEntry{ID: " abc123 "}, liked, time.Now())
	if err != nil || skip {
		t.Fatalf("toItem error=%v skip=%t", err, skip)
	}
	if item.SourceKey != key {
		t.Fatalf("normal source key = %q, want %q", item.SourceKey, key)
	}
	if _, err := youtubeSourceKey(liked, " "); err == nil {
		t.Fatal("expected blank video id to fail")
	}
}

func TestYouTubeAuditInventoryUsesFixedFeedExactArgsAndScrubbedEnvironment(t *testing.T) {
	root := t.TempDir()
	argsPath := filepath.Join(root, "args")
	envPath := filepath.Join(root, "env")
	script := writeAuditYTDLPScript(t, root, `
printf '%s\n' "$@" > "$ARGS_PATH"
env > "$ENV_PATH"
printf '%s\n' '{"id":"WL","entries":[{"id":"one"},{"id":"two"}]}'
`)
	t.Setenv("ARGS_PATH", argsPath)
	t.Setenv("ENV_PATH", envPath)
	t.Setenv("HTTP_PROXY", "http://secret-proxy")
	t.Setenv("https_proxy", "http://secret-lower-proxy")
	t.Setenv("ALL_PROXY", "socks5://secret")
	t.Setenv("NO_PROXY", "secret.internal")
	watch := selectedFeeds(Options{WatchLater: true})[0]
	inventory := newYouTubeAuditInventory(watch, "chrome", "Profile 1", script, os.Environ)
	result, err := inventory.Inventory(t.Context(), audit.InventoryBudget{MaxIdentities: 2, MaxPages: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.PageCount != 1 || len(result.IdentityHashes) != 2 {
		t.Fatalf("result = %#v", result)
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	wantArgs := []string{
		"--ignore-config", "--dump-single-json", "--flat-playlist",
		"--cookies-from-browser", "chrome:Profile 1",
		"--playlist-end", "3", "https://www.youtube.com/playlist?list=WL",
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
	envBytes, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	env := strings.ToLower(string(envBytes))
	for _, key := range []string{"http_proxy=", "https_proxy=", "all_proxy=", "no_proxy="} {
		if strings.Contains(env, key) {
			t.Fatalf("subprocess environment retained %s: %s", key, env)
		}
	}
	if !strings.Contains(env, "path=") || !strings.Contains(env, "home=") {
		t.Fatalf("subprocess environment lost PATH/HOME: %s", env)
	}
}

func TestYouTubeAuditInventoriesKeepLikedAndWatchLaterSeparate(t *testing.T) {
	root := t.TempDir()
	script := writeAuditYTDLPScript(t, root, `
case "$*" in
  *"list=LL"*) printf '%s\n' '{"entries":[{"id":"liked-video"}]}' ;;
  *"list=WL"*) printf '%s\n' '{"entries":[{"id":"watch-video"}]}' ;;
  *) exit 9 ;;
esac
`)
	liked := newYouTubeAuditInventory(selectedFeeds(Options{Liked: true})[0], "chrome", "Default", script, os.Environ)
	watch := newYouTubeAuditInventory(selectedFeeds(Options{WatchLater: true})[0], "chrome", "Default", script, os.Environ)
	likedResult, err := liked.Inventory(t.Context(), audit.DefaultInventoryBudget())
	if err != nil {
		t.Fatal(err)
	}
	watchResult, err := watch.Inventory(t.Context(), audit.DefaultInventoryBudget())
	if err != nil {
		t.Fatal(err)
	}
	wantLiked, _ := audit.HashUpstreamIdentity(audit.SourceYouTubeLiked, "yt:liked:liked-video")
	wantWatch, _ := audit.HashUpstreamIdentity(audit.SourceYouTubeWatchLater, "yt:watch_later:watch-video")
	if !reflect.DeepEqual(likedResult.IdentityHashes, []string{wantLiked}) {
		t.Fatalf("liked hashes = %#v", likedResult.IdentityHashes)
	}
	if !reflect.DeepEqual(watchResult.IdentityHashes, []string{wantWatch}) {
		t.Fatalf("watch hashes = %#v", watchResult.IdentityHashes)
	}
}

func TestYouTubeAuditInventoryCompletionCapsAndMalformedOutput(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantHash int
		wantErr  error
	}{
		{name: "exact cap observes end", body: `{"entries":[{"id":"one"},{"id":"two"}]}`, wantHash: 2},
		{name: "duplicate ids dedupe", body: `{"entries":[{"id":"one"},{"id":"one"}]}`, wantHash: 1},
		{name: "cap plus one is incomplete", body: `{"entries":[{"id":"one"},{"id":"two"},{"id":"three"}]}`, wantErr: audit.ErrInventoryBudget},
		{name: "blank id", body: `{"entries":[{"id":""}]}`, wantErr: audit.ErrInventoryInvalid},
		{name: "missing entries", body: `{}`, wantErr: audit.ErrInventoryInvalid},
		{name: "null entries", body: `{"entries":null}`, wantErr: audit.ErrInventoryInvalid},
		{name: "trailing json", body: `{"entries":[]} {}`, wantErr: audit.ErrInventoryInvalid},
		{name: "malformed", body: `{`, wantErr: audit.ErrInventoryInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			script := writeAuditYTDLPScript(t, root, "printf '%s\\n' '"+test.body+"'")
			inventory := newYouTubeAuditInventory(selectedFeeds(Options{Liked: true})[0], "chrome", "Default", script, os.Environ)
			result, err := inventory.Inventory(t.Context(), audit.InventoryBudget{MaxIdentities: 2, MaxPages: 10})
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want %v", err, test.wantErr)
				}
				if result.Complete {
					t.Fatalf("failed result claimed completion: %#v", result)
				}
				return
			}
			if err != nil || !result.Complete || len(result.IdentityHashes) != test.wantHash {
				t.Fatalf("result=%#v error=%v", result, err)
			}
		})
	}
}

func TestYouTubeAuditInventoryRetriesCookieCandidatesWithinCallerContext(t *testing.T) {
	root := t.TempDir()
	countPath := filepath.Join(root, "count")
	script := writeAuditYTDLPScript(t, root, `
count=0
[ -f "$COUNT_PATH" ] && count=$(cat "$COUNT_PATH")
count=$((count + 1))
printf '%s' "$count" > "$COUNT_PATH"
case "$*" in
  *"chrome:Default"*) printf '%s\n' '{"entries":[{"id":"ok"}]}' ;;
  *) printf '%s\n' 'cookie failure' >&2; exit 2 ;;
esac
`)
	t.Setenv("COUNT_PATH", countPath)
	inventory := newYouTubeAuditInventory(selectedFeeds(Options{Liked: true})[0], "chrome", "", script, os.Environ)
	result, err := inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
	if err != nil || !result.Complete {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	count, err := os.ReadFile(countPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(count) != "2" {
		t.Fatalf("attempt count = %q, want 2", count)
	}

	timeoutScript := writeAuditYTDLPScript(t, root, `sleep 5`)
	timed := newYouTubeAuditInventory(selectedFeeds(Options{Liked: true})[0], "chrome", "Default", timeoutScript, os.Environ)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err = timed.Inventory(ctx, audit.DefaultInventoryBudget())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestCappedDrainWriterKeepsConsumingAfterLimit(t *testing.T) {
	writer := newCappedDrainWriter(4)
	if n, err := writer.Write([]byte("abcdefgh")); err != nil || n != 8 {
		t.Fatalf("write n=%d err=%v", n, err)
	}
	if !writer.Overflowed() || writer.String() != "abcd" {
		t.Fatalf("writer overflow=%t value=%q", writer.Overflowed(), writer.String())
	}
}

func TestYouTubeAuditInventoryBoundsBothStreamsAndSanitizesFailures(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{
			name: "successful stdout overflow",
			body: "dd if=/dev/zero bs=16777217 count=1 2>/dev/null",
			want: audit.ErrInventoryBudget,
		},
		{
			name: "successful stderr overflow",
			body: "dd if=/dev/zero bs=65537 count=1 2>/dev/null | cat >&2\nprintf '%s\\n' '{\"entries\":[]}'",
			want: audit.ErrInventoryBudget,
		},
		{
			name: "nonzero exit",
			body: "printf '%s\\n' 'SECRET_OUTPUT cookie=chrome:Sensitive path=/private/secret' >&2\nexit 7",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			script := writeAuditYTDLPScript(t, root, test.body)
			inventory := newYouTubeAuditInventory(selectedFeeds(Options{Liked: true})[0], "chrome", "Sensitive", script, os.Environ)
			_, err := inventory.Inventory(t.Context(), audit.DefaultInventoryBudget())
			if err == nil {
				t.Fatal("expected inventory failure")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			for _, secret := range []string{"SECRET_OUTPUT", "Sensitive", root, "chrome:"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func TestYouTubeAuditInventoryCancellationStopsBeforeSecondCandidate(t *testing.T) {
	root := t.TempDir()
	countPath := filepath.Join(root, "count-canceled")
	script := writeAuditYTDLPScript(t, root, `
count=0
[ -f "$COUNT_PATH" ] && count=$(cat "$COUNT_PATH")
count=$((count + 1))
printf '%s' "$count" > "$COUNT_PATH"
sleep 5
`)
	t.Setenv("COUNT_PATH", countPath)
	inventory := newYouTubeAuditInventory(selectedFeeds(Options{Liked: true})[0], "chrome", "", script, os.Environ)
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := inventory.Inventory(ctx, audit.DefaultInventoryBudget())
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		count, err := os.ReadFile(countPath)
		if err == nil && string(count) == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("yt-dlp attempt did not start: count=%q err=%v", count, err)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	err := <-result
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	count, readErr := os.ReadFile(countPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(count) != "1" {
		t.Fatalf("attempt count after cancellation = %q", count)
	}
}

func TestYouTubeAuditInventoryRejectsInvalidBudgetAndFeedWithoutExecuting(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "executed")
	script := writeAuditYTDLPScript(t, root, `touch "$MARKER"`)
	t.Setenv("MARKER", marker)
	validFeed := selectedFeeds(Options{Liked: true})[0]
	for _, test := range []struct {
		name      string
		inventory *AuditInventory
		budget    audit.InventoryBudget
	}{
		{name: "zero identity budget", inventory: newYouTubeAuditInventory(validFeed, "chrome", "Default", script, os.Environ), budget: audit.InventoryBudget{MaxPages: 1}},
		{name: "page budget too high", inventory: newYouTubeAuditInventory(validFeed, "chrome", "Default", script, os.Environ), budget: audit.InventoryBudget{MaxIdentities: 1, MaxPages: audit.InventoryMaxPages + 1}},
		{name: "unknown feed", inventory: newYouTubeAuditInventory(feed{name: "history", url: "https://www.youtube.com/playlist?list=SECRET"}, "chrome", "Default", script, os.Environ), budget: audit.DefaultInventoryBudget()},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.inventory.Inventory(t.Context(), test.budget)
			if !errors.Is(err, audit.ErrInventoryInvalid) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid inventory executed subprocess: %v", err)
	}
}

func writeAuditYTDLPScript(t *testing.T, root, body string) string {
	t.Helper()
	path := filepath.Join(root, "fake-yt-dlp")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
