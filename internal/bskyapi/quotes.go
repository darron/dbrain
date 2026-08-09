package bskyapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/mediadownload"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/projection"
	"github.com/darron/dbrain/internal/safehttp"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vault"
)

const maxBlueskyQuoteDepth = 4

type bskyQuoteSkip string

const (
	bskyQuoteSkipBlocked     bskyQuoteSkip = "blocked"
	bskyQuoteSkipNotFound    bskyQuoteSkip = "not_found"
	bskyQuoteSkipDetached    bskyQuoteSkip = "detached"
	bskyQuoteSkipUnsupported bskyQuoteSkip = "unsupported"
	bskyQuoteSkipMalformed   bskyQuoteSkip = "malformed"
)

type bskyQuoteView struct {
	URI       string
	CID       string
	Author    postAuthor
	Value     postRecord
	ValueRaw  json.RawMessage
	Embeds    []json.RawMessage
	IndexedAt string
	Raw       json.RawMessage
}

type bskyQuoteDecode struct {
	View *bskyQuoteView
	Skip bskyQuoteSkip
}

func decodeBskyQuote(raw json.RawMessage) bskyQuoteDecode {
	if isEmptyJSON(raw) {
		return bskyQuoteDecode{}
	}
	object, err := rawObject(raw)
	if err != nil {
		return bskyQuoteDecode{Skip: bskyQuoteSkipMalformed}
	}
	typeName := rawString(object["$type"])
	switch typeName {
	case "app.bsky.embed.recordWithMedia#view", "app.bsky.embed.recordWithMedia":
		record, ok := object["record"]
		if !ok || isEmptyJSON(record) {
			return bskyQuoteDecode{Skip: bskyQuoteSkipMalformed}
		}
		return decodeBskyQuote(record)
	case "app.bsky.embed.record#viewNotFound":
		return bskyQuoteDecode{Skip: bskyQuoteSkipNotFound}
	case "app.bsky.embed.record#viewBlocked":
		return bskyQuoteDecode{Skip: bskyQuoteSkipBlocked}
	case "app.bsky.embed.record#viewDetached":
		return bskyQuoteDecode{Skip: bskyQuoteSkipDetached}
	case "app.bsky.embed.record#viewRecord", "app.bsky.embed.record#view":
		return decodeBskyViewRecord(raw, object)
	case "app.bsky.feed.defs#generatorView", "app.bsky.graph.defs#listView", "app.bsky.labeler.defs#labelerView", "app.bsky.graph.defs#starterPackViewBasic":
		return bskyQuoteDecode{Skip: bskyQuoteSkipUnsupported}
	case "app.bsky.embed.images#view", "app.bsky.embed.images",
		"app.bsky.embed.gallery#view", "app.bsky.embed.gallery",
		"app.bsky.embed.video#view", "app.bsky.embed.video",
		"app.bsky.embed.external#view", "app.bsky.embed.external":
		return bskyQuoteDecode{}
	default:
		return bskyQuoteDecode{}
	}
}

func decodeBskyViewRecord(raw json.RawMessage, object map[string]json.RawMessage) bskyQuoteDecode {
	var view struct {
		URI       string            `json:"uri"`
		CID       string            `json:"cid"`
		Author    postAuthor        `json:"author"`
		Value     json.RawMessage   `json:"value"`
		Embeds    []json.RawMessage `json:"embeds"`
		IndexedAt string            `json:"indexedAt"`
	}
	if err := json.Unmarshal(raw, &view); err != nil || strings.TrimSpace(view.URI) == "" || isEmptyJSON(view.Value) {
		return bskyQuoteDecode{Skip: bskyQuoteSkipMalformed}
	}
	did, _, err := parsePostURI(view.URI)
	if err != nil {
		return bskyQuoteDecode{Skip: bskyQuoteSkipMalformed}
	}
	var value postRecord
	if err := json.Unmarshal(view.Value, &value); err != nil {
		return bskyQuoteDecode{Skip: bskyQuoteSkipMalformed}
	}
	if strings.TrimSpace(view.Author.DID) == "" {
		view.Author.DID = did
	}
	if strings.TrimSpace(view.Author.Handle) == "" {
		view.Author.Handle = view.Author.DID
	}
	sanitized, err := redactSessionFields(raw)
	if err != nil {
		return bskyQuoteDecode{Skip: bskyQuoteSkipMalformed}
	}
	if embeds, ok := object["embeds"]; ok && !isEmptyJSON(embeds) {
		var decoded []json.RawMessage
		if err := json.Unmarshal(embeds, &decoded); err != nil {
			return bskyQuoteDecode{Skip: bskyQuoteSkipMalformed}
		}
		view.Embeds = decoded
	}
	valueRaw, err := redactSessionFields(view.Value)
	if err != nil {
		return bskyQuoteDecode{Skip: bskyQuoteSkipMalformed}
	}
	return bskyQuoteDecode{View: &bskyQuoteView{
		URI:       strings.TrimSpace(view.URI),
		CID:       strings.TrimSpace(view.CID),
		Author:    view.Author,
		Value:     value,
		ValueRaw:  valueRaw,
		Embeds:    view.Embeds,
		IndexedAt: normalizeTimestamp(view.IndexedAt),
		Raw:       sanitized,
	}}
}

func nestedBskyQuote(view *bskyQuoteView) bskyQuoteDecode {
	if view == nil {
		return bskyQuoteDecode{}
	}
	for _, embed := range view.Embeds {
		decoded := decodeBskyQuote(embed)
		if decoded.View != nil || decoded.Skip != "" {
			return decoded
		}
	}
	return decodeBskyQuote(view.Value.Embed)
}

func bskyQuoteToItem(ctx context.Context, quote *bskyQuoteView, now time.Time, resolver videoBlobResolver) (model.Item, bookmarkMediaDecode, error) {
	if quote == nil {
		return model.Item{}, bookmarkMediaDecode{}, fmt.Errorf("nil Bluesky quote view")
	}
	post := postView{
		URI:       quote.URI,
		CID:       quote.CID,
		Author:    quote.Author,
		Record:    quote.ValueRaw,
		Embed:     quote.Raw,
		IndexedAt: quote.IndexedAt,
	}
	item, err := bookmarkViewToItemBase(bookmarkView{
		Subject: bookmarkSubject{URI: quote.URI, CID: quote.CID},
		Item:    mustJSON(post),
	}, now)
	if err != nil {
		return model.Item{}, bookmarkMediaDecode{}, err
	}
	item.SourceType = "bsky_quote"
	item.SavedAt = ""
	item.ContentHash = itemhash.Compute(item)
	media := decodeBookmarkMediaViews(ctx, quote.Author.DID, item.CanonicalURL, quote.Value.Embed, quote.Embeds, resolver)
	return item, media, nil
}

type bskyQuoteImportStats struct {
	media            mediadownload.Stats
	mediaCandidates  int
	mediaLinked      int
	mediaUnavailable int
	linked           int
	skipped          int
	skip             bskyQuoteSkip
	skipCounts       map[bskyQuoteSkip]int
	linkChanged      bool
	rendered         int
}

func syncBskyQuoteTree(ctx context.Context, cfg config.Config, st *store.Store, parentID int64, parentURI string, quote *bskyQuoteView, skip bskyQuoteSkip, now time.Time, resolver videoBlobResolver, mediaHTTPPolicy *safehttp.Policy, renderer *projection.Renderer) (bskyQuoteImportStats, error) {
	if quote == nil || skip != "" {
		changed, err := st.ReplaceItemChildLinks(ctx, parentID, "quoted_post", nil)
		stats := bskyQuoteImportStats{linkChanged: changed}
		addBskyQuoteSkip(&stats, skip)
		return stats, err
	}
	visited := map[string]struct{}{strings.TrimSpace(parentURI): {}}
	childID, stats, err := upsertBskyQuoteTree(ctx, cfg, st, quote, 0, now, resolver, mediaHTTPPolicy, renderer, visited)
	if err != nil {
		return bskyQuoteImportStats{}, err
	}
	if childID <= 0 {
		changed, err := st.ReplaceItemChildLinks(ctx, parentID, "quoted_post", nil)
		stats.linkChanged = stats.linkChanged || changed
		return stats, err
	}
	changed, err := st.ReplaceItemChildLinks(ctx, parentID, "quoted_post", []int64{childID})
	if err != nil {
		return bskyQuoteImportStats{}, err
	}
	stats.linkChanged = stats.linkChanged || changed
	stats.linked++
	return stats, nil
}

func upsertBskyQuoteTree(ctx context.Context, cfg config.Config, st *store.Store, quote *bskyQuoteView, depth int, now time.Time, resolver videoBlobResolver, mediaHTTPPolicy *safehttp.Policy, renderer *projection.Renderer, visited map[string]struct{}) (int64, bskyQuoteImportStats, error) {
	if quote == nil || depth >= maxBlueskyQuoteDepth {
		return 0, bskyQuoteImportStats{}, nil
	}
	uri := strings.TrimSpace(quote.URI)
	if uri == "" {
		stats := bskyQuoteImportStats{}
		addBskyQuoteSkip(&stats, bskyQuoteSkipMalformed)
		return 0, stats, nil
	}
	if _, ok := visited[uri]; ok {
		return 0, bskyQuoteImportStats{}, nil
	}
	visited[uri] = struct{}{}

	item, media, err := bskyQuoteToItem(ctx, quote, now, resolver)
	if err != nil {
		return 0, bskyQuoteImportStats{}, err
	}
	stats := bskyQuoteImportStats{mediaCandidates: len(media.candidates)}
	if media.unavailable {
		stats.mediaUnavailable = 1
	}

	existing, lookupErr := st.GetItem(ctx, item.SourceKey)
	directBookmark := lookupErr == nil && existing.SourceType == "bsky_bookmark"
	if lookupErr != nil && !errors.Is(lookupErr, store.ErrItemNotFound) {
		return 0, bskyQuoteImportStats{}, lookupErr
	}
	var upsert model.UpsertResult
	if directBookmark {
		item = existing
		upsert = model.UpsertResult{Status: model.UpsertUnchanged, ItemID: existing.ID, NotePath: existing.NotePath}
	} else {
		upsert, err = st.UpsertItem(ctx, item)
		if err != nil {
			return 0, bskyQuoteImportStats{}, err
		}
		if media.known {
			changed, err := st.SaveItemMediaCandidates(ctx, upsert.ItemID, media.candidates)
			if err != nil {
				return 0, bskyQuoteImportStats{}, err
			}
			if changed {
				stats.mediaLinked = 1
			}
		}
	}

	mediaStats, err := mediadownload.RunForItem(ctx, cfg, st, upsert.ItemID, mediadownload.Options{
		MediaNamespace: mediadownload.MediaNamespaceForSourceType(item.SourceType),
		HTTPPolicy:     mediaHTTPPolicy,
	})
	if err != nil {
		return 0, bskyQuoteImportStats{}, err
	}
	addMediaStats(&stats.media, mediaStats)

	nested := nestedBskyQuote(quote)
	var nestedStats bskyQuoteImportStats
	var nestedID int64
	if nested.View != nil {
		nestedID, nestedStats, err = upsertBskyQuoteTree(ctx, cfg, st, nested.View, depth+1, now, resolver, mediaHTTPPolicy, renderer, visited)
		if err != nil {
			return 0, bskyQuoteImportStats{}, err
		}
	} else if nested.Skip != "" {
		addBskyQuoteSkip(&nestedStats, nested.Skip)
	}
	childIDs := []int64(nil)
	if nestedID > 0 {
		childIDs = []int64{nestedID}
	}
	linkChanged, err := st.ReplaceItemChildLinks(ctx, upsert.ItemID, "quoted_post", childIDs)
	if err != nil {
		return 0, bskyQuoteImportStats{}, err
	}
	stats.linkChanged = linkChanged
	if nestedID > 0 {
		stats.linked++
	}
	mergeBskyQuoteStats(&stats, nestedStats)

	shouldRender := (!directBookmark && upsert.Status != model.UpsertUnchanged) || stats.mediaLinked > 0 || mediaStats.Changed > 0 || linkChanged || nestedStats.linkChanged
	if !shouldRender {
		if _, err := vault.StatNote(cfg, item.NotePath); err != nil {
			shouldRender = true
		}
	}
	if shouldRender {
		if _, err := renderer.RefreshItem(ctx, item.SourceKey); err != nil {
			return 0, bskyQuoteImportStats{}, fmt.Errorf("render Bluesky quote note %s: %w", item.SourceKey, err)
		}
		stats.rendered++
	}

	return upsert.ItemID, stats, nil
}

func mergeBskyQuoteStats(dst *bskyQuoteImportStats, src bskyQuoteImportStats) {
	addMediaStats(&dst.media, src.media)
	dst.mediaCandidates += src.mediaCandidates
	dst.mediaLinked += src.mediaLinked
	dst.mediaUnavailable += src.mediaUnavailable
	dst.linked += src.linked
	dst.skipped += src.skipped
	if dst.skip == "" {
		dst.skip = src.skip
	}
	if len(src.skipCounts) > 0 {
		if dst.skipCounts == nil {
			dst.skipCounts = make(map[bskyQuoteSkip]int)
		}
		for reason, count := range src.skipCounts {
			dst.skipCounts[reason] += count
		}
	}
	dst.linkChanged = dst.linkChanged || src.linkChanged
	dst.rendered += src.rendered
}

func addMediaStats(dst *mediadownload.Stats, src mediadownload.Stats) {
	dst.Candidates += src.Candidates
	dst.Requested += src.Requested
	dst.Downloaded += src.Downloaded
	dst.Gone += src.Gone
	dst.Errors += src.Errors
	dst.Blocked += src.Blocked
	dst.Changed += src.Changed
}

func addBskyQuoteSkip(stats *bskyQuoteImportStats, skip bskyQuoteSkip) {
	if stats == nil || skip == "" {
		return
	}
	if stats.skipCounts == nil {
		stats.skipCounts = make(map[bskyQuoteSkip]int)
	}
	stats.skipped++
	stats.skipCounts[skip]++
	if stats.skip == "" {
		stats.skip = skip
	}
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}
