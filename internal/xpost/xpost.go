package xpost

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"
)

type MediaObject struct {
	Type        string `json:"type,omitempty"`
	URL         string `json:"url,omitempty"`
	ExpandedURL string `json:"expanded_url,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
}

type Snapshot struct {
	ID                    string         `json:"id"`
	Text                  string         `json:"text"`
	Language              string         `json:"language"`
	AuthorHandle          string         `json:"author_handle"`
	AuthorName            string         `json:"author_name"`
	AuthorProfileImageURL string         `json:"author_profile_image_url,omitempty"`
	PostedAt              string         `json:"posted_at,omitempty"`
	URL                   string         `json:"url,omitempty"`
	Links                 []string       `json:"links,omitempty"`
	Media                 []string       `json:"media,omitempty"`
	MediaObjects          []MediaObject  `json:"media_objects,omitempty"`
	QuotedPost            *Snapshot      `json:"quoted_post,omitempty"`
	Raw                   map[string]any `json:"-"`
}

type envelope struct {
	Snapshot *Snapshot `json:"snapshot"`
}

func DecodeSnapshot(raw string) (*Snapshot, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false, nil
	}

	var env envelope
	if err := json.Unmarshal([]byte(raw), &env); err == nil && env.Snapshot != nil {
		return env.Snapshot, true, nil
	}

	var snapshot Snapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(snapshot.ID) == "" &&
		strings.TrimSpace(snapshot.Text) == "" &&
		strings.TrimSpace(snapshot.URL) == "" &&
		len(snapshot.MediaObjects) == 0 &&
		snapshot.QuotedPost == nil {
		return nil, false, nil
	}
	return &snapshot, true, nil
}

func NormalizeTimestamp(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	layouts := []string{
		time.RFC3339,
		time.RFC3339Nano,
		time.RubyDate,
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return value
}

func NotePath(snapshot *Snapshot) string {
	if snapshot == nil {
		return ""
	}
	id := strings.TrimSpace(snapshot.ID)
	if id == "" {
		return ""
	}
	year := "unknown"
	if postedAt := NormalizeTimestamp(snapshot.PostedAt); postedAt != "" {
		if t, err := time.Parse(time.RFC3339, postedAt); err == nil {
			year = t.Format("2006")
		}
	}
	return filepath.ToSlash(filepath.Join("items", "x", year, id+".md"))
}

func ForStorage(snapshot *Snapshot) *Snapshot {
	if snapshot == nil {
		return nil
	}

	cloned := &Snapshot{
		ID:                    snapshot.ID,
		Text:                  snapshot.Text,
		Language:              snapshot.Language,
		AuthorHandle:          snapshot.AuthorHandle,
		AuthorName:            snapshot.AuthorName,
		AuthorProfileImageURL: snapshot.AuthorProfileImageURL,
		PostedAt:              snapshot.PostedAt,
		URL:                   snapshot.URL,
	}
	if len(snapshot.Links) > 0 {
		cloned.Links = append([]string(nil), snapshot.Links...)
	}
	if len(snapshot.Media) > 0 {
		cloned.Media = append([]string(nil), snapshot.Media...)
	}
	if len(snapshot.MediaObjects) > 0 {
		cloned.MediaObjects = append([]MediaObject(nil), snapshot.MediaObjects...)
	}
	if snapshot.QuotedPost != nil {
		cloned.QuotedPost = ForStorage(snapshot.QuotedPost)
	}
	return cloned
}
