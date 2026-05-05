package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func resolveGetContentMode(value string, includeContent *bool) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode == "" {
		if includeContent != nil && !*includeContent {
			return getModeBrief, nil
		}
		return getModeEvidence, nil
	}
	switch mode {
	case getModeBrief, getModeEvidence, getModeRaw, getModeRendered:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported content_mode %q; expected brief, evidence, raw, or rendered", value)
	}
}

func (s *Server) getPayloadForLookup(ctx context.Context, lookup string, mode string, maxChars int, query string) (map[string]interface{}, string, error) {
	lookup = strings.TrimSpace(lookup)
	if lookup == "" {
		return nil, "", fmt.Errorf("lookup is required")
	}
	query = strings.TrimSpace(query)
	if item, err := s.st.GetItem(ctx, lookup); err == nil {
		return s.getItemPayload(ctx, item, mode, maxChars, query)
	}
	if source, err := s.st.GetSource(ctx, lookup); err == nil {
		return s.getSourcePayload(ctx, source, mode, maxChars, query)
	}
	return nil, "", lookupNotFoundError(lookup)
}

func lookupNotFoundError(lookup string) error {
	return fmt.Errorf("lookup not found: %s (searched items and sources by source key, external id, URL, and note path)", lookup)
}

func maxGetSectionChars(value int) int {
	if value <= 0 {
		return defaultGetSectionChars
	}
	if value > maxGetSectionCharsHard {
		return maxGetSectionCharsHard
	}
	return value
}

func (s *Server) getItemPayload(ctx context.Context, item model.Item, mode string, maxChars int, query string) (map[string]interface{}, string, error) {
	available := itemAvailableSections(item)
	relatedItems, relatedItemSections, err := s.itemRelatedItemSections(ctx, item)
	if err != nil {
		return nil, "", err
	}
	relatedSources, relatedSourceSections, err := s.itemRelatedSourceSections(ctx, item)
	if err != nil {
		return nil, "", err
	}
	available = append(available, relatedItemSections...)
	available = append(available, relatedSourceSections...)

	sections := sectionsForMode(available, mode, maxChars, query)
	if mode == getModeRendered {
		content, err := readVaultNote(s.cfg.VaultDir, item.NotePath)
		if err != nil {
			return nil, "", err
		}
		sections = []getSection{makeGetSection("rendered_note", "rendered", "", "", "", time.Time{}, content, maxChars)}
	}

	payload := map[string]interface{}{
		"kind":                  "item",
		"title":                 item.Title,
		"source_key":            item.SourceKey,
		"url":                   item.CanonicalURL,
		"note":                  item.NotePath,
		"note_path":             item.NotePath,
		"content_mode":          mode,
		"max_chars_per_section": maxChars,
		"available_sections":    sectionCatalog(available),
		"content_sections":      sections,
		"item":                  slimItem(item),
	}
	if query != "" {
		payload["query"] = query
	}
	if len(relatedItems) > 0 {
		payload["related_items"] = relatedItems
	}
	if len(relatedSources) > 0 {
		payload["related_sources"] = relatedSources
	}
	if mode == getModeRendered && len(sections) > 0 {
		payload["content"] = sections[0].Text
	}
	return payload, formatGetPayload(payload), nil
}

func (s *Server) getSourcePayload(ctx context.Context, source model.SourceDocument, mode string, maxChars int, query string) (map[string]interface{}, string, error) {
	available := sourceAvailableSections(source)
	backlinks, backlinkSections, err := s.sourceBacklinkSections(ctx, source)
	if err != nil {
		return nil, "", err
	}
	available = append(available, backlinkSections...)

	sections := sectionsForMode(available, mode, maxChars, query)
	if mode == getModeRendered {
		content, err := readVaultNote(s.cfg.VaultDir, source.NotePath)
		if err != nil {
			return nil, "", err
		}
		sections = []getSection{makeGetSection("rendered_note", "rendered", "", "", "", time.Time{}, content, maxChars)}
	}

	title := firstNonEmpty(source.Title, source.CanonicalURL)
	payload := map[string]interface{}{
		"kind":                  "source",
		"title":                 title,
		"source_key":            source.SourceKey,
		"url":                   source.CanonicalURL,
		"note":                  source.NotePath,
		"note_path":             source.NotePath,
		"content_mode":          mode,
		"max_chars_per_section": maxChars,
		"available_sections":    sectionCatalog(available),
		"content_sections":      sections,
		"source":                slimSource(source),
	}
	if query != "" {
		payload["query"] = query
	}
	if len(backlinks) > 0 {
		payload["backlinks"] = backlinks
	}
	if mode == getModeRendered && len(sections) > 0 {
		payload["content"] = sections[0].Text
	}
	return payload, formatGetPayload(payload), nil
}
