package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
)

type ResearchPack = brainresearch.Pack
type ResearchPackOptions = brainresearch.Options
type researchBucket = brainresearch.Bucket

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

	pack, err := s.BuildResearchPack(ctx, ResearchPackOptions{
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

func (s *Server) BuildResearchPack(ctx context.Context, opts ResearchPackOptions) (ResearchPack, error) {
	return brainresearch.Build(ctx, s.cfg, s.st, opts)
}

func formatResearchPack(pack brainresearch.Pack) string {
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
	if pack.TopicBrief != nil && strings.TrimSpace(pack.TopicBrief.Summary) != "" {
		b.WriteString("\nTopic brief:\n")
		b.WriteString(strings.TrimSpace(pack.TopicBrief.Summary))
		b.WriteString("\n")
	}
	b.WriteString("\nEvidence count: ")
	_, _ = fmt.Fprintf(&b, "%d", len(pack.Evidence))
	if len(pack.Evidence) > 0 {
		b.WriteString("\n")
		for _, doc := range pack.Evidence {
			writeResearchEvidenceLine(&b, doc, 280)
		}
	}
	if len(pack.ExactTagEvidence) > 0 {
		b.WriteString("\nExact tag examples:\n")
		for _, doc := range pack.ExactTagEvidence {
			writeResearchEvidenceLine(&b, doc, 220)
		}
	}
	if len(pack.NextSteps) > 0 {
		b.WriteString("\nSuggested next actions:\n")
		for _, step := range pack.NextSteps {
			b.WriteString("- ")
			b.WriteString(step.Action)
			if strings.TrimSpace(step.Label) != "" {
				b.WriteString(" (")
				b.WriteString(step.Label)
				b.WriteString(")")
			}
			if strings.TrimSpace(step.Reason) != "" {
				b.WriteString(": ")
				b.WriteString(step.Reason)
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func writeResearchEvidenceLine(b *strings.Builder, doc ask.Evidence, maxChars int) {
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
	if text := trimResearchText(firstNonEmpty(doc.Summary, doc.Excerpt), maxChars); text != "" {
		b.WriteString("  Evidence: ")
		b.WriteString(text)
		b.WriteString("\n")
	}
}

func formatResearchBuckets(buckets []brainresearch.Bucket, limit int) string {
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
