package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"dbrain/internal/ask"
	"dbrain/internal/topics"
)

type researchPack struct {
	Question       string                    `json:"question"`
	Mode           string                    `json:"mode"`
	QueryPlan      researchQueryPlan         `json:"query_plan"`
	Coverage       researchCoverage          `json:"coverage"`
	Topic          string                    `json:"topic,omitempty"`
	UsedTopicBrief bool                      `json:"used_topic_brief"`
	Evidence       []ask.Evidence            `json:"evidence"`
	TopicBrief     map[string]interface{}    `json:"topic_brief,omitempty"`
	NextSteps      []researchSuggestedAction `json:"next_steps,omitempty"`
}

type researchPackOptions struct {
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

type researchQueryPlan struct {
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

type researchCoverage struct {
	EvidenceCount     int              `json:"evidence_count"`
	ByKind            []researchBucket `json:"by_kind"`
	BySourceType      []researchBucket `json:"by_source_type"`
	TopUserTags       []researchBucket `json:"top_user_tags,omitempty"`
	ExactTagMatches   []researchBucket `json:"exact_tag_matches,omitempty"`
	ItemTextMatches   int              `json:"item_text_matches,omitempty"`
	SourceTextMatches int              `json:"source_text_matches,omitempty"`
	TopicNodeCount    int              `json:"topic_node_count,omitempty"`
	TopicEdgeCount    int              `json:"topic_edge_count,omitempty"`
	DisplayedLimit    int              `json:"displayed_limit"`
	RelatedLimit      int              `json:"related_limit,omitempty"`
	RecallNote        string           `json:"recall_note,omitempty"`
}

type researchBucket struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type researchSuggestedAction struct {
	Tool      string                 `json:"tool"`
	Reason    string                 `json:"reason"`
	Arguments map[string]interface{} `json:"arguments"`
}

func (s *Server) toolResearchPack(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Question          string   `json:"question"`
		Topic             string   `json:"topic"`
		Limit             int      `json:"limit"`
		SourceTypes       []string `json:"source_types"`
		IncludeRelated    bool     `json:"include_related"`
		RelatedLimit      int      `json:"related_limit"`
		SeedLimit         int      `json:"seed_limit"`
		IncludeTopicBrief *bool    `json:"include_topic_brief"`
		MaxCharsPerDoc    int      `json:"max_chars_per_doc"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode research pack args: %w", err)
	}

	pack, err := s.buildResearchPack(ctx, researchPackOptions{
		Question:       args.Question,
		Topic:          args.Topic,
		Limit:          args.Limit,
		SourceTypes:    args.SourceTypes,
		IncludeRelated: args.IncludeRelated,
		RelatedLimit:   args.RelatedLimit,
		SeedLimit:      args.SeedLimit,
		IncludeTopic:   args.IncludeTopicBrief,
		MaxCharsPerDoc: args.MaxCharsPerDoc,
	})
	if err != nil {
		return nil, err
	}

	return toolOKResult(formatResearchPack(pack), pack), nil
}

func (s *Server) buildResearchPack(ctx context.Context, opts researchPackOptions) (researchPack, error) {
	question := strings.TrimSpace(opts.Question)
	if question == "" {
		return researchPack{}, fmt.Errorf("question is required")
	}

	hints := ask.Hints(question)
	limit := defaultInt(opts.Limit, 8)
	maxChars := defaultInt(opts.MaxCharsPerDoc, 700)
	topic, topicSource, hasTopic := resolveResearchTopic(question, opts.Topic)
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

	resp, err := ask.Run(ctx, s.cfg, s.st, question, ask.Options{
		Limit:          limit,
		RetrieveOnly:   true,
		SourceTypes:    opts.SourceTypes,
		IncludeRelated: opts.IncludeRelated,
		RelatedLimit:   opts.RelatedLimit,
		MaxCharsPerDoc: maxChars,
	})
	if err != nil {
		return researchPack{}, err
	}

	corpusCoverage, err := s.buildResearchCorpusCoverage(ctx, topic, hints, opts.SourceTypes, limit, opts.RelatedLimit)
	if err != nil {
		return researchPack{}, err
	}

	pack := researchPack{
		Question: question,
		Mode:     "evidence_only",
		QueryPlan: researchQueryPlan{
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
		Evidence:  resp.Evidence,
		Coverage:  mergeResearchCoverage(buildResearchCoverage(resp.Evidence), corpusCoverage),
		NextSteps: buildResearchNextSteps(resp.Evidence),
	}

	if includeTopic {
		graph, err := topics.Build(ctx, s.st, topic, topics.Options{
			SourceTypes:  opts.SourceTypes,
			SeedLimit:    opts.SeedLimit,
			RelatedLimit: defaultInt(opts.RelatedLimit, 2),
		})
		if err != nil {
			return researchPack{}, err
		}
		pack.Mode = "topic_brief_and_evidence"
		pack.Topic = topic
		pack.UsedTopicBrief = true
		pack.TopicBrief = map[string]interface{}{
			"topic":         graph.Topic,
			"source_types":  graph.SourceTypes,
			"seed_limit":    graph.SeedLimit,
			"related_limit": graph.RelatedLimit,
			"summary":       topics.SummaryText(graph),
			"pivots":        graph.Pivots,
			"entities":      graph.Entities,
			"nodes":         graph.Nodes,
			"edges":         graph.Edges,
		}
		pack.Coverage.TopicNodeCount = len(graph.Nodes)
		pack.Coverage.TopicEdgeCount = len(graph.Edges)
	}
	pack.Coverage.RecallNote = researchRecallNote(pack.Coverage)

	return pack, nil
}

func formatResearchPack(pack researchPack) string {
	var b strings.Builder
	b.WriteString("Research pack for: ")
	b.WriteString(pack.Question)
	b.WriteString("\n")
	b.WriteString("Mode: ")
	b.WriteString(pack.Mode)
	b.WriteString("\n")
	if strings.TrimSpace(pack.QueryPlan.TextQuery) != "" {
		b.WriteString("Text query: ")
		b.WriteString(pack.QueryPlan.TextQuery)
		b.WriteString("\n")
	}
	if len(pack.QueryPlan.TagQueries) > 0 {
		b.WriteString("Tag aliases: ")
		b.WriteString(strings.Join(pack.QueryPlan.TagQueries, ", "))
		b.WriteString("\n")
	}
	if strings.TrimSpace(pack.Topic) != "" {
		b.WriteString("Topic: ")
		b.WriteString(pack.Topic)
		if strings.TrimSpace(pack.QueryPlan.TopicSource) != "" {
			b.WriteString(" (")
			b.WriteString(pack.QueryPlan.TopicSource)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	b.WriteString("Coverage: ")
	_, _ = fmt.Fprintf(&b, "%d evidence", pack.Coverage.EvidenceCount)
	if len(pack.Coverage.ExactTagMatches) > 0 {
		b.WriteString("; exact tags ")
		b.WriteString(formatResearchBuckets(pack.Coverage.ExactTagMatches, 4))
	}
	if pack.Coverage.ItemTextMatches > 0 || pack.Coverage.SourceTextMatches > 0 {
		_, _ = fmt.Fprintf(&b, "; text matches items=%d sources=%d", pack.Coverage.ItemTextMatches, pack.Coverage.SourceTextMatches)
	}
	if pack.Coverage.TopicNodeCount > 0 {
		_, _ = fmt.Fprintf(&b, "; topic nodes=%d", pack.Coverage.TopicNodeCount)
	}
	if len(pack.Coverage.BySourceType) > 0 {
		b.WriteString("; source types ")
		b.WriteString(formatResearchBuckets(pack.Coverage.BySourceType, 4))
	}
	if len(pack.Coverage.TopUserTags) > 0 {
		b.WriteString("; top tags ")
		b.WriteString(formatResearchBuckets(pack.Coverage.TopUserTags, 5))
	}
	b.WriteString("\n")
	if pack.Coverage.RecallNote != "" {
		b.WriteString("Recall: ")
		b.WriteString(pack.Coverage.RecallNote)
		b.WriteString("\n")
	}
	if pack.TopicBrief != nil {
		if summary, ok := pack.TopicBrief["summary"].(string); ok && strings.TrimSpace(summary) != "" {
			b.WriteString("\nTopic brief:\n")
			b.WriteString(strings.TrimSpace(summary))
			b.WriteString("\n")
		}
	}
	b.WriteString("\nEvidence count: ")
	_, _ = fmt.Fprintf(&b, "%d", len(pack.Evidence))
	if len(pack.Evidence) > 0 {
		b.WriteString("\n")
		for _, doc := range pack.Evidence {
			b.WriteString("- [")
			b.WriteString(doc.SourceKey)
			b.WriteString("] ")
			b.WriteString(doc.Title)
			if doc.Relationship != "" {
				b.WriteString(" (")
				b.WriteString(doc.Relationship)
				b.WriteString(")")
			}
			b.WriteString("\n")
			if strings.TrimSpace(doc.UserTags) != "" {
				b.WriteString("  User tags: ")
				b.WriteString(strings.TrimSpace(doc.UserTags))
				b.WriteString("\n")
			}
			if text := trimResearchText(firstNonEmpty(doc.Summary, doc.Excerpt), 280); text != "" {
				b.WriteString("  Evidence: ")
				b.WriteString(text)
				b.WriteString("\n")
			}
		}
	}
	if len(pack.NextSteps) > 0 {
		b.WriteString("\nSuggested next tools:\n")
		for _, step := range pack.NextSteps {
			b.WriteString("- ")
			b.WriteString(step.Tool)
			b.WriteString(": ")
			b.WriteString(step.Reason)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func resolveResearchTopic(question string, explicitTopic string) (string, string, bool) {
	if topic := normalizeTopicPhrase(explicitTopic); topic != "" {
		return topic, "explicit", true
	}
	if topic, ok := inferResearchTopic(question); ok {
		return topic, "inferred", true
	}
	return "", "", false
}

func inferResearchTopic(question string) (string, bool) {
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
		terms := researchQuestionTerms(lower)
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
	terms := researchQuestionTerms(value)
	if len(terms) == 0 {
		return ""
	}
	if len(terms) > 5 {
		terms = terms[:5]
	}
	return strings.Join(terms, " ")
}

func buildResearchCoverage(evidence []ask.Evidence) researchCoverage {
	return researchCoverage{
		EvidenceCount: len(evidence),
		ByKind:        countResearchValues(evidence, func(doc ask.Evidence) string { return doc.Kind }, 10),
		BySourceType:  countResearchValues(evidence, func(doc ask.Evidence) string { return doc.SourceType }, 10),
		TopUserTags:   topResearchTags(evidence, 12),
	}
}

func (s *Server) buildResearchCorpusCoverage(ctx context.Context, topic string, hints ask.QueryHints, sourceTypes []string, limit int, relatedLimit int) (researchCoverage, error) {
	coverage := researchCoverage{
		DisplayedLimit: limit,
		RelatedLimit:   relatedLimit,
	}
	tagQueries := append([]string(nil), hints.TagQueries...)
	if topicAlias := tagAlias(topic); topicAlias != "" {
		tagQueries = append(tagQueries, topicAlias)
	}
	tagQueries = uniqueStrings(tagQueries)
	for _, tagQuery := range tagQueries {
		count, err := s.st.CountExactUserTag(ctx, tagQuery, sourceTypes)
		if err != nil {
			return researchCoverage{}, err
		}
		if count > 0 {
			coverage.ExactTagMatches = append(coverage.ExactTagMatches, researchBucket{Key: tagQuery, Count: count})
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
		itemCount, err := s.st.CountItemTextMatches(ctx, textQuery, sourceTypes)
		if err != nil {
			return researchCoverage{}, err
		}
		sourceCount, err := s.st.CountSourceTextMatches(ctx, textQuery, sourceTypes)
		if err != nil {
			return researchCoverage{}, err
		}
		coverage.ItemTextMatches = itemCount
		coverage.SourceTextMatches = sourceCount
	}
	return coverage, nil
}

func mergeResearchCoverage(base researchCoverage, corpus researchCoverage) researchCoverage {
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

func researchRecallNote(coverage researchCoverage) string {
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

func countResearchValues(evidence []ask.Evidence, keyFn func(ask.Evidence) string, limit int) []researchBucket {
	counts := map[string]int{}
	for _, doc := range evidence {
		key := strings.TrimSpace(keyFn(doc))
		if key == "" {
			key = "unknown"
		}
		counts[key]++
	}
	return orderedResearchBuckets(counts, limit)
}

func topResearchTags(evidence []ask.Evidence, limit int) []researchBucket {
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
	return orderedResearchBuckets(counts, limit)
}

func orderedResearchBuckets(counts map[string]int, limit int) []researchBucket {
	buckets := make([]researchBucket, 0, len(counts))
	for key, count := range counts {
		buckets = append(buckets, researchBucket{Key: key, Count: count})
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

func buildResearchNextSteps(evidence []ask.Evidence) []researchSuggestedAction {
	if len(evidence) == 0 {
		return nil
	}
	steps := make([]researchSuggestedAction, 0, 2)
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
			steps = append(steps, researchSuggestedAction{
				Tool:   "dbrain_get_many",
				Reason: "Inspect the strongest evidence notes in one MCP call before making detailed claims.",
				Arguments: map[string]interface{}{
					"lookups":      lookups,
					"content_mode": "evidence",
				},
			})
		}
	} else {
		steps = append(steps, researchSuggestedAction{
			Tool:   "dbrain_get",
			Reason: "Inspect the strongest evidence note before making detailed claims.",
			Arguments: map[string]interface{}{
				"lookup":       evidence[0].SourceKey,
				"content_mode": "evidence",
			},
		})
	}
	for _, doc := range evidence {
		if doc.Kind == "item" || doc.Kind == "source" {
			steps = append(steps, researchSuggestedAction{
				Tool:   "dbrain_related",
				Reason: "Follow linked sources or backlinks around a high-signal evidence node.",
				Arguments: map[string]interface{}{
					"lookup": doc.SourceKey,
				},
			})
			break
		}
	}
	return steps
}

func formatResearchBuckets(buckets []researchBucket, limit int) string {
	if limit > 0 && len(buckets) > limit {
		buckets = buckets[:limit]
	}
	parts := make([]string, 0, len(buckets))
	for _, bucket := range buckets {
		parts = append(parts, fmt.Sprintf("%s=%d", bucket.Key, bucket.Count))
	}
	return strings.Join(parts, ", ")
}

func trimResearchText(value string, maxChars int) string {
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

func researchQuestionTerms(value string) []string {
	stopwords := map[string]struct{}{
		"a": {}, "about": {}, "an": {}, "and": {}, "are": {}, "brain": {}, "can": {}, "dbrain": {}, "do": {}, "does": {}, "for": {},
		"evidence": {}, "find": {}, "give": {}, "have": {}, "how": {}, "i": {}, "if": {}, "in": {}, "include": {}, "is": {}, "know": {}, "me": {}, "my": {}, "of": {}, "on": {},
		"overview": {}, "present": {}, "related": {}, "saved": {}, "show": {}, "tag": {}, "tags": {}, "tell": {}, "the": {}, "to": {}, "use": {}, "using": {}, "we": {}, "what": {}, "why": {}, "you": {}, "your": {},
	}
	replacer := strings.NewReplacer("-", " ", "_", " ", "?", " ", ".", " ", ",", " ", ":", " ", ";", " ", "\"", " ", "'", " ")
	parts := strings.Fields(replacer.Replace(strings.ToLower(strings.TrimSpace(value))))
	seen := map[string]struct{}{}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if _, skip := stopwords[part]; skip {
			continue
		}
		if len(part) < 2 {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}

func tagAlias(value string) string {
	terms := researchQuestionTerms(value)
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
