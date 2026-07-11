package brainresearch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/summarizecli"
)

const synthesisPrompt = `Answer this question from the provided dbrain research pack only.
Do not use outside knowledge.
The corpus is intentionally selective: it reflects what the collector cared about or found noteworthy, not a neutral or comprehensive sample.
Do not criticize the corpus for not being unbiased or compensate by adding outside balance unless asked.
Accuracy matters more than appearing objective: separate supported facts, source claims, opinions, and uncertainty; flag weak or conflicting evidence.
Cite each material claim with exact source keys from the research pack in brackets, such as [src:...], [x:...], [apple-note:...], or [gh-star:...].
Do not add, remove, shorten, or rewrite source key prefixes.
Do not include local note paths, filesystem paths, or a separate Sources section; the UI renders citation metadata separately.
Ignore evidence that is unrelated to the question. Do not mention, summarize, cite, or add a note or section about unrelated candidates in the research pack.
If evidence is weak, partial, list-like, or missing, say so plainly.
Distinguish user-authored notes from linked third-party sources when the evidence marks that difference.
Distinguish summaries, excerpts, transcripts, OCR, raw notes, and archived web extracts when relevant.
Keep the answer concise and useful.`

func RunPreparedSynthesis(ctx context.Context, cfg config.Config, prepared PreparedSynthesis, opts SynthesisOptions) (SynthesisResult, error) {
	if prepared.Status == "no_evidence" {
		return SynthesisResult{
			SchemaVersion: SynthesisSchemaVersion,
			Question:      prepared.Question,
			AnswerStatus:  "no_evidence",
			Warnings:      prepared.Warnings,
			Truncation:    prepared.Truncation,
			Citations:     prepared.Citations,
			AnchorContext: prepared.AnchorContext,
			PromptVersion: SynthesisPromptVersion,
			Model:         prepared.Model,
		}, nil
	}

	inputFile, err := cfg.CreateTemp("dbrain-research-synthesis-*.md")
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
	answer := strings.TrimSpace(runResult.Summary.Text)
	return SynthesisResult{
		SchemaVersion: SynthesisSchemaVersion,
		Question:      prepared.Question,
		Answer:        answer,
		AnswerStatus:  status,
		Warnings:      prepared.Warnings,
		Truncation:    prepared.Truncation,
		Citations:     CitationsUsedInAnswer(answer, prepared.Citations),
		AnchorContext: prepared.AnchorContext,
		PromptVersion: SynthesisPromptVersion,
		Model:         firstNonEmpty(runResult.Summary.Model, prepared.Model),
		Tool:          runResult.Summary.Tool,
		ToolVersion:   runResult.Summary.ToolVersion,
	}, nil
}
