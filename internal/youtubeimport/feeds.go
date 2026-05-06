package youtubeimport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

func selectedFeeds(opts Options) []feed {
	feeds := make([]feed, 0, 2)
	if opts.WatchLater {
		feeds = append(feeds, feed{
			name:       "watch_later",
			sourceType: "youtube_watch_later",
			url:        "https://www.youtube.com/playlist?list=WL",
		})
	}
	if opts.Liked {
		feeds = append(feeds, feed{
			name:       "liked",
			sourceType: "youtube_liked",
			url:        "https://www.youtube.com/playlist?list=LL",
		})
	}
	return feeds
}

func fetchFeed(ctx context.Context, currentFeed feed, opts Options) (playlistEnvelope, string, error) {
	cookieArgs := cookiesFromBrowserArgs(opts.Browser, opts.Profile)
	var lastErr error

	for _, cookiesArg := range cookieArgs {
		commandCtx, cancel := context.WithTimeout(ctx, opts.Timeout)

		args := []string{"--dump-single-json", "--flat-playlist", "--cookies-from-browser", cookiesArg}
		if opts.Limit > 0 {
			args = append(args, "--playlist-end", fmt.Sprintf("%d", opts.Limit))
		}
		args = append(args, currentFeed.url)

		cmd := exec.CommandContext(commandCtx, opts.YTDLPBinary, args...)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		cancel()
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			lastErr = fmt.Errorf("run yt-dlp for %s with %s: %s", currentFeed.name, cookiesArg, msg)
			debugLog(opts.Logger, "youtube feed load attempt failed", "feed", currentFeed.name, "url", currentFeed.url, "cookies", cookiesArg, "error", msg)
			continue
		}

		var envelope playlistEnvelope
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			lastErr = fmt.Errorf("parse yt-dlp json for %s with %s: %w", currentFeed.name, cookiesArg, err)
			debugLog(opts.Logger, "youtube feed parse failed", "feed", currentFeed.name, "url", currentFeed.url, "cookies", cookiesArg, "error", err.Error())
			continue
		}

		debugLog(opts.Logger, "youtube feed loaded", "feed", currentFeed.name, "url", currentFeed.url, "cookies", cookiesArg, "entries", len(envelope.Entries))
		return envelope, cookiesArg, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("run yt-dlp for %s: no cookie candidates available", currentFeed.name)
	}
	return playlistEnvelope{}, "", lastErr
}
