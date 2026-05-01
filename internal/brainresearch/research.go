package brainresearch

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/queryterms"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/topics"
)

const (
	SchemaVersion       = "research_pack.v1"
	maxExactTagEvidence = 3
)

type Builder struct {
	cfg config.Config
	st  *store.Store
}

type Options struct {
	Question       string
	Topic          string
	Limit          int
	SourceTypes    []string
	IncludeRelated bool
	RelatedLimit   int
	SeedLimit      int
	IncludeTopic   *bool
	MaxCharsPerDoc int
}

type Pack struct {
	SchemaVersion    string            `json:"schema_version"`
	Question         string            `json:"question"`
	Mode             string            `json:"mode"`
	QueryPlan        QueryPlan         `json:"query_plan"`
	Coverage         Coverage          `json:"coverage"`
	Topic            string            `json:"topic,omitempty"`
	UsedTopicBrief   bool              `json:"used_topic_brief"`
	Evidence         []ask.Evidence    `json:"evidence"`
	ExactTagEvidence []ask.Evidence    `json:"exact_tag_evidence,omitempty"`
	TopicBrief       *TopicBrief       `json:"topic_brief,omitempty"`
	NextSteps        []SuggestedAction `json:"next_steps,omitempty"`
}

type QueryPlan struct {
	TextQuery         string   `json:"text_query"`
	QueryTerms        []string `json:"query_terms"`
	TagQueries        []string `json:"tag_queries"`
	SourceTypes       []string `json:"source_types,omitempty"`
	Limit             int      `json:"limit"`
	MaxCharsPerDoc    int      `json:"max_chars_per_doc"`
	IncludeRelated    bool     `json:"include_related"`
	RelatedLimit      int      `json:"related_limit,omitempty"`
	Topic             string   `json:"topic,omitempty"`
	TopicSource       string   `json:"topic_source,omitempty"`
	IncludeTopicBrief bool     `json:"include_topic_brief"`
}

type Coverage struct {
	EvidenceCount     int      `json:"evidence_count"`
	ByKind            []Bucket `json:"by_kind"`
	BySourceType      []Bucket `json:"by_source_type"`
	TopUserTags       []Bucket `json:"top_user_tags,omitempty"`
	ExactTagMatches   []Bucket `json:"exact_tag_matches,omitempty"`
	ItemTextMatches   int      `json:"item_text_matches,omitempty"`
	SourceTextMatches int      `json:"source_text_matches,omitempty"`
	TopicNodeCount    int      `json:"topic_node_count,omitempty"`
	TopicEdgeCount    int      `json:"topic_edge_count,omitempty"`
	DisplayedLimit    int      `json:"displayed_limit"`
	RelatedLimit      int      `json:"related_limit,omitempty"`
	RecallNote        string   `json:"recall_note,omitempty"`
}

type Bucket struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type TopicBrief struct {
	Topic        string                `json:"topic"`
	SourceTypes  []string              `json:"source_types,omitempty"`
	SeedLimit    int                   `json:"seed_limit"`
	RelatedLimit int                   `json:"related_limit"`
	Summary      string                `json:"summary"`
	Pivots       topics.TopicPivots    `json:"pivots,omitempty"`
	Entities     []topics.TopicEntity  `json:"entities,omitempty"`
	Nodes        []topics.TopicMapNode `json:"nodes"`
	Edges        []topics.TopicMapEdge `json:"edges"`
}

type SuggestedAction struct {
	Action string                 `json:"action"`
	Label  string                 `json:"label"`
	Reason string                 `json:"reason,omitempty"`
	Params map[string]interface{} `json:"params,omitempty"`
}

func New(cfg config.Config, st *store.Store) *Builder {
	return &Builder{cfg: cfg, st: st}
}

func Build(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Pack, error) {
	return New(cfg, st).Build(ctx, opts)
}

func (b *Builder) Build(ctx context.Context, opts Options) (Pack, error) {
	question := strings.TrimSpace(opts.Question)
	if question == "" {
		return Pack{}, fmt.Errorf("question is required")
	}

	hints := ask.Hints(question)
	limit := defaultInt(opts.Limit, 8)
	maxChars := defaultInt(opts.MaxCharsPerDoc, 700)
	topic, topicSource, hasTopic := resolveTopic(question, opts.Topic)
	includeTopic := hasTopic
	if opts.IncludeTopic != nil {
		includeTopic = *opts.IncludeTopic
	}
	if includeTopic && !hasTopic {
		topic = normalizeTopicPhrase(question)
		if topic != "" {
			topicSource = "normalized_question"
			hasTopic = true
		}
	}
	if !hasTopic {
		includeTopic = false
	}

	resp, err := ask.Run(ctx, b.cfg, b.st, question, ask.Options{
		Limit:          limit,
		RetrieveOnly:   true,
		SourceTypes:    opts.SourceTypes,
		IncludeRelated: opts.IncludeRelated,
		RelatedLimit:   opts.RelatedLimit,
		MaxCharsPerDoc: maxChars,
	})
	if err != nil {
		return Pack{}, err
	}

	corpusCoverage, err := b.buildCorpusCoverage(ctx, topic, hints, opts.SourceTypes, limit, opts.RelatedLimit)
	if err != nil {
		return Pack{}, err
	}
	exactTagEvidence, err := b.buildExactTagEvidence(ctx, topic, hints, opts.SourceTypes, maxChars)
	if err != nil {
		return Pack{}, err
	}

	pack := Pack{
		SchemaVersion: SchemaVersion,
		Question:      question,
		Mode:          "evidence_only",
		QueryPlan: QueryPlan{
			TextQuery:         hints.TextQuery,
			QueryTerms:        hints.Terms,
			TagQueries:        hints.TagQueries,
			SourceTypes:       opts.SourceTypes,
			Limit:             limit,
			MaxCharsPerDoc:    maxChars,
			IncludeRelated:    opts.IncludeRelated,
			RelatedLimit:      opts.RelatedLimit,
			Topic:             topic,
			TopicSource:       topicSource,
			IncludeTopicBrief: includeTopic,
		},
		Evidence:         resp.Evidence,
		ExactTagEvidence: exactTagEvidence,
		Coverage:         mergeCoverage(buildCoverage(resp.Evidence), corpusCoverage),
		NextSteps:        buildNextSteps(resp.Evidence, hints.TextQuery),
	}

	if includeTopic {
		graph, err := topics.Build(ctx, b.st, topic, topics.Options{
			SourceTypes:  opts.SourceTypes,
			SeedLimit:    opts.SeedLimit,
			RelatedLimit: defaultInt(opts.RelatedLimit, 2),
		})
		if err != nil {
			return Pack{}, err
		}
		pack.Mode = "topic_brief_and_evidence"
		pack.Topic = topic
		pack.UsedTopicBrief = true
		pack.TopicBrief = &TopicBrief{
			Topic:        graph.Topic,
			SourceTypes:  graph.SourceTypes,
			SeedLimit:    graph.SeedLimit,
			RelatedLimit: graph.RelatedLimit,
			Summary:      topics.SummaryText(graph),
			Pivots:       graph.Pivots,
			Entities:     graph.Entities,
			Nodes:        graph.Nodes,
			Edges:        graph.Edges,
		}
		pack.Coverage.TopicNodeCount = len(graph.Nodes)
		pack.Coverage.TopicEdgeCount = len(graph.Edges)
	}
	pack.Coverage.RecallNote = recallNote(pack.Coverage)

	return pack, nil
}

func resolveTopic(question string, explicitTopic string) (string, string, bool) {
	if topic := normalizeTopicPhrase(explicitTopic); topic != "" {
		return topic, "explicit", true
	}
	if topic, ok := inferTopic(question); ok {
		return topic, "inferred", true
	}
	return "", "", false
}

func inferTopic(question string) (string, bool) {
	q := strings.TrimSpace(question)
	if q == "" {
		return "", false
	}
	lower := strings.ToLower(q)
	switch {
	case hasAnyPrefix(lower,
		"what do i have in my brain about ",
		"what do we have in my brain about ",
		"what do i have saved about ",
		"what do we have saved about ",
		"what do i have about ",
		"what do we have about ",
		"what do i know about ",
		"what do we know about ",
		"what do you know about ",
		"what is in my brain about ",
		"what's in my brain about ",
		"what does my brain know about ",
		"what does dbrain know about ",
		"what does the brain know about ",
		"ask my brain about ",
		"use my brain to research ",
		"use my brain for ",
		"research "):
		return normalizeTopicPhrase(trimQuestionPrefix(lower,
			"what do i have in my brain about ",
			"what do we have in my brain about ",
			"what do i have saved about ",
			"what do we have saved about ",
			"what do i have about ",
			"what do we have about ",
			"what do i know about ",
			"what do we know about ",
			"what do you know about ",
			"what is in my brain about ",
			"what's in my brain about ",
			"what does my brain know about ",
			"what does dbrain know about ",
			"what does the brain know about ",
			"ask my brain about ",
			"use my brain to research ",
			"use my brain for ",
			"research ")), true
	case strings.HasPrefix(lower, "what is "),
		strings.HasPrefix(lower, "what are "),
		strings.HasPrefix(lower, "explain "),
		strings.HasPrefix(lower, "tell me about "),
		strings.HasPrefix(lower, "overview of "),
		strings.HasPrefix(lower, "give me an overview of "):
		return normalizeTopicPhrase(trimQuestionPrefix(lower,
			"what is ", "what are ", "explain ", "tell me about ", "overview of ", "give me an overview of ")), true
	case (strings.HasPrefix(lower, "show me ") || strings.HasPrefix(lower, "find me ")) &&
		(strings.Contains(lower, " about ") || strings.Contains(lower, " for ")):
		if topic := topicAfterPreposition(lower); topic != "" {
			return topic, true
		}
	case strings.HasSuffix(lower, "?") && (strings.HasPrefix(lower, "how ") || strings.HasPrefix(lower, "why ")):
		terms := queryterms.Terms(lower)
		if len(terms) >= 2 {
			return normalizeTopicPhrase(strings.Join(terms[:min(len(terms), 4)], " ")), true
		}
	}
	return "", false
}

func hasAnyPrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func trimQuestionPrefix(value string, prefixes ...string) string {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimSpace(value[len(prefix):])
		}
	}
	return strings.TrimSpace(value)
}

func topicAfterPreposition(value string) string {
	for _, marker := range []string{" about ", " for "} {
		if idx := strings.Index(value, marker); idx >= 0 {
			return normalizeTopicPhrase(value[idx+len(marker):])
		}
	}
	return ""
}

func normalizeTopicPhrase(value string) string {
	value = strings.TrimSpace(strings.TrimRight(value, "?.!"))
	terms := queryterms.Terms(value)
	if len(terms) == 0 {
		return ""
	}
	if len(terms) > 5 {
		terms = terms[:5]
	}
	return strings.Join(terms, " ")
}

func buildCoverage(evidence []ask.Evidence) Coverage {
	return Coverage{
		EvidenceCount: len(evidence),
		ByKind:        countValues(evidence, func(doc ask.Evidence) string { return doc.Kind }, 10),
		BySourceType:  countValues(evidence, func(doc ask.Evidence) string { return doc.SourceType }, 10),
		TopUserTags:   topTags(evidence, 12),
	}
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
	retrieval := exactTagExampleRetrieval(tag, terms, matchText)
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
		Retrieval:   &retrieval,
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
	retrieval := exactTagExampleRetrieval(tag, terms, matchText)
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
		Retrieval:    &retrieval,
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

func buildNextSteps(evidence []ask.Evidence, query string) []SuggestedAction {
	if len(evidence) == 0 {
		return nil
	}
	query = strings.TrimSpace(query)
	steps := make([]SuggestedAction, 0, 2)
	if len(evidence) > 1 {
		lookups := make([]string, 0, min(len(evidence), 5))
		for _, doc := range evidence {
			if strings.TrimSpace(doc.SourceKey) == "" {
				continue
			}
			lookups = append(lookups, doc.SourceKey)
			if len(lookups) >= 5 {
				break
			}
		}
		if len(lookups) > 0 {
			params := map[string]interface{}{
				"lookups":      lookups,
				"content_mode": "evidence",
			}
			if query != "" {
				params["query"] = query
			}
			steps = append(steps, SuggestedAction{
				Action: "inspect_top_evidence",
				Label:  "Inspect top evidence",
				Reason: "Inspect the strongest evidence notes before making detailed claims.",
				Params: params,
			})
		}
	} else {
		params := map[string]interface{}{
			"lookup":       evidence[0].SourceKey,
			"content_mode": "evidence",
		}
		if query != "" {
			params["query"] = query
		}
		steps = append(steps, SuggestedAction{
			Action: "inspect_top_evidence",
			Label:  "Inspect top evidence",
			Reason: "Inspect the strongest evidence note before making detailed claims.",
			Params: params,
		})
	}
	for _, doc := range evidence {
		if doc.Kind == "item" || doc.Kind == "source" {
			steps = append(steps, SuggestedAction{
				Action: "expand_related",
				Label:  "Expand related context",
				Reason: "Follow linked sources or backlinks around a high-signal evidence node.",
				Params: map[string]interface{}{
					"lookup": doc.SourceKey,
				},
			})
			break
		}
	}
	return steps
}

func filterSearchResults(ctx context.Context, st *store.Store, results []model.SearchResult, sourceTypes []string) []model.SearchResult {
	if len(sourceTypes) == 0 {
		return results
	}
	filtered := make([]model.SearchResult, 0, len(results))
	for _, result := range results {
		if item, err := st.GetItem(ctx, result.SourceKey); err == nil {
			if matchesSourceTypes(sourceTypes, item.SourceType) {
				filtered = append(filtered, result)
			}
			continue
		}
		if source, err := st.GetSource(ctx, result.SourceKey); err == nil {
			if matchesSourceTypes(sourceTypes, source.SourceType) {
				filtered = append(filtered, result)
			}
		}
	}
	return filtered
}

func matchesSourceTypes(filters []string, sourceType string) bool {
	if len(filters) == 0 {
		return true
	}
	sourceType = strings.TrimSpace(strings.ToLower(sourceType))
	family := sourceTypeFamily(sourceType)
	for _, filter := range filters {
		filter = strings.TrimSpace(strings.ToLower(filter))
		if filter == "" {
			continue
		}
		if filter == sourceType || filter == family {
			return true
		}
	}
	return false
}

func sourceTypeFamily(value string) string {
	if idx := strings.IndexByte(value, '_'); idx > 0 {
		return value[:idx]
	}
	return value
}

func tagAlias(value string) string {
	terms := queryterms.Terms(value)
	if len(terms) < 2 {
		return ""
	}
	return strings.Join(terms, "-")
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func defaultInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func trimText(value string, maxChars int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" || maxChars <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return strings.TrimSpace(string(runes[:maxChars])) + "..."
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
