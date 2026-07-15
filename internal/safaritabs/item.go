package safaritabs

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/vault"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func itemFromTab(tab Tab, now time.Time) (model.Item, error) {
	u, err := url.Parse(tab.URL)
	if err != nil {
		return model.Item{}, fmt.Errorf("parse Safari tab URL %q: %w", tab.URL, err)
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	title := strings.TrimSpace(tab.Title)
	if title == "" {
		title = tab.URL
	}
	lastViewed := tab.LastViewed
	if lastViewed.IsZero() {
		lastViewed = now
	}
	linksJSON, _ := json.Marshal([]string{tab.URL})
	rawJSON, _ := json.Marshal(map[string]any{
		"source_type":       sourceType,
		"tab_uuid":          tab.UUID,
		"device_uuid":       tab.DeviceUUID,
		"device_name":       tab.DeviceName,
		"device_type":       tab.DeviceType,
		"title":             tab.Title,
		"url":               tab.URL,
		"is_showing_reader": tab.IsShowingReader,
		"is_pinned":         tab.IsPinned,
		"scene_id":          tab.SceneID,
		"last_viewed_at":    formatTime(lastViewed),
	})

	sourceKey := safariTabSourceKey(tab.DeviceUUID, tab.UUID)
	item := model.Item{
		SourceKey:     sourceKey,
		SourceType:    sourceType,
		ExternalID:    tab.UUID,
		CanonicalURL:  tab.URL,
		Title:         title,
		SavedAt:       formatTime(lastViewed),
		SyncedAt:      formatTime(now),
		Text:          safariTabText(tab, lastViewed),
		PrimaryDomain: host,
		LinksJSON:     string(linksJSON),
		NotePath:      vault.NoteRelativePath("safari-tabs", yearFor(lastViewed), safariTabSlug(tab)),
		RawJSON:       string(rawJSON),
		UpdatedAt:     now,
		LastSeenAt:    lastViewed,
	}
	item.ContentHash = itemhash.Compute(item)
	return item, nil
}

func safariTabSourceKey(deviceUUID, tabUUID string) string {
	return "safari-tab:" + deviceUUID + ":" + tabUUID
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return scheme == "http" || scheme == "https"
}

func safariTabText(tab Tab, lastViewed time.Time) string {
	var b strings.Builder
	b.WriteString("Safari tab captured from iCloud Tabs.\n\n")
	if strings.TrimSpace(tab.DeviceName) != "" {
		b.WriteString("Device: ")
		b.WriteString(tab.DeviceName)
		b.WriteString("\n")
	}
	if strings.TrimSpace(tab.Title) != "" {
		b.WriteString("Title: ")
		b.WriteString(tab.Title)
		b.WriteString("\n")
	}
	b.WriteString("URL: ")
	b.WriteString(tab.URL)
	b.WriteString("\n")
	if !lastViewed.IsZero() {
		b.WriteString("Last viewed: ")
		b.WriteString(formatTime(lastViewed))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func safariTabSlug(tab Tab) string {
	base := strings.TrimSpace(tab.Title)
	if base == "" {
		base = strings.TrimSpace(tab.URL)
	}
	slug := nonSlug.ReplaceAllString(strings.ToLower(base), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 72 {
		slug = strings.Trim(slug[:72], "-")
	}
	if slug == "" {
		slug = "tab"
	}
	return slug + "-" + shortHash(tab.DeviceUUID+":"+tab.UUID)
}
