package ask

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vaultfs"
)

func buildEvidence(ctx context.Context, cfg config.Config, st *store.Store, result model.SearchResult, maxChars int, terms []string) (evidenceCandidate, bool, error) {
	if !isSourceDocumentKey(result.SourceKey) {
		if item, err := st.GetItem(ctx, result.SourceKey); err == nil {
			return evidenceFromItem(cfg, item, result, maxChars, terms), true, nil
		}
	}

	source, err := st.GetSourceEvidence(ctx, result.SourceKey)
	if err != nil {
		if isSourceDocumentKey(result.SourceKey) {
			return evidenceCandidate{}, false, nil
		}
		if item, itemErr := st.GetItem(ctx, result.SourceKey); itemErr == nil {
			// Some synthetic lookups can resolve as items even when source loading fails.
			// Keep this as a fallback, not the common source-document path.
			return evidenceFromItem(cfg, item, result, maxChars, terms), true, nil
		}
		return evidenceCandidate{}, false, nil
	}
	needsRawEvidence := !hasAnyText(result.Snippet, source.SummaryText, source.Description) || !hasEnoughQueryCoverage([]string{result.Snippet, source.SummaryText, source.Description}, terms)
	if needsRawEvidence {
		extractedText, err := st.GetSourceExtractedText(ctx, result.SourceKey)
		if err != nil {
			return evidenceCandidate{}, false, nil
		}
		source.ExtractedText = extractedText
	} else if strings.TrimSpace(source.Title) == "" && sourceTitleFromExtractedText(result.Snippet) == "" {
		extractedText, err := st.GetSourceExtractedTextPrefix(ctx, result.SourceKey, 2000)
		if err == nil {
			source.ExtractedText = extractedText
		}
	}
	return evidenceFromSource(cfg, source, result, maxChars, terms), true, nil
}

func isSourceDocumentKey(sourceKey string) bool {
	return strings.HasPrefix(strings.TrimSpace(sourceKey), "src:")
}

func evidenceFromItem(cfg config.Config, item model.Item, result model.SearchResult, maxChars int, terms []string) evidenceCandidate {
	author := strings.TrimSpace(item.AuthorName)
	if strings.TrimSpace(item.AuthorHandle) != "" {
		handle := "@" + strings.TrimSpace(item.AuthorHandle)
		if author != "" {
			author += " "
		}
		author += handle
	}

	excerpt := evidenceExcerpt(maxChars, terms,
		excerptValuesWithRawFallback(
			terms,
			[]string{result.Snippet, item.SummaryText},
			item.XPostText,
			item.ArticleText,
			item.Text,
			item.OCRText,
		)...,
	)
	sections, chunk, role := itemEvidenceSections(item, excerpt, maxChars, terms)

	return evidenceCandidate{
		Evidence: Evidence{
			SourceKey:       item.SourceKey,
			Kind:            "item",
			Title:           item.Title,
			URL:             item.CanonicalURL,
			NotePath:        safeEvidenceNotePath(cfg.VaultDir, item.NotePath),
			Summary:         trimTo(item.SummaryText, maxChars),
			Excerpt:         excerpt,
			Author:          author,
			SourceType:      item.SourceType,
			PublishedAt:     item.PublishedAt,
			UserTags:        item.UserTags,
			EvidenceRole:    role,
			Chunk:           chunk,
			ContentSections: sections,
			Media:           retrieval.MediaRefs(item.Media),
		},
		ItemID: item.ID,
		MatchText: compactMatchText(
			item.Title,
			item.CanonicalURL,
			item.AuthorName,
			item.AuthorHandle,
			item.UserTags,
			item.SummaryText,
			result.Snippet,
			excerpt,
		),
	}
}

func evidenceFromSource(cfg config.Config, source model.SourceDocument, result model.SearchResult, maxChars int, terms []string) evidenceCandidate {
	excerpt := evidenceExcerpt(maxChars, terms,
		sourceExcerptValues(
			[]string{result.Snippet, source.SummaryText, source.Description},
			terms,
			source.ExtractedText,
		)...,
	)
	sections, chunk, role := sourceEvidenceSections(source, excerpt, maxChars, terms)
	title := sourceEvidenceTitle(source, result)
	return evidenceCandidate{
		Evidence: Evidence{
			SourceKey:       source.SourceKey,
			Kind:            "source",
			Title:           title,
			URL:             source.CanonicalURL,
			NotePath:        safeEvidenceNotePath(cfg.VaultDir, source.NotePath),
			Summary:         trimTo(source.SummaryText, maxChars),
			Excerpt:         excerpt,
			SourceType:      source.SourceType,
			ExtractedAt:     formatTime(source.ExtractedAt),
			SummarizedAt:    formatTime(source.SummarizedAt),
			UserTags:        source.UserTags,
			EvidenceRole:    role,
			Chunk:           chunk,
			ContentSections: sections,
		},
		SourceID: source.ID,
		MatchText: compactMatchText(
			title,
			source.CanonicalURL,
			source.Description,
			source.UserTags,
			source.SummaryText,
			result.Snippet,
			excerpt,
		),
	}
}

func safeEvidenceNotePath(vaultDir string, notePath string) string {
	root, err := vaultfs.Open(vaultDir)
	if err != nil {
		return ""
	}
	defer func() { _ = root.Close() }()
	if _, err := root.Stat(notePath); err != nil {
		return ""
	}
	return filepath.Join(vaultDir, filepath.FromSlash(notePath))
}

func sourceEvidenceTitle(source model.SourceDocument, result model.SearchResult) string {
	return firstNonEmpty(source.Title, result.Title, sourceTitleFromExtractedText(result.Snippet), sourceTitleFromExtractedText(source.ExtractedText), source.CanonicalURL)
}

func sourceTitleFromExtractedText(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if title, ok := strings.CutPrefix(line, "Title:"); ok {
			return strings.TrimSpace(title)
		}
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "#"))
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "url source:") || strings.HasPrefix(lower, "published time:") || lower == "markdown content:" {
			continue
		}
		return ""
	}
	return ""
}

func collectRelatedEvidence(ctx context.Context, cfg config.Config, st *store.Store, selected []evidenceCandidate, question string, terms []string, opts Options, seen map[string]struct{}, entityMatches map[string]entityMatch) ([]evidenceCandidate, error) {
	related := make([]evidenceCandidate, 0, opts.RelatedLimit)
	for _, candidate := range selected {
		if len(related) >= opts.RelatedLimit {
			break
		}

		if candidate.ItemID > 0 {
			refs, err := st.ListSourcesForItem(ctx, candidate.ItemID)
			if err != nil {
				return nil, err
			}
			for _, ref := range refs {
				if len(related) >= opts.RelatedLimit {
					break
				}
				if _, exists := seen[ref.SourceKey]; exists {
					continue
				}
				source, err := st.GetSource(ctx, ref.SourceKey)
				if err != nil {
					continue
				}
				rel := evidenceFromSource(cfg, source, model.SearchResult{}, opts.MaxCharsPerDoc, terms)
				if !matchesSourceTypes(opts.SourceTypes, rel.SourceType) {
					continue
				}
				rel.RelatedTo = candidate.SourceKey
				rel.Relationship = "linked source"
				scoreCandidate(&rel, question, terms)
				addRetrievalLane(&rel, RetrievalLane{Name: "lexical", Status: "used"})
				addRetrievalLane(&rel, RetrievalLane{Name: "graph_related", Status: "used", Reason: "linked source"})
				addRetrievalSignal(&rel, "graph_related", "linked source from "+candidate.SourceKey, 1)
				applyEntityMatches(&rel, entityMatches)
				related = append(related, rel)
			}
		}

		if candidate.SourceID > 0 {
			backlinks, err := st.ListBacklinksForSource(ctx, candidate.SourceID)
			if err != nil {
				return nil, err
			}
			for _, ref := range backlinks {
				if len(related) >= opts.RelatedLimit {
					break
				}
				if _, exists := seen[ref.SourceKey]; exists {
					continue
				}
				item, err := st.GetItem(ctx, ref.SourceKey)
				if err != nil {
					continue
				}
				rel := evidenceFromItem(cfg, item, model.SearchResult{}, opts.MaxCharsPerDoc, terms)
				if !matchesSourceTypes(opts.SourceTypes, rel.SourceType) {
					continue
				}
				rel.RelatedTo = candidate.SourceKey
				rel.Relationship = "referenced by"
				scoreCandidate(&rel, question, terms)
				addRetrievalLane(&rel, RetrievalLane{Name: "lexical", Status: "used"})
				addRetrievalLane(&rel, RetrievalLane{Name: "graph_related", Status: "used", Reason: "referenced by"})
				addRetrievalSignal(&rel, "graph_related", "referencing item for "+candidate.SourceKey, 1)
				applyEntityMatches(&rel, entityMatches)
				related = append(related, rel)
			}
		}
	}
	rankCandidates(related)
	return related, nil
}
