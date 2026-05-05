package brainresearch

import (
	"fmt"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/ask"
)

type synthesisInputBuilder struct {
	pack       Pack
	question   string
	budget     int
	used       int
	truncation TruncationMetadata
	citations  []Citation
	seen       map[string]struct{}
}

func (b *synthesisInputBuilder) build() string {
	b.truncation.EvidenceBudgetChars = b.budget
	var out strings.Builder
	out.WriteString("# dbrain Research Synthesis Input\n\n")
	out.WriteString("## Question\n")
	out.WriteString(b.question)
	out.WriteString("\n\n")
	out.WriteString("## Query Plan\n")
	out.WriteString("- text_query: ")
	out.WriteString(b.pack.QueryPlan.TextQuery)
	out.WriteString("\n- query_terms: ")
	out.WriteString(strings.Join(b.pack.QueryPlan.QueryTerms, ", "))
	out.WriteString("\n- tag_queries: ")
	out.WriteString(strings.Join(b.pack.QueryPlan.TagQueries, ", "))
	if strings.TrimSpace(b.pack.QueryPlan.Planner) != "" {
		out.WriteString("\n- planner: ")
		out.WriteString(b.pack.QueryPlan.Planner)
	}
	if strings.TrimSpace(b.pack.QueryPlan.PlannerModel) != "" {
		out.WriteString("\n- planner_model: ")
		out.WriteString(b.pack.QueryPlan.PlannerModel)
	}
	if strings.TrimSpace(b.pack.QueryPlan.PlannerError) != "" {
		out.WriteString("\n- planner_error: ")
		out.WriteString(b.pack.QueryPlan.PlannerError)
	}
	if len(b.pack.QueryPlan.QueryVariants) > 0 {
		out.WriteString("\n- query_variants:")
		for _, variant := range b.pack.QueryPlan.QueryVariants {
			out.WriteString("\n  - ")
			out.WriteString(variant.Query)
			if strings.TrimSpace(variant.Reason) != "" {
				out.WriteString(" (")
				out.WriteString(variant.Reason)
				out.WriteString(")")
			}
		}
	}
	if len(b.pack.QueryPlan.Concepts) > 0 {
		out.WriteString("\n- required_concepts:")
		for _, concept := range b.pack.QueryPlan.Concepts {
			out.WriteString("\n  - ")
			out.WriteString(concept.Key)
			if !concept.Required {
				out.WriteString(" (optional)")
			}
			out.WriteString(": ")
			out.WriteString(strings.Join(concept.Terms, ", "))
		}
	}
	out.WriteString("\n\n")
	out.WriteString("## Coverage\n")
	out.WriteString("- evidence_count: ")
	_, _ = fmt.Fprintf(&out, "%d", b.pack.Coverage.EvidenceCount)
	out.WriteString("\n- recall_note: ")
	out.WriteString(b.pack.Coverage.RecallNote)
	out.WriteString("\n\n")

	primary, related := splitPrimaryAndRelated(b.pack.Evidence)
	b.appendLane(&out, "Primary Evidence", primary, 0)
	b.appendLane(&out, "Exact Tag Evidence", b.pack.ExactTagEvidence, defaultExactTagReservedChars)
	b.appendTopicBrief(&out)
	b.appendLane(&out, "Related Evidence", related, 0)
	b.truncation.EvidenceCharsUsed = b.used
	sort.Strings(b.truncation.DroppedSourceKeys)
	return out.String()
}

func (b *synthesisInputBuilder) appendTopicBrief(out *strings.Builder) {
	if b.pack.TopicBrief == nil {
		return
	}
	remaining := b.budget - b.used
	if remaining < defaultTopicBriefMinRemaining {
		b.truncation.DroppedSourceKeys = appendUnique(b.truncation.DroppedSourceKeys, "topic_brief")
		return
	}
	summary := trimRunes(b.pack.TopicBrief.Summary, min(defaultTopicBriefSummaryMaxChars, remaining))
	if strings.TrimSpace(summary) == "" {
		return
	}
	chunk := "## Topic Brief\n" + summary + "\n\n"
	if b.tryAppend(out, "topic_brief", chunk, false) {
		return
	}
	b.truncation.DroppedSourceKeys = appendUnique(b.truncation.DroppedSourceKeys, "topic_brief")
}

func (b *synthesisInputBuilder) appendLane(out *strings.Builder, title string, docs []ask.Evidence, reserve int) {
	if len(docs) == 0 {
		return
	}
	out.WriteString("## ")
	out.WriteString(title)
	out.WriteString("\n")
	wroteAny := false
	for _, doc := range docs {
		chunk := evidenceChunk(doc)
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		forceOne := reserve > 0 && !wroteAny && b.budget-b.used > 0
		if b.tryAppend(out, doc.SourceKey, chunk, forceOne) {
			b.addCitation(doc)
			wroteAny = true
			continue
		}
		b.truncation.DroppedSourceKeys = appendUnique(b.truncation.DroppedSourceKeys, doc.SourceKey)
	}
	out.WriteString("\n")
}

func (b *synthesisInputBuilder) tryAppend(out *strings.Builder, sourceKey string, chunk string, forcePartial bool) bool {
	remaining := b.budget - b.used
	if remaining <= 0 {
		return false
	}
	size := len([]rune(chunk))
	if size <= remaining {
		out.WriteString(chunk)
		b.used += size
		return true
	}
	if b.truncation.PartiallyTrimmedSourceKey != "" && !forcePartial {
		return false
	}
	if remaining < 200 && !forcePartial {
		return false
	}
	trimmed := trimRunes(chunk, remaining)
	if strings.TrimSpace(trimmed) == "" {
		return false
	}
	out.WriteString(trimmed)
	out.WriteString("\n[trimmed]\n")
	b.used += len([]rune(trimmed))
	b.truncation.PartiallyTrimmedSourceKey = sourceKey
	return true
}

func (b *synthesisInputBuilder) addCitation(doc ask.Evidence) {
	if strings.TrimSpace(doc.SourceKey) == "" {
		return
	}
	if _, ok := b.seen[doc.SourceKey]; ok {
		return
	}
	b.seen[doc.SourceKey] = struct{}{}
	b.citations = append(b.citations, Citation{
		SourceKey: doc.SourceKey,
		Title:     doc.Title,
		URL:       doc.URL,
		NotePath:  doc.NotePath,
		Kind:      doc.Kind,
	})
}

func (b *synthesisInputBuilder) warnings() []string {
	if len(b.truncation.DroppedSourceKeys) > 0 || b.truncation.PartiallyTrimmedSourceKey != "" {
		return []string{"evidence_truncated"}
	}
	return nil
}

func splitPrimaryAndRelated(docs []ask.Evidence) ([]ask.Evidence, []ask.Evidence) {
	primary := make([]ask.Evidence, 0, len(docs))
	related := make([]ask.Evidence, 0)
	for _, doc := range docs {
		if strings.TrimSpace(doc.RelatedTo) != "" || strings.TrimSpace(doc.Relationship) != "" {
			related = append(related, doc)
		} else {
			primary = append(primary, doc)
		}
	}
	return primary, related
}

func evidenceChunk(doc ask.Evidence) string {
	text := strings.TrimSpace(doc.Summary)
	textKind := "summary"
	if text == "" {
		text = strings.TrimSpace(doc.Excerpt)
		textKind = "excerpt"
	}
	if text == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("- source_key: ")
	b.WriteString(doc.SourceKey)
	b.WriteString("\n  title: ")
	b.WriteString(doc.Title)
	b.WriteString("\n  kind: ")
	b.WriteString(doc.Kind)
	if doc.SourceType != "" {
		b.WriteString("\n  source_type: ")
		b.WriteString(doc.SourceType)
	}
	if doc.URL != "" {
		b.WriteString("\n  url: ")
		b.WriteString(doc.URL)
	}
	if doc.NotePath != "" {
		b.WriteString("\n  note_path: ")
		b.WriteString(doc.NotePath)
	}
	if doc.UserTags != "" {
		b.WriteString("\n  user_tags: ")
		b.WriteString(doc.UserTags)
	}
	if doc.Relationship != "" {
		b.WriteString("\n  relationship: ")
		b.WriteString(doc.Relationship)
		if doc.RelatedTo != "" {
			b.WriteString(" (")
			b.WriteString(doc.RelatedTo)
			b.WriteString(")")
		}
	}
	b.WriteString("\n  ")
	b.WriteString(textKind)
	b.WriteString(": |\n")
	for _, line := range strings.Split(text, "\n") {
		b.WriteString("    ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
