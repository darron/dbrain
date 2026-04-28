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
	EvidenceCount int              `json:"evidence_count"`
	ByKind        []researchBucket `json:"by_kind"`
	BySourceType  []researchBucket `json:"by_source_type"`
	TopUserTags   []researchBucket `json:"top_user_tags,omitempty"`
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
		Coverage:  buildResearchCoverage(resp.Evidence),
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
	}

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
	if len(pack.Coverage.BySourceType) > 0 {
		b.WriteString("; source types ")
		b.WriteString(formatResearchBuckets(pack.Coverage.BySourceType, 4))
	}
	if len(pack.Coverage.TopUserTags) > 0 {
		b.WriteString("; top tags ")
		b.WriteString(formatResearchBuckets(pack.Coverage.TopUserTags, 5))
	}
	b.WriteString("\n")
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
		"what do i know about ",
		"what do we know about ",
		"what do you know about ",
		"what does my brain know about ",
		"what does dbrain know about ",
		"what does the brain know about ",
		"ask my brain about ",
		"use my brain to research ",
		"use my brain for ",
		"research "):
		return normalizeTopicPhrase(trimQuestionPrefix(lower,
			"what do i know about ",
			"what do we know about ",
			"what do you know about ",
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
	steps := []researchSuggestedAction{
		{
			Tool:   "dbrain_get",
			Reason: "Inspect the strongest evidence note before making detailed claims.",
			Arguments: map[string]interface{}{
				"lookup":          evidence[0].SourceKey,
				"include_content": true,
			},
		},
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
		"a": {}, "an": {}, "and": {}, "are": {}, "can": {}, "do": {}, "does": {}, "for": {},
		"find": {}, "give": {}, "how": {}, "i": {}, "is": {}, "me": {}, "of": {}, "on": {},
		"overview": {}, "show": {}, "tell": {}, "the": {}, "to": {}, "what": {}, "why": {},
	}
	replacer := strings.NewReplacer("?", " ", ".", " ", ",", " ", ":", " ", ";", " ", "\"", " ", "'", " ")
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
