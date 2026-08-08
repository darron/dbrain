package bskyapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/projection"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

type BookmarkOptions struct {
	Browser  string
	Profile  string
	Limit    int
	PageSize int
	MaxPages int
	Timeout  time.Duration
}

type BookmarkStats struct {
	PagesFetched       int    `json:"pages_fetched"`
	Seen               int    `json:"seen"`
	Processed          int    `json:"processed"`
	Skipped            int    `json:"skipped"` // total skipped entries; typed skip counters below are subsets
	SkippedBlocked     int    `json:"skipped_blocked"`
	SkippedNotFound    int    `json:"skipped_not_found"`
	SkippedUnsupported int    `json:"skipped_unsupported"`
	SkippedMalformed   int    `json:"skipped_malformed"`
	Created            int    `json:"created"`
	Updated            int    `json:"updated"`
	Unchanged          int    `json:"unchanged"`
	Rendered           int    `json:"rendered"`
	StoppedReason      string `json:"stopped_reason"`
}

func RunBookmarks(ctx context.Context, cfg config.Config, st *store.Store, opts BookmarkOptions) (BookmarkStats, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	state, err := readBSKYStorageFromProfile(opts.Browser, opts.Profile)
	if err != nil {
		return BookmarkStats{}, err
	}
	credentials, err := parsePersistedSession(state)
	if err != nil {
		return BookmarkStats{}, err
	}
	client, err := newBookmarkClient(credentials, &http.Client{Timeout: opts.Timeout})
	if err != nil {
		return BookmarkStats{}, err
	}
	return runBookmarks(ctx, cfg, st, client, opts)
}

func runBookmarks(ctx context.Context, cfg config.Config, st *store.Store, client *bookmarkClient, opts BookmarkOptions) (BookmarkStats, error) {
	if opts.PageSize <= 0 || opts.PageSize > 100 {
		opts.PageSize = 100
	}
	stats := BookmarkStats{}
	cursor := ""
	seenCursors := map[string]struct{}{}
	now := time.Now().UTC()
	renderer := projection.NewRenderer(cfg, st)

	for {
		if opts.MaxPages > 0 && stats.PagesFetched >= opts.MaxPages {
			stats.StoppedReason = "max pages reached"
			break
		}
		if opts.Limit > 0 && stats.Seen >= opts.Limit {
			stats.StoppedReason = "limit reached"
			break
		}

		page, err := client.fetchBookmarksPage(ctx, cursor, opts.PageSize)
		if err != nil {
			return stats, err
		}
		stats.PagesFetched++

		for _, view := range page.Bookmarks {
			if opts.Limit > 0 && stats.Seen >= opts.Limit {
				stats.StoppedReason = "limit reached"
				break
			}
			stats.Seen++
			item, err := bookmarkViewToItem(view, now)
			if err != nil {
				stats.Skipped++
				switch {
				case errors.Is(err, errBlockedBookmark):
					stats.SkippedBlocked++
				case errors.Is(err, errNotFoundBookmark):
					stats.SkippedNotFound++
				case errors.Is(err, errUnsupportedBookmark):
					stats.SkippedUnsupported++
				default:
					stats.SkippedMalformed++
				}
				continue
			}

			result, err := st.UpsertItem(ctx, item)
			if err != nil {
				return stats, fmt.Errorf("upsert Bluesky bookmark %s: %w", item.SourceKey, err)
			}
			stats.Processed++
			switch result.Status {
			case model.UpsertCreated:
				stats.Created++
			case model.UpsertUpdated:
				stats.Updated++
			case model.UpsertUnchanged:
				stats.Unchanged++
			}

			shouldRender := result.Status != model.UpsertUnchanged
			if !shouldRender {
				if _, err := vault.StatNote(cfg, item.NotePath); err != nil {
					shouldRender = true
				}
			}
			if shouldRender {
				if _, err := renderer.RefreshItem(ctx, item.SourceKey); err != nil {
					return stats, fmt.Errorf("render Bluesky bookmark note %s: %w", item.SourceKey, err)
				}
				stats.Rendered++
			}
		}
		if stats.StoppedReason == "limit reached" {
			break
		}

		if page.Cursor == "" {
			stats.StoppedReason = "end of bookmarks"
			break
		}
		if _, ok := seenCursors[page.Cursor]; ok {
			return stats, errors.New("bluesky bookmarks API returned a repeated cursor")
		}
		seenCursors[page.Cursor] = struct{}{}
		cursor = page.Cursor
	}
	return stats, nil
}
