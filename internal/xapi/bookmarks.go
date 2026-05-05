package xapi

import (
	"context"
	"fmt"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

func RunBookmarks(ctx context.Context, cfg config.Config, st *store.Store, opts BookmarkOptions) (BookmarkStats, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.PageSize <= 0 {
		opts.PageSize = 20
	}
	if opts.PageSize > 100 {
		opts.PageSize = 100
	}
	if opts.StalePageLimit <= 0 {
		opts.StalePageLimit = 2
	}

	client, err := newClient(ctx, Options{
		Browser:   opts.Browser,
		Profile:   opts.Profile,
		CT0:       opts.CT0,
		AuthToken: opts.AuthToken,
		Timeout:   opts.Timeout,
		Logger:    opts.Logger,
	})
	if err != nil {
		return BookmarkStats{}, err
	}

	stats := BookmarkStats{}
	cursor := ""
	stalePages := 0
	now := time.Now().UTC()

pageLoop:
	for {
		if opts.MaxPages > 0 && stats.PagesFetched >= opts.MaxPages {
			stats.StoppedReason = "max pages reached"
			break
		}
		if opts.Limit > 0 && stats.Processed >= opts.Limit {
			stats.StoppedReason = "limit reached"
			break
		}

		page, err := client.FetchBookmarksPage(ctx, cursor, opts.PageSize)
		if err != nil {
			return stats, err
		}
		stats.PagesFetched++

		if len(page.Records) == 0 {
			stats.StoppedReason = "end of bookmarks"
			break
		}

		pageCreated := 0
		for _, record := range page.Records {
			if opts.Limit > 0 && stats.Processed >= opts.Limit {
				stats.StoppedReason = "limit reached"
				break pageLoop
			}

			item, err := bookmarkRecordToItem(record, now)
			if err != nil {
				return stats, err
			}

			result, err := st.UpsertItem(ctx, item)
			if err != nil {
				return stats, err
			}

			stats.Processed++
			switch result.Status {
			case model.UpsertCreated:
				stats.Created++
				pageCreated++
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
				if err := vault.WriteItem(cfg, item); err != nil {
					return stats, fmt.Errorf("render imported bookmark note %s: %w", item.SourceKey, err)
				}
				stats.Rendered++
			}
		}

		if !opts.Force {
			if pageCreated == 0 {
				stalePages++
				stats.StalePages = stalePages
				if stalePages >= opts.StalePageLimit {
					stats.StoppedReason = "overlap with existing bookmarks"
					break
				}
			} else {
				stalePages = 0
				stats.StalePages = 0
			}
		}

		if page.NextCursor == "" {
			stats.StoppedReason = "end of bookmarks"
			break
		}
		cursor = page.NextCursor
	}

	if stats.StoppedReason == "" {
		stats.StoppedReason = "completed"
	}
	return stats, nil
}
