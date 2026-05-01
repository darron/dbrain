package brainresearch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/summarizecli"
	"github.com/darron/dbrain/internal/summaryconfig"
)

const (
	SynthesisSchemaVersion           = "research_synthesis.v1"
	SynthesisPromptVersion           = "brain-research-synthesis-v1"
	DefaultMaxEvidenceChars          = 24000
	defaultExactTagReservedChars     = 2000
	defaultTopicBriefMinRemaining    = 2000
	defaultTopicBriefSummaryMaxChars = 2000
)

const synthesisPrompt = `Answer this question from the provided dbrain research pack only.
Do not use outside knowledge.
Cite each material claim with exact source keys from the research pack in brackets, such as [src:...], [x:...], [apple-note:...], or [gh-star:...].
Do not add, remove, shorten, or rewrite source key prefixes.
Include a short Sources section with source keys and note paths.
If evidence is weak, partial, list-like, or missing, say so plainly.
Distinguish user-authored notes from linked third-party sources when the evidence marks that difference.
Distinguish summaries, excerpts, transcripts, OCR, raw notes, and archived web extracts when relevant.
Keep the answer concise and useful.`

var ErrSynthesisUnavailable = errors.New("synthesis model is not configured")

type SynthesisOptions struct {
	Question         string
	Pack             Pack
	Model            string
	CLI              string
	Length           string
	Timeout          time.Duration
	MaxEvidenceChars int
	Binary           string
}

type PreparedSynthesis struct {
	SchemaVersion string             `json:"schema_version"`
	Question      string             `json:"question"`
	Model         string             `json:"model"`
	PromptVersion string             `json:"prompt_version"`
	Input         string             `json:"-"`
	Truncation    TruncationMetadata `json:"truncation"`
	Citations     []Citation         `json:"citations,omitempty"`
	Warnings      []string           `json:"answer_warnings,omitempty"`
	Status        string             `json:"answer_status"`
}

type SynthesisResult struct {
	SchemaVersion string             `json:"schema_version"`
	Question      string             `json:"question"`
	Answer        string             `json:"answer"`
	AnswerStatus  string             `json:"answer_status"`
	Warnings      []string           `json:"answer_warnings,omitempty"`
	Truncation    TruncationMetadata `json:"truncation"`
	Citations     []Citation         `json:"citations,omitempty"`
	PromptVersion string             `json:"prompt_version"`
	Model         string             `json:"model"`
	Tool          string             `json:"tool"`
	ToolVersion   string             `json:"tool_version"`
}

type TruncationMetadata struct {
	EvidenceBudgetChars       int      `json:"evidence_budget_chars"`
	EvidenceCharsUsed         int      `json:"evidence_chars_used"`
	DroppedSourceKeys         []string `json:"dropped_source_keys,omitempty"`
	PartiallyTrimmedSourceKey string   `json:"partially_trimmed_source_key,omitempty"`
}

type Citation struct {
	SourceKey string `json:"source_key"`
	Title     string `json:"title,omitempty"`
	URL       string `json:"url,omitempty"`
	NotePath  string `json:"note_path,omitempty"`
	Kind      string `json:"kind,omitempty"`
}

func PrepareSynthesis(cfg config.Config, opts SynthesisOptions) (PreparedSynthesis, error) {
	question := strings.TrimSpace(opts.Question)
	if question == "" {
		question = strings.TrimSpace(opts.Pack.Question)
	}
	if question == "" {
		return PreparedSynthesis{}, fmt.Errorf("question is required")
	}
	if strings.TrimSpace(opts.Pack.SchemaVersion) != SchemaVersion {
		return PreparedSynthesis{}, fmt.Errorf("research_pack.schema_version must be %q", SchemaVersion)
	}

	modelName := summaryconfig.Model(cfg.RootDir, opts.Model)
	hasEvidence := len(opts.Pack.Evidence) > 0 || len(opts.Pack.ExactTagEvidence) > 0
	if hasEvidence && strings.TrimSpace(modelName) == "" {
		return PreparedSynthesis{}, fmt.Errorf("%w: set DBRAIN_SUMMARY_MODEL or pass --model", ErrSynthesisUnavailable)
	}
	budget := opts.MaxEvidenceChars
	if budget <= 0 {
		budget = DefaultMaxEvidenceChars
	}

	builder := synthesisInputBuilder{
		pack:      opts.Pack,
		question:  question,
		budget:    budget,
		citations: make([]Citation, 0, len(opts.Pack.Evidence)+len(opts.Pack.ExactTagEvidence)),
		seen:      map[string]struct{}{},
	}
	input := builder.build()
	status := "ok"
	warnings := builder.warnings()
	if len(opts.Pack.Evidence) == 0 && len(opts.Pack.ExactTagEvidence) == 0 {
		status = "no_evidence"
		warnings = appendUnique(warnings, "no_evidence")
	}

	return PreparedSynthesis{
		SchemaVersion: SynthesisSchemaVersion,
		Question:      question,
		Model:         modelName,
		PromptVersion: SynthesisPromptVersion,
		Input:         input,
		Truncation:    builder.truncation,
		Citations:     builder.citations,
		Warnings:      warnings,
		Status:        status,
	}, nil
}

func RunPreparedSynthesis(ctx context.Context, cfg config.Config, prepared PreparedSynthesis, opts SynthesisOptions) (SynthesisResult, error) {
	if prepared.Status == "no_evidence" {
		return SynthesisResult{
			SchemaVersion: SynthesisSchemaVersion,
			Question:      prepared.Question,
			AnswerStatus:  "no_evidence",
			Warnings:      prepared.Warnings,
			Truncation:    prepared.Truncation,
			Citations:     prepared.Citations,
			PromptVersion: SynthesisPromptVersion,
			Model:         prepared.Model,
		}, nil
	}

	inputFile, err := os.CreateTemp("", "dbrain-research-synthesis-*.md")
	if err != nil {
		return SynthesisResult{}, fmt.Errorf("create synthesis input: %w", err)
	}
	inputPath := inputFile.Name()
	defer func() {
		_ = os.Remove(inputPath)
	}()
	if _, err := inputFile.WriteString(prepared.Input); err != nil {
		_ = inputFile.Close()
		return SynthesisResult{}, fmt.Errorf("write synthesis input: %w", err)
	}
	if err := inputFile.Close(); err != nil {
		return SynthesisResult{}, fmt.Errorf("close synthesis input: %w", err)
	}

	runResult, err := summarizecli.Run(ctx, summarizecli.Options{
		Binary:    opts.Binary,
		Input:     inputPath,
		Summarize: true,
		Model:     prepared.Model,
		CLI:       opts.CLI,
		Prompt:    synthesisPrompt,
		Length:    defaultString(opts.Length, "medium"),
		Timeout:   defaultDuration(opts.Timeout, 2*time.Minute),
		RootDir:   cfg.RootDir,
	})
	if err != nil {
		return SynthesisResult{}, err
	}
	if runResult.Summary.Status != "ok" || strings.TrimSpace(runResult.Summary.Text) == "" {
		if runResult.Summary.Error != "" {
			return SynthesisResult{}, errors.New(runResult.Summary.Error)
		}
		return SynthesisResult{}, fmt.Errorf("synthesis returned no answer")
	}

	status := "ok"
	if hasString(prepared.Warnings, "evidence_truncated") {
		status = "ok_truncated"
	}
	return SynthesisResult{
		SchemaVersion: SynthesisSchemaVersion,
		Question:      prepared.Question,
		Answer:        strings.TrimSpace(runResult.Summary.Text),
		AnswerStatus:  status,
		Warnings:      prepared.Warnings,
		Truncation:    prepared.Truncation,
		Citations:     prepared.Citations,
		PromptVersion: SynthesisPromptVersion,
		Model:         firstNonEmpty(runResult.Summary.Model, prepared.Model),
		Tool:          runResult.Summary.Tool,
		ToolVersion:   runResult.Summary.ToolVersion,
	}, nil
}

func Synthesize(ctx context.Context, cfg config.Config, opts SynthesisOptions) (SynthesisResult, error) {
	prepared, err := PrepareSynthesis(cfg, opts)
	if err != nil {
		return SynthesisResult{}, err
	}
	return RunPreparedSynthesis(ctx, cfg, prepared, opts)
}

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

func trimRunes(value string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return string(runes[:maxChars])
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func hasString(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func defaultDuration(value time.Duration, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}
