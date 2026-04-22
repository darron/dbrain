package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dbrain/internal/ask"
	"dbrain/internal/topics"
)

type researchPack struct {
	Question       string                 `json:"question"`
	Mode           string                 `json:"mode"`
	Topic          string                 `json:"topic,omitempty"`
	UsedTopicBrief bool                   `json:"used_topic_brief"`
	Evidence       []ask.Evidence         `json:"evidence"`
	TopicBrief     map[string]interface{} `json:"topic_brief,omitempty"`
}

type researchPackOptions struct {
	Question       string
	Limit          int
	SourceTypes    []string
	IncludeRelated bool
	RelatedLimit   int
	SeedLimit      int
}

func (s *Server) toolResearchPack(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Question       string   `json:"question"`
		Limit          int      `json:"limit"`
		SourceTypes    []string `json:"source_types"`
		IncludeRelated bool     `json:"include_related"`
		RelatedLimit   int      `json:"related_limit"`
		SeedLimit      int      `json:"seed_limit"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode research pack args: %w", err)
	}

	pack, err := s.buildResearchPack(ctx, researchPackOptions{
		Question:       args.Question,
		Limit:          args.Limit,
		SourceTypes:    args.SourceTypes,
		IncludeRelated: args.IncludeRelated,
		RelatedLimit:   args.RelatedLimit,
		SeedLimit:      args.SeedLimit,
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

	resp, err := ask.Run(ctx, s.cfg, s.st, question, ask.Options{
		Limit:          defaultInt(opts.Limit, 8),
		RetrieveOnly:   true,
		SourceTypes:    opts.SourceTypes,
		IncludeRelated: opts.IncludeRelated,
		RelatedLimit:   opts.RelatedLimit,
	})
	if err != nil {
		return researchPack{}, err
	}

	pack := researchPack{
		Question: question,
		Mode:     "evidence_only",
		Evidence: resp.Evidence,
	}

	if topic, ok := inferResearchTopic(question); ok {
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
	if strings.TrimSpace(pack.Topic) != "" {
		b.WriteString("Topic: ")
		b.WriteString(pack.Topic)
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
		}
	}
	return strings.TrimSpace(b.String())
}

func inferResearchTopic(question string) (string, bool) {
	q := strings.TrimSpace(question)
	if q == "" {
		return "", false
	}
	lower := strings.ToLower(q)
	switch {
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
