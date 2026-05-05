package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/xpost"
)

func WriteItem(cfg config.Config, item model.Item) error {
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(item.NotePath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return fmt.Errorf("create note dir: %w", err)
	}

	body, err := RenderItemWithOptions(item, renderOptionsForConfig(cfg))
	if err != nil {
		return err
	}

	existing, err := os.ReadFile(fullPath)
	if err == nil && string(existing) == body {
		return nil
	}
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write note: %w", err)
	}
	return nil
}

func RenderItem(item model.Item) (string, error) {
	return RenderItemWithOptions(item, RenderOptions{})
}

func RenderItemWithOptions(item model.Item, opts RenderOptions) (string, error) {
	links, err := decodeStringArray(item.LinksJSON)
	if err != nil {
		return "", fmt.Errorf("decode links for %s: %w", item.SourceKey, err)
	}
	snapshot, _, _ := xpost.DecodeSnapshot(item.XPostJSON)

	var b strings.Builder
	writeItemFrontmatter(&b, item, links)
	writeItemTitle(&b, item)
	writeItemSourceSection(&b, item)
	writeItemSummarySection(&b, item)
	writeItemEvidenceSections(&b, item, snapshot)
	writeItemMediaSection(&b, item.Media, opts)
	writeItemLinksSection(&b, links)
	writeItemMetadataSection(&b, item)
	return b.String(), nil
}
