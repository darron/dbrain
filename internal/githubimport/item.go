package githubimport

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/itemhash"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/vault"
)

func toItem(viewerLogin string, record starRecord, now time.Time) (model.Item, error) {
	starredAt := normalizeTimestamp(record.StarredAt)
	externalID := record.Repo.FullName
	noteID := itemNoteID(record.Repo.FullName)
	links := make([]string, 0, 1)
	if value := strings.TrimSpace(record.Repo.Homepage); value != "" {
		links = append(links, value)
	}
	linksJSON, err := json.Marshal(links)
	if err != nil {
		return model.Item{}, fmt.Errorf("marshal github links: %w", err)
	}
	rawJSONBytes, err := json.Marshal(record)
	if err != nil {
		return model.Item{}, fmt.Errorf("marshal github star: %w", err)
	}

	item := model.Item{
		SourceKey:       "gh-star:" + viewerLogin + ":" + strings.ToLower(record.Repo.FullName),
		SourceType:      "github_star",
		ExternalID:      externalID,
		CanonicalURL:    strings.TrimSpace(record.Repo.HTMLURL),
		Title:           strings.TrimSpace(record.Repo.FullName),
		AuthorHandle:    strings.TrimSpace(record.Repo.Owner.Login),
		PublishedAt:     normalizeTimestamp(record.Repo.CreatedAt),
		SavedAt:         starredAt,
		SyncedAt:        starredAt,
		Language:        strings.TrimSpace(record.Repo.Language),
		Text:            strings.TrimSpace(record.Repo.Description),
		PrimaryCategory: "star",
		PrimaryDomain:   "github.com",
		LinksJSON:       string(linksJSON),
		Categories:      strings.Join(record.Repo.Topics, ", "),
		Domains:         "github.com",
		GitHubURLs:      strings.TrimSpace(record.Repo.HTMLURL),
		NotePath:        vault.NoteRelativePath("github", chooseYear(starredAt, normalizeTimestamp(record.Repo.CreatedAt), now.Format(time.RFC3339)), noteID),
		RawJSON:         string(rawJSONBytes),
		ImportedAt:      now,
		UpdatedAt:       now,
		LastSeenAt:      now,
	}
	if item.SyncedAt == "" {
		item.SyncedAt = now.Format(time.RFC3339)
	}
	item.ContentHash = itemhash.Compute(item)
	return item, nil
}
