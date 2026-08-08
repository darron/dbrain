package bskyapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

type bookmarkProjection struct {
	Item             model.Item
	MediaCandidates  []model.MediaCandidate
	MediaKnown       bool
	MediaUnavailable bool
}

type bookmarkMediaDecode struct {
	candidates  []model.MediaCandidate
	known       bool
	unavailable bool
	supported   bool
}

func decodeBookmarkMedia(ctx context.Context, did, canonicalURL string, recordEmbed, viewEmbed json.RawMessage, resolver videoBlobResolver) bookmarkMediaDecode {
	if isEmptyJSON(recordEmbed) && isEmptyJSON(viewEmbed) {
		return bookmarkMediaDecode{known: true}
	}
	if isEmptyJSON(viewEmbed) {
		viewEmbed = recordEmbed
	}
	return decodeBookmarkMediaView(ctx, did, canonicalURL, recordEmbed, viewEmbed, resolver)
}

func decodeBookmarkMediaView(ctx context.Context, did, canonicalURL string, recordEmbed, raw json.RawMessage, resolver videoBlobResolver) bookmarkMediaDecode {
	if isEmptyJSON(raw) {
		return bookmarkMediaDecode{known: true}
	}
	object, err := rawObject(raw)
	if err != nil {
		return bookmarkMediaDecode{unavailable: true}
	}
	typeName := rawString(object["$type"])
	switch typeName {
	case "app.bsky.embed.images#view", "app.bsky.embed.images":
		var view struct {
			Images []struct {
				Fullsize    string `json:"fullsize"`
				AspectRatio struct {
					Width  int `json:"width"`
					Height int `json:"height"`
				} `json:"aspectRatio"`
			} `json:"images"`
		}
		if err := json.Unmarshal(raw, &view); err != nil || len(view.Images) == 0 {
			return bookmarkMediaDecode{unavailable: true, supported: true}
		}
		candidates := make([]model.MediaCandidate, 0, len(view.Images))
		for _, image := range view.Images {
			remoteURL := strings.TrimSpace(image.Fullsize)
			if !validHTTPURL(remoteURL) {
				return bookmarkMediaDecode{unavailable: true, supported: true}
			}
			candidates = append(candidates, model.MediaCandidate{
				RemoteURL:   remoteURL,
				ExpandedURL: canonicalURL,
				MediaType:   "photo",
				Width:       image.AspectRatio.Width,
				Height:      image.AspectRatio.Height,
			})
		}
		return bookmarkMediaDecode{candidates: candidates, known: true, supported: true}
	case "app.bsky.embed.video#view", "app.bsky.embed.video":
		media := bookmarkMediaDecode{supported: true}
		playlist := rawString(object["playlist"])
		cid := rawString(object["cid"])
		if cid == "" {
			cid = blobCID(recordEmbed)
		}
		if cid == "" || resolver == nil {
			media.unavailable = true
			return media
		}
		remoteURL, err := resolver.ResolveVideoBlob(ctx, did, cid)
		if err != nil || !validHTTPURL(remoteURL) {
			media.unavailable = true
			return media
		}
		if playlist == "" {
			playlist = canonicalURL
		}
		media.candidates = []model.MediaCandidate{{RemoteURL: remoteURL, ExpandedURL: playlist, MediaType: "video"}}
		media.known = true
		return media
	case "app.bsky.embed.recordWithMedia#view", "app.bsky.embed.recordWithMedia":
		media, ok := object["media"]
		if !ok || isEmptyJSON(media) {
			return bookmarkMediaDecode{unavailable: true, supported: true}
		}
		decoded := decodeBookmarkMediaView(ctx, did, canonicalURL, recordEmbed, media, resolver)
		decoded.supported = true
		return decoded
	case "app.bsky.embed.external#view", "app.bsky.embed.external", "app.bsky.embed.record#viewRecord", "app.bsky.embed.record#view", "app.bsky.embed.record":
		return bookmarkMediaDecode{known: true, supported: true}
	default:
		if _, ok := object["external"]; ok {
			return bookmarkMediaDecode{known: true, supported: true}
		}
		return bookmarkMediaDecode{unavailable: true}
	}
}

func blobCID(raw json.RawMessage) string {
	object, err := rawObject(raw)
	if err != nil {
		return ""
	}
	if value, ok := object["video"]; ok {
		if cid := blobRefCID(value); cid != "" {
			return cid
		}
	}
	if value, ok := object["media"]; ok {
		if cid := blobCID(value); cid != "" {
			return cid
		}
	}
	return blobRefCID(raw)
}

func blobRefCID(raw json.RawMessage) string {
	object, err := rawObject(raw)
	if err != nil {
		return ""
	}
	if link := rawString(object["$link"]); link != "" {
		return link
	}
	if ref, ok := object["ref"]; ok {
		if link := blobRefCID(ref); link != "" {
			return link
		}
		if value := rawString(ref); value != "" {
			return value
		}
	}
	return ""
}

func collectEmbeddedLinks(raw json.RawMessage, add func(string)) {
	if isEmptyJSON(raw) {
		return
	}
	object, err := rawObject(raw)
	if err != nil {
		return
	}
	typeName := rawString(object["$type"])
	if external, ok := object["external"]; ok {
		if externalObject, err := rawObject(external); err == nil {
			add(rawString(externalObject["uri"]))
		}
	}
	switch typeName {
	case "app.bsky.embed.recordWithMedia#view", "app.bsky.embed.recordWithMedia":
		collectEmbeddedLinks(object["media"], add)
		collectEmbeddedLinks(object["record"], add)
	case "app.bsky.embed.record#viewRecord", "app.bsky.embed.record#view":
		if value, ok := object["value"]; ok {
			var record postRecord
			if json.Unmarshal(value, &record) == nil {
				collectRecordLinks(record, add)
				collectEmbeddedLinks(record.Embed, add)
			}
		}
		if embeds, ok := object["embeds"]; ok {
			var nested []json.RawMessage
			if json.Unmarshal(embeds, &nested) == nil {
				for _, embed := range nested {
					collectEmbeddedLinks(embed, add)
				}
			}
		}
	}
}

func collectRecordLinks(record postRecord, add func(string)) {
	for _, raw := range httpURLPattern.FindAllString(record.Text, -1) {
		add(raw)
	}
	for _, facet := range record.Facets {
		for _, feature := range facet.Features {
			if strings.HasSuffix(feature.Type, "#link") {
				add(feature.URI)
			}
		}
	}
}

func deriveBookmarkTitle(text string, recordEmbed, viewEmbed json.RawMessage, handle, did string) string {
	if title := deriveTitle(text); title != "" {
		return title
	}
	for _, embed := range []json.RawMessage{viewEmbed, recordEmbed} {
		var title string
		collectEmbedTitle(embed, &title)
		if title != "" {
			return title
		}
	}
	if handle = strings.TrimSpace(handle); handle != "" {
		return fmt.Sprintf("Post by @%s", handle)
	}
	if did = strings.TrimSpace(did); did != "" {
		return "Post by " + did
	}
	return "Bluesky post"
}

func collectEmbedTitle(raw json.RawMessage, title *string) {
	if title == nil || *title != "" || isEmptyJSON(raw) {
		return
	}
	object, err := rawObject(raw)
	if err != nil {
		return
	}
	typeName := rawString(object["$type"])
	if external, ok := object["external"]; ok {
		if externalObject, err := rawObject(external); err == nil {
			*title = deriveTitle(rawString(externalObject["title"]))
			if *title != "" {
				return
			}
		}
	}
	if typeName == "app.bsky.embed.images#view" || typeName == "app.bsky.embed.images" {
		var images struct {
			Images []struct {
				Alt string `json:"alt"`
			} `json:"images"`
		}
		if json.Unmarshal(raw, &images) == nil {
			for _, image := range images.Images {
				if alt := deriveTitle(image.Alt); alt != "" {
					*title = alt
					return
				}
			}
		}
	}
	if typeName == "app.bsky.embed.video#view" || typeName == "app.bsky.embed.video" {
		if alt := deriveTitle(rawString(object["alt"])); alt != "" {
			*title = alt
			return
		}
	}
	for _, key := range []string{"media", "record", "embed"} {
		collectEmbedTitle(object[key], title)
	}
	if embeds, ok := object["embeds"]; ok {
		var nested []json.RawMessage
		if json.Unmarshal(embeds, &nested) == nil {
			for _, embed := range nested {
				collectEmbedTitle(embed, title)
			}
		}
	}
}

func rawObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		if err == nil {
			err = errors.New("JSON value is not an object")
		}
		return nil, err
	}
	return object, nil
}

func rawString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	return ""
}

func isEmptyJSON(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value == "" || value == "null" || value == "{}"
}

func validHTTPURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.User == nil && parsed.Hostname() != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}
