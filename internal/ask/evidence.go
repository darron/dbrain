package ask

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/store"
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
	if !hasAnyText(result.Snippet, source.SummaryText, source.Description) {
		extractedText, err := st.GetSourceExtractedText(ctx, result.SourceKey)
		if err != nil {
			return evidenceCandidate{}, false, nil
		}
		source.ExtractedText = extractedText
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

	return evidenceCandidate{
		Evidence: Evidence{
			SourceKey:   item.SourceKey,
			Kind:        "item",
			Title:       item.Title,
			URL:         item.CanonicalURL,
			NotePath:    filepath.Join(cfg.VaultDir, filepath.FromSlash(item.NotePath)),
			Summary:     trimTo(item.SummaryText, maxChars),
			Excerpt:     excerpt,
			Author:      author,
			SourceType:  item.SourceType,
			PublishedAt: item.PublishedAt,
			UserTags:    item.UserTags,
			Media:       retrieval.MediaRefs(item.Media),
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
			source.ExtractedText,
		)...,
	)
	return evidenceCandidate{
		Evidence: Evidence{
			SourceKey:    source.SourceKey,
			Kind:         "source",
			Title:        firstNonEmpty(source.Title, source.CanonicalURL),
			URL:          source.CanonicalURL,
			NotePath:     filepath.Join(cfg.VaultDir, filepath.FromSlash(source.NotePath)),
			Summary:      trimTo(source.SummaryText, maxChars),
			Excerpt:      excerpt,
			SourceType:   source.SourceType,
			ExtractedAt:  formatTime(source.ExtractedAt),
			SummarizedAt: formatTime(source.SummarizedAt),
			UserTags:     source.UserTags,
		},
		SourceID: source.ID,
		MatchText: compactMatchText(
			source.Title,
			source.CanonicalURL,
			source.Description,
			source.UserTags,
			source.SummaryText,
			result.Snippet,
			excerpt,
		),
	}
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
				addRetrievalSignal(&rel, "graph_related", "referencing item for "+candidate.SourceKey, 1)
				applyEntityMatches(&rel, entityMatches)
				related = append(related, rel)
			}
		}
	}
	rankCandidates(related)
	return related, nil
}
