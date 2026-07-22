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
	sourceKey, err := githubStarSourceKey(viewerLogin, record.Repo.FullName)
	if err != nil {
		return model.Item{}, err
	}
	starredAt := normalizeTimestamp(record.StarredAt)
	externalID := strings.TrimSpace(record.Repo.FullName)
	noteID := itemNoteID(externalID)
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
		SourceKey:       sourceKey,
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

func githubStarSourceKey(viewerLogin, repoFullName string) (string, error) {
	viewerLogin = strings.TrimSpace(viewerLogin)
	repoFullName = strings.TrimSpace(repoFullName)
	parts := strings.Split(repoFullName, "/")
	if viewerLogin == "" || len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", fmt.Errorf("invalid github star identity")
	}
	return "gh-star:" + viewerLogin + ":" + strings.ToLower(strings.TrimSpace(parts[0])+"/"+strings.TrimSpace(parts[1])), nil
}
