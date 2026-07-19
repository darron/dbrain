package brainresearch

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/ask"
)

func buildCoverage(evidence []ask.Evidence) Coverage {
	evidence = uniqueParentEvidence(evidence)
	return Coverage{
		EvidenceCount: len(evidence),
		ByKind:        countValues(evidence, func(doc ask.Evidence) string { return doc.Kind }, 10),
		BySourceType:  countValues(evidence, func(doc ask.Evidence) string { return doc.SourceType }, 10),
		TopUserTags:   topTags(evidence, 12),
	}
}

func uniqueParentEvidence(evidence []ask.Evidence) []ask.Evidence {
	out := make([]ask.Evidence, 0, len(evidence))
	seen := map[string]struct{}{}
	for _, row := range evidence {
		key := strings.TrimSpace(row.SourceKey)
		if row.Chunk != nil && strings.TrimSpace(row.Chunk.ParentSourceKey) != "" {
			key = strings.TrimSpace(row.Chunk.ParentSourceKey)
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, row)
	}
	return out
}

func (b *Builder) buildCorpusCoverage(ctx context.Context, topic string, hints ask.QueryHints, sourceTypes []string, limit int, relatedLimit int) (Coverage, error) {
	coverage := Coverage{
		DisplayedLimit: limit,
		RelatedLimit:   relatedLimit,
	}
	tagQueries := append([]string(nil), hints.TagQueries...)
	if topicAlias := tagAlias(topic); topicAlias != "" {
		tagQueries = append(tagQueries, topicAlias)
	}
	tagQueries = uniqueStrings(tagQueries)
	for _, tagQuery := range tagQueries {
		count, err := b.st.CountExactUserTag(ctx, tagQuery, sourceTypes)
		if err != nil {
			return Coverage{}, err
		}
		if count > 0 {
			coverage.ExactTagMatches = append(coverage.ExactTagMatches, Bucket{Key: tagQuery, Count: count})
		}
	}
	sort.Slice(coverage.ExactTagMatches, func(i, j int) bool {
		if coverage.ExactTagMatches[i].Count != coverage.ExactTagMatches[j].Count {
			return coverage.ExactTagMatches[i].Count > coverage.ExactTagMatches[j].Count
		}
		return coverage.ExactTagMatches[i].Key < coverage.ExactTagMatches[j].Key
	})

	textQuery := strings.TrimSpace(topic)
	if textQuery == "" {
		textQuery = strings.TrimSpace(hints.TextQuery)
	}
	if textQuery != "" {
		itemCount, err := b.st.CountItemTextMatches(ctx, textQuery, sourceTypes)
		if err != nil {
			return Coverage{}, err
		}
		sourceCount, err := b.st.CountSourceTextMatches(ctx, textQuery, sourceTypes)
		if err != nil {
			return Coverage{}, err
		}
		coverage.ItemTextMatches = itemCount
		coverage.SourceTextMatches = sourceCount
	}
	return coverage, nil
}

func mergeCoverage(base Coverage, corpus Coverage) Coverage {
	base.ExactTagMatches = corpus.ExactTagMatches
	base.ItemTextMatches = corpus.ItemTextMatches
	base.SourceTextMatches = corpus.SourceTextMatches
	base.TopicNodeCount = corpus.TopicNodeCount
	base.TopicEdgeCount = corpus.TopicEdgeCount
	base.DisplayedLimit = corpus.DisplayedLimit
	base.RelatedLimit = corpus.RelatedLimit
	base.RecallNote = corpus.RecallNote
	return base
}

func recallNote(coverage Coverage) string {
	var parts []string
	if len(coverage.ExactTagMatches) > 0 {
		total := 0
		labels := make([]string, 0, len(coverage.ExactTagMatches))
		for _, bucket := range coverage.ExactTagMatches {
			total += bucket.Count
			labels = append(labels, fmt.Sprintf("%s=%d", bucket.Key, bucket.Count))
		}
		parts = append(parts, fmt.Sprintf("exact user-tag matches: %s (sum=%d)", strings.Join(labels, ", "), total))
	}
	if coverage.ItemTextMatches > 0 || coverage.SourceTextMatches > 0 {
		parts = append(parts, fmt.Sprintf("phrase/text matches: items=%d sources=%d", coverage.ItemTextMatches, coverage.SourceTextMatches))
	}
	if coverage.TopicNodeCount > 0 {
		parts = append(parts, fmt.Sprintf("topic brief shows %d nodes and %d edges", coverage.TopicNodeCount, coverage.TopicEdgeCount))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ") + ". Returned evidence is a capped working set, not the full corpus."
}

func countValues(evidence []ask.Evidence, keyFn func(ask.Evidence) string, limit int) []Bucket {
	counts := map[string]int{}
	for _, doc := range evidence {
		key := strings.TrimSpace(keyFn(doc))
		if key == "" {
			key = "unknown"
		}
		counts[key]++
	}
	return orderedBuckets(counts, limit)
}

func topTags(evidence []ask.Evidence, limit int) []Bucket {
	counts := map[string]int{}
	for _, doc := range evidence {
		for _, tag := range strings.Split(doc.UserTags, ",") {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			counts[tag]++
		}
	}
	return orderedBuckets(counts, limit)
}

func orderedBuckets(counts map[string]int, limit int) []Bucket {
	buckets := make([]Bucket, 0, len(counts))
	for key, count := range counts {
		buckets = append(buckets, Bucket{Key: key, Count: count})
	}
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].Count != buckets[j].Count {
			return buckets[i].Count > buckets[j].Count
		}
		return strings.ToLower(buckets[i].Key) < strings.ToLower(buckets[j].Key)
	})
	if limit > 0 && len(buckets) > limit {
		buckets = buckets[:limit]
	}
	return buckets
}
