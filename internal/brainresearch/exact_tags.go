package brainresearch

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/retrieval"
)

func (b *Builder) buildExactTagEvidence(ctx context.Context, topic string, hints ask.QueryHints, sourceTypes []string, maxChars int) ([]ask.Evidence, error) {
	tagQueries := append([]string(nil), hints.TagQueries...)
	if topicAlias := tagAlias(topic); topicAlias != "" {
		tagQueries = append(tagQueries, topicAlias)
	}
	tagQueries = uniqueStrings(tagQueries)
	if len(tagQueries) == 0 {
		return nil, nil
	}

	examples := make([]ask.Evidence, 0, maxExactTagEvidence)
	seen := map[string]struct{}{}
	for _, tagQuery := range tagQueries {
		results, err := b.st.SearchExactUserTag(ctx, tagQuery, maxExactTagEvidence*3)
		if err != nil {
			return nil, err
		}
		results = filterSearchResults(ctx, b.st, results, sourceTypes)
		for _, result := range results {
			if len(examples) >= maxExactTagEvidence {
				return examples, nil
			}
			if _, exists := seen[result.SourceKey]; exists {
				continue
			}
			if item, err := b.st.GetItem(ctx, result.SourceKey); err == nil {
				seen[result.SourceKey] = struct{}{}
				examples = append(examples, exactTagEvidenceFromItem(b.cfg.VaultDir, item, result, tagQuery, maxChars, hints.Terms))
				continue
			}
			source, err := b.st.GetSource(ctx, result.SourceKey)
			if err != nil {
				continue
			}
			seen[result.SourceKey] = struct{}{}
			examples = append(examples, exactTagEvidenceFromSource(b.cfg.VaultDir, source, result, tagQuery, maxChars, hints.Terms))
		}
	}
	return examples, nil
}

func exactTagEvidenceFromItem(vaultDir string, item model.Item, result model.SearchResult, tag string, maxChars int, terms []string) ask.Evidence {
	author := strings.TrimSpace(item.AuthorName)
	if handle := strings.TrimSpace(item.AuthorHandle); handle != "" {
		if author != "" {
			author += " "
		}
		author += "@" + strings.TrimPrefix(handle, "@")
	}
	excerpt := trimText(firstNonEmpty(
		item.SummaryText,
		item.XPostText,
		item.ArticleText,
		item.Text,
		item.OCRText,
		result.Snippet,
	), maxChars)
	matchText := strings.ToLower(strings.Join([]string{
		item.Title,
		item.CanonicalURL,
		item.AuthorName,
		item.AuthorHandle,
		item.UserTags,
		item.SummaryText,
		item.XPostText,
		item.ArticleText,
		item.Text,
		item.OCRText,
	}, "\n"))
	retrievalInfo := exactTagExampleRetrieval(tag, terms, matchText)
	return ask.Evidence{
		SourceKey:   item.SourceKey,
		Kind:        "item",
		Title:       item.Title,
		URL:         item.CanonicalURL,
		NotePath:    filepath.Join(vaultDir, filepath.FromSlash(item.NotePath)),
		Summary:     trimText(item.SummaryText, maxChars),
		Excerpt:     excerpt,
		Author:      author,
		SourceType:  item.SourceType,
		PublishedAt: item.PublishedAt,
		UserTags:    item.UserTags,
		Media:       retrieval.MediaRefs(item.Media),
		Retrieval:   &retrievalInfo,
	}
}

func exactTagEvidenceFromSource(vaultDir string, source model.SourceDocument, result model.SearchResult, tag string, maxChars int, terms []string) ask.Evidence {
	excerpt := trimText(firstNonEmpty(
		source.SummaryText,
		source.ExtractedText,
		source.Description,
		result.Snippet,
	), maxChars)
	matchText := strings.ToLower(strings.Join([]string{
		source.Title,
		source.CanonicalURL,
		source.Description,
		source.SiteName,
		source.UserTags,
		source.SummaryText,
		source.ExtractedText,
	}, "\n"))
	retrievalInfo := exactTagExampleRetrieval(tag, terms, matchText)
	return ask.Evidence{
		SourceKey:    source.SourceKey,
		Kind:         "source",
		Title:        firstNonEmpty(source.Title, source.CanonicalURL),
		URL:          source.CanonicalURL,
		NotePath:     filepath.Join(vaultDir, filepath.FromSlash(source.NotePath)),
		Summary:      trimText(source.SummaryText, maxChars),
		Excerpt:      excerpt,
		SourceType:   source.SourceType,
		ExtractedAt:  formatTime(source.ExtractedAt),
		SummarizedAt: formatTime(source.SummarizedAt),
		UserTags:     source.UserTags,
		Retrieval:    &retrievalInfo,
	}
}

func exactTagExampleRetrieval(tag string, terms []string, matchText string) ask.RetrievalInfo {
	info := ask.RetrievalInfo{
		Score: 12,
		Signals: []ask.RetrievalSignal{{
			Name:   "exact_user_tag_example",
			Detail: tag,
			Weight: 12,
		}},
	}
	for _, term := range uniqueStrings(terms) {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		if strings.Contains(matchText, term) {
			info.MatchedTerms = append(info.MatchedTerms, term)
		} else {
			info.MissingTerms = append(info.MissingTerms, term)
		}
	}
	if len(info.MatchedTerms) > 0 {
		info.Score += 2 * len(info.MatchedTerms)
		info.Signals = append(info.Signals, ask.RetrievalSignal{
			Name:   "exact_tag_example_matched_terms",
			Detail: strings.Join(info.MatchedTerms, ", "),
			Weight: 2 * len(info.MatchedTerms),
		})
	}
	return info
}
