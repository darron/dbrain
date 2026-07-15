package youtubeimport

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/vault"
)

func toItem(entry videoEntry, currentFeed feed, now time.Time) (model.Item, bool, error) {
	videoID := strings.TrimSpace(entry.ID)
	if videoID == "" {
		return model.Item{}, true, nil
	}
	sourceKey, err := youtubeSourceKey(currentFeed, videoID)
	if err != nil {
		return model.Item{}, false, err
	}

	title := strings.TrimSpace(entry.Title)
	if title == "" {
		title = "YouTube video " + videoID
	}

	watchURL := canonicalVideoURL(entry)
	publishedAt := normalizeYouTubeTimestamp(entry)
	signal := currentFeed.name

	payload := map[string]any{
		"feed": map[string]string{
			"name": currentFeed.name,
			"url":  currentFeed.url,
		},
		"video": entry,
	}
	rawJSONBytes, err := json.Marshal(payload)
	if err != nil {
		return model.Item{}, false, fmt.Errorf("marshal youtube item %s: %w", videoID, err)
	}

	item := model.Item{
		SourceKey:       sourceKey,
		SourceType:      currentFeed.sourceType,
		ExternalID:      videoID,
		CanonicalURL:    watchURL,
		Title:           title,
		AuthorHandle:    firstNonEmpty(strings.TrimSpace(entry.UploaderID), strings.TrimSpace(entry.ChannelID)),
		AuthorName:      firstNonEmpty(strings.TrimSpace(entry.Channel), strings.TrimSpace(entry.Uploader)),
		PublishedAt:     publishedAt,
		SavedAt:         "",
		SyncedAt:        "",
		Text:            strings.TrimSpace(entry.Description),
		PrimaryCategory: signal,
		PrimaryDomain:   "youtube.com",
		Categories:      signal,
		LinksJSON:       "[]",
		NotePath:        vault.NoteRelativePath(filepath.ToSlash(filepath.Join("youtube", signal)), chooseYear(publishedAt, now.Format(time.RFC3339)), videoID),
		RawJSON:         string(rawJSONBytes),
		ImportedAt:      now,
		UpdatedAt:       now,
		LastSeenAt:      now,
	}
	item.ContentHash = itemhash.Compute(item)

	return item, false, nil
}

func youtubeSourceKey(currentFeed feed, videoID string) (string, error) {
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return "", fmt.Errorf("youtube video id is required")
	}
	switch currentFeed.name {
	case "liked", "watch_later":
		return "yt:" + currentFeed.name + ":" + videoID, nil
	default:
		return "", fmt.Errorf("unsupported youtube feed %q", currentFeed.name)
	}
}

func sourceCandidateForVideo(rawURL string) model.SourceCandidate {
	canonical := canonicalizeVideoURL(rawURL)
	hash := shortHash(canonical)
	return model.SourceCandidate{
		OriginalURL:   rawURL,
		CanonicalURL:  canonical,
		NormalizedURL: canonical,
		SourceType:    "youtube",
		Domain:        "youtube.com",
		SourceKey:     "src:" + hash,
		NotePath:      vault.SourceNoteRelativePath("youtube", "youtube-"+hash),
	}
}

func canonicalVideoURL(entry videoEntry) string {
	if value := strings.TrimSpace(entry.WebpageURL); value != "" {
		return canonicalizeVideoURL(value)
	}
	if value := strings.TrimSpace(entry.URL); value != "" {
		if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
			return canonicalizeVideoURL(value)
		}
		return canonicalizeVideoURL("https://www.youtube.com/watch?v=" + value)
	}
	return canonicalizeVideoURL("https://www.youtube.com/watch?v=" + strings.TrimSpace(entry.ID))
}

func canonicalizeVideoURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}

	host := strings.ToLower(u.Hostname())
	switch host {
	case "youtu.be":
		videoID := strings.Trim(strings.TrimSpace(u.Path), "/")
		if videoID != "" {
			u = &url.URL{
				Scheme: "https",
				Host:   "www.youtube.com",
				Path:   "/watch",
			}
			query := url.Values{}
			query.Set("v", videoID)
			u.RawQuery = query.Encode()
			return u.String()
		}
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		query := u.Query()
		videoID := strings.TrimSpace(query.Get("v"))
		if strings.HasPrefix(strings.TrimSpace(u.Path), "/shorts/") {
			videoID = strings.Trim(strings.TrimPrefix(strings.TrimSpace(u.Path), "/shorts/"), "/")
		}
		if videoID != "" {
			clean := &url.URL{
				Scheme: "https",
				Host:   "www.youtube.com",
				Path:   "/watch",
			}
			values := url.Values{}
			values.Set("v", videoID)
			clean.RawQuery = values.Encode()
			return clean.String()
		}
	}
	u.Scheme = "https"
	u.Fragment = ""
	return strings.TrimSuffix(u.String(), "?")
}

func normalizeYouTubeTimestamp(entry videoEntry) string {
	if entry.Timestamp > 0 {
		return time.Unix(entry.Timestamp, 0).UTC().Format(time.RFC3339)
	}
	value := strings.TrimSpace(entry.UploadDate)
	if len(value) == 8 {
		if t, err := time.Parse("20060102", value); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return ""
}
