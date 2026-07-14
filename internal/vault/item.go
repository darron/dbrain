package vault

import (
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/xpost"
)

func WriteItem(cfg config.Config, item model.Item) error {
	body, err := RenderItemWithOptions(item, renderOptionsForConfig(cfg))
	if err != nil {
		return err
	}
	return writeRenderedNote(cfg, item.NotePath, body, "item note")
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
