package xapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/mediadownload"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
	"github.com/darron/dbrain/internal/xpost"
)

func syncQuotedPosts(ctx context.Context, cfg config.Config, st *store.Store, parent model.Item, hydration model.XHydration, snapshot *xpost.Snapshot, opts Options) (mediadownload.Stats, bool, int, error) {
	if snapshot == nil || snapshot.QuotedPost == nil {
		changed, err := st.ReplaceItemChildLinks(ctx, parent.ID, "quoted_post", nil)
		return mediadownload.Stats{}, changed, 0, err
	}

	visited := map[string]struct{}{
		strings.TrimSpace(parent.ExternalID): {},
	}
	childID, mediaStats, childRendered, err := upsertQuotedPostTree(ctx, cfg, st, snapshot.QuotedPost, hydration, opts, visited)
	if err != nil {
		return mediadownload.Stats{}, false, 0, err
	}
	if childID <= 0 {
		changed, err := st.ReplaceItemChildLinks(ctx, parent.ID, "quoted_post", nil)
		return mediadownload.Stats{}, changed, childRendered, err
	}
	linkChanged, err := st.ReplaceItemChildLinks(ctx, parent.ID, "quoted_post", []int64{childID})
	if err != nil {
		return mediadownload.Stats{}, false, 0, err
	}
	return mediaStats, linkChanged, childRendered, nil
}

func upsertQuotedPostTree(ctx context.Context, cfg config.Config, st *store.Store, snapshot *xpost.Snapshot, hydration model.XHydration, opts Options, visited map[string]struct{}) (int64, mediadownload.Stats, int, error) {
	if snapshot == nil {
		return 0, mediadownload.Stats{}, 0, nil
	}
	snapshotID := strings.TrimSpace(snapshot.ID)
	if snapshotID == "" {
		return 0, mediadownload.Stats{}, 0, nil
	}
	if _, exists := visited[snapshotID]; exists {
		return 0, mediadownload.Stats{}, 0, nil
	}
	visited[snapshotID] = struct{}{}

	item, err := quotedSnapshotToItem(snapshot, hydration.FetchedAt)
	if err != nil {
		return 0, mediadownload.Stats{}, 0, err
	}
	upsertResult, err := st.UpsertItem(ctx, item)
	if err != nil {
		return 0, mediadownload.Stats{}, 0, err
	}

	childHydration, err := buildSnapshotHydration(hydration.Status, snapshot, snapshot.Raw, hydration.FetchedAt)
	if err != nil {
		return 0, mediadownload.Stats{}, 0, err
	}
	hydrationChanged, err := st.SaveXHydration(ctx, upsertResult.ItemID, childHydration)
	if err != nil {
		return 0, mediadownload.Stats{}, 0, err
	}

	effectiveSnapshot := snapshot
	refreshedItem, err := st.GetItem(ctx, item.SourceKey)
	if err != nil {
		return 0, mediadownload.Stats{}, 0, err
	}
	if storedSnapshot, err := snapshotFromHydrationJSON(refreshedItem.ExternalID, refreshedItem.XPostJSON); err != nil {
		return 0, mediadownload.Stats{}, 0, err
	} else if storedSnapshot != nil {
		effectiveSnapshot = storedSnapshot
	}

	mediaStats, err := mediadownload.RunForItem(ctx, cfg, st, upsertResult.ItemID, mediadownload.Options{
		Force:   opts.Force,
		Timeout: opts.Timeout,
		Logger:  opts.Logger,
	})
	if err != nil {
		return 0, mediadownload.Stats{}, 0, err
	}

	var childIDs []int64
	rendered := 0
	if effectiveSnapshot.QuotedPost != nil {
		childID, nestedMediaStats, nestedRendered, err := upsertQuotedPostTree(ctx, cfg, st, effectiveSnapshot.QuotedPost, hydration, opts, visited)
		if err != nil {
			return 0, mediadownload.Stats{}, 0, err
		}
		mediaStats.Candidates += nestedMediaStats.Candidates
		mediaStats.Requested += nestedMediaStats.Requested
		mediaStats.Downloaded += nestedMediaStats.Downloaded
		mediaStats.Gone += nestedMediaStats.Gone
		mediaStats.Errors += nestedMediaStats.Errors
		mediaStats.Changed += nestedMediaStats.Changed
		rendered += nestedRendered
		if childID > 0 {
			childIDs = append(childIDs, childID)
		}
	}

	linkChanged, err := st.ReplaceItemChildLinks(ctx, upsertResult.ItemID, "quoted_post", childIDs)
	if err != nil {
		return 0, mediadownload.Stats{}, 0, err
	}

	if upsertResult.Status != model.UpsertUnchanged || hydrationChanged || mediaStats.Changed > 0 || linkChanged {
		refreshed, err := st.GetItem(ctx, item.SourceKey)
		if err != nil {
			return 0, mediadownload.Stats{}, 0, err
		}
		if err := vault.WriteItem(cfg, refreshed); err != nil {
			return 0, mediadownload.Stats{}, 0, fmt.Errorf("render quoted x note %s: %w", refreshed.SourceKey, err)
		}
		rendered++
	}

	return upsertResult.ItemID, mediaStats, rendered, nil
}

func snapshotFromHydrationJSON(fallbackTweetID, apiJSON string) (*xpost.Snapshot, error) {
	if strings.TrimSpace(apiJSON) == "" {
		return nil, nil
	}
	_, snapshot, _, err := normalizeHydration(model.XHydration{APIJSON: apiJSON}, fallbackTweetID)
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func quotedSnapshotToItem(snapshot *xpost.Snapshot, fetchedAt time.Time) (model.Item, error) {
	record := bookmarkRecord{
		ID:           strings.TrimSpace(snapshot.ID),
		TweetID:      strings.TrimSpace(snapshot.ID),
		URL:          strings.TrimSpace(snapshot.URL),
		Text:         strings.TrimSpace(snapshot.Text),
		AuthorHandle: strings.TrimSpace(snapshot.AuthorHandle),
		AuthorName:   strings.TrimSpace(snapshot.AuthorName),
		PostedAt:     xpost.NormalizeTimestamp(snapshot.PostedAt),
		BookmarkedAt: "",
		SyncedAt:     fetchedAt.UTC().Format(time.RFC3339),
		Language:     strings.TrimSpace(snapshot.Language),
		Links:        append([]string(nil), snapshot.Links...),
		IngestedVia:  "quoted-post",
	}
	item, err := bookmarkRecordToItem(record, fetchedAt.UTC())
	if err != nil {
		return model.Item{}, err
	}
	item.SourceType = "x_quote"
	item.SavedAt = ""
	item.RawJSON = string(mustJSON(snapshot))
	item.ContentHash = itemhash.Compute(item)
	return item, nil
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}
