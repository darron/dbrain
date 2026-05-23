package brainresearch

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/summarizecli"
	"github.com/darron/dbrain/internal/summaryconfig"
)

const (
	researchPlannerPromptVersion = "brain-research-planner-v1"
	defaultPlannerTimeout        = 20 * time.Second
	maxPlannerConcepts           = 8
	maxPlannerConceptTerms       = 8
	maxPlannerVariants           = 8
	maxPlannerQueryChars         = 120
)

const researchPlannerPrompt = `You are a query planner for a private local second-brain search index.
Return compact JSON only. Do not answer the user's question.

Build a retrieval plan that helps find relevant saved notes, links, titles, summaries, transcripts, and OCR text.
Use general language knowledge to expand abbreviations, synonyms, title-like phrasings, and likely alternate wording.
Do not invent facts about the user's corpus.
Keep variants short enough for keyword search.

JSON schema:
{
  "concepts": [
    {"key":"canonical_concept", "preferred":"best_search_term", "terms":["alias", "alternate phrase"], "required":true}
  ],
  "query_variants": [
    {"query":"short keyword query", "reason":"why this variant helps"}
  ]
}

Rules:
- concepts must be semantic constraints from the question, not filler words.
- required=false only for numeric counts, weak modifiers, or optional context.
- include abbreviations and expansions when useful, e.g. k8s/kubernetes.
- include title-like variants for news/event questions.
- include product/project/repository names exactly when present.
- return at most 8 concepts and 8 query_variants.`

type modelResearchPlan struct {
	Concepts      []QueryConcept `json:"concepts"`
	QueryVariants []QueryVariant `json:"query_variants"`
}

func (p modelResearchPlan) Empty() bool {
	return len(p.Concepts) == 0 && len(p.QueryVariants) == 0
}

func (b *Builder) buildModelResearchPlan(ctx context.Context, question string, hints ask.QueryHints, deterministic researchStrategy, opts Options) (modelResearchPlan, string, error) {
	modelName := summaryconfig.Model(b.cfg.RootDir, opts.PlannerModel)
	if strings.TrimSpace(modelName) == "" {
		return modelResearchPlan{}, "", nil
	}
	timeout := opts.PlannerTimeout
	if timeout <= 0 {
		timeout = defaultPlannerTimeout
	}

	inputFile, err := b.cfg.CreateTemp("dbrain-research-planner-*.md")
	if err != nil {
		return modelResearchPlan{}, modelName, fmt.Errorf("create planner input: %w", err)
	}
	inputPath := inputFile.Name()
	defer func() {
		_ = os.Remove(inputPath)
	}()
	input := plannerInput(question, hints, deterministic)
	emitPlannerInput(opts.Observer, input)
	emitEvent(opts.Observer, "planner_requested", map[string]interface{}{
		"prompt_version": researchPlannerPromptVersion,
		"model":          modelName,
		"timeout_ms":     timeout.Milliseconds(),
		"input_chars":    len(input),
	})
	if _, err := inputFile.WriteString(input); err != nil {
		_ = inputFile.Close()
		return modelResearchPlan{}, modelName, fmt.Errorf("write planner input: %w", err)
	}
	if err := inputFile.Close(); err != nil {
		return modelResearchPlan{}, modelName, fmt.Errorf("close planner input: %w", err)
	}

	result, err := summarizecli.Run(ctx, summarizecli.Options{
		Binary:    opts.PlannerBinary,
		Input:     inputPath,
		Summarize: true,
		Model:     modelName,
		Prompt:    researchPlannerPrompt,
		Length:    "short",
		Language:  "auto",
		Timeout:   timeout,
		RootDir:   b.cfg.RootDir,
	})
	if err != nil {
		return modelResearchPlan{}, modelName, fmt.Errorf("model planner %s: %w", researchPlannerPromptVersion, err)
	}
	emitPlannerOutput(opts.Observer, firstNonEmpty(result.Summary.RawJSON, result.Summary.Text, result.Extract.RawJSON))
	emitEvent(opts.Observer, "planner_returned", map[string]interface{}{
		"prompt_version": researchPlannerPromptVersion,
		"model":          firstNonEmpty(result.Summary.Model, modelName),
		"tool":           result.Summary.Tool,
		"tool_version":   result.Summary.ToolVersion,
		"output_chars":   len(firstNonEmpty(result.Summary.RawJSON, result.Summary.Text, result.Extract.RawJSON)),
	})
	if result.Summary.Status != "ok" || strings.TrimSpace(result.Summary.Text) == "" {
		return modelResearchPlan{}, modelName, fmt.Errorf("model planner %s returned no plan", researchPlannerPromptVersion)
	}
	plan, err := parseModelResearchPlan(result.Summary.Text)
	if err != nil {
		return modelResearchPlan{}, modelName, fmt.Errorf("model planner %s: %w", researchPlannerPromptVersion, err)
	}
	return plan, modelName, nil
}

func plannerInput(question string, hints ask.QueryHints, deterministic researchStrategy) string {
	var b strings.Builder
	b.WriteString("# Research Question\n")
	b.WriteString(strings.TrimSpace(question))
	b.WriteString("\n\n# Deterministic Retrieval Seed\n")
	b.WriteString("- text_query: ")
	b.WriteString(hints.TextQuery)
	b.WriteString("\n- terms: ")
	b.WriteString(strings.Join(hints.Terms, ", "))
	if len(deterministic.Concepts) > 0 {
		b.WriteString("\n- existing_concepts:")
		for _, concept := range deterministic.Concepts {
			b.WriteString("\n  - ")
			b.WriteString(concept.Key)
			b.WriteString(": ")
			b.WriteString(strings.Join(concept.Terms, ", "))
		}
	}
	if len(deterministic.Variants) > 0 {
		b.WriteString("\n- existing_variants:")
		for _, variant := range deterministic.Variants {
			b.WriteString("\n  - ")
			b.WriteString(variant.Query)
		}
	}
	b.WriteString("\n")
	return b.String()
}
