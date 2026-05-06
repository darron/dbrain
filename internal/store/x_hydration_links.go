package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/xpost"
)

func syncXHydrationLinksTx(ctx context.Context, tx *sql.Tx, itemID int64, currentLinksJSON string, apiJSON string, nowText string) (bool, error) {
	snapshot, ok, err := xpost.DecodeSnapshot(apiJSON)
	if err != nil {
		return false, fmt.Errorf("decode hydration snapshot links %d: %w", itemID, err)
	}
	if !ok || len(snapshot.Links) == 0 {
		return false, nil
	}

	links := uniqueNonEmptyStrings(snapshot.Links)
	if len(links) == 0 {
		return false, nil
	}
	linksJSONBytes, err := json.Marshal(links)
	if err != nil {
		return false, fmt.Errorf("marshal hydration links %d: %w", itemID, err)
	}
	linksJSON := string(linksJSONBytes)
	if currentLinksJSON == linksJSON {
		return false, nil
	}

	primaryDomain, domains, githubURLs := deriveItemLinkMetadata(links)
	if _, err := tx.ExecContext(ctx, `
		UPDATE items
		SET links_json = ?,
			primary_domain = ?,
			domains = ?,
			github_urls = ?,
			link_extract_synced_at = '',
			updated_at = ?
		WHERE id = ?`,
		linksJSON,
		primaryDomain,
		strings.Join(domains, ","),
		strings.Join(githubURLs, ","),
		nowText,
		itemID,
	); err != nil {
		return false, fmt.Errorf("sync hydration links %d: %w", itemID, err)
	}

	return true, nil
}
