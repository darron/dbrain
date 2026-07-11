package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/summarizecli"
	"github.com/darron/dbrain/internal/summaryconfig"
)

const answerReviewPrompt = `Review the synthesized answer against the provided dbrain research synthesis input.
Do not use outside knowledge.
Return only JSON with:
{"verdict":"pass|warn|fail","warnings":["..."],"errors":["..."]}
Use "fail" for unsupported claims, contradictions, uncited material claims, or discussion of evidence unrelated to the user's question.
Use "warn" for weak wording, incomplete support, or uncertainty that should be visible to the user.`

type answerReviewJSON struct {
	Verdict  string   `json:"verdict"`
	Warnings []string `json:"warnings"`
	Errors   []string `json:"errors"`
}

func RunAnswerReview(ctx context.Context, cfg config.Config, prepared brainresearch.PreparedSynthesis, synthesis brainresearch.SynthesisResult, opts Options) AnswerReviewResult {
	if !opts.EnableAnswerReview && strings.TrimSpace(opts.AnswerReviewModel) == "" {
		return AnswerReviewResult{}
	}
	modelName := summaryconfig.Model(cfg.RootDir, opts.AnswerReviewModel)
	result := AnswerReviewResult{Enabled: true, Model: modelName}
	if strings.TrimSpace(modelName) == "" {
		result.Verdict = AnswerReviewUnavailable
		result.Warnings = []string{"answer_review_model_unavailable"}
		return result
	}

	inputFile, err := cfg.CreateTemp("dbrain-answer-review-*.md")
	if err != nil {
		result.Verdict = AnswerReviewError
		result.Errors = []string{fmt.Sprintf("create answer review input: %v", err)}
		return result
	}
	inputPath := inputFile.Name()
	defer func() {
		_ = os.Remove(inputPath)
	}()
	if _, err := inputFile.WriteString(answerReviewInput(prepared, synthesis)); err != nil {
		_ = inputFile.Close()
		result.Verdict = AnswerReviewError
		result.Errors = []string{fmt.Sprintf("write answer review input: %v", err)}
		return result
	}
	if err := inputFile.Close(); err != nil {
		result.Verdict = AnswerReviewError
		result.Errors = []string{fmt.Sprintf("close answer review input: %v", err)}
		return result
	}

	timeout := opts.AnswerReviewTimeout
	if timeout <= 0 {
		timeout = defaultStageTimeout
	}
	runResult, err := summarizecli.Run(ctx, summarizecli.Options{
		Binary:    opts.AnswerReviewBinary,
		Input:     inputPath,
		Summarize: true,
		Model:     modelName,
		CLI:       opts.CLI,
		Prompt:    answerReviewPrompt,
		Length:    "short",
		Timeout:   timeout,
		RootDir:   cfg.RootDir,
	})
	if err != nil {
		result.Verdict = AnswerReviewError
		result.Errors = []string{err.Error()}
		return result
	}
	raw := strings.TrimSpace(runResult.Summary.Text)
	result.Raw = raw
	parsed, err := parseAnswerReview(raw)
	if err != nil {
		result.Verdict = AnswerReviewWarn
		result.Warnings = []string{fmt.Sprintf("answer_review_parse_failed: %v", err)}
		return result
	}
	result.Verdict = parsed.Verdict
	result.Warnings = parsed.Warnings
	result.Errors = parsed.Errors
	if result.Verdict == "" {
		result.Verdict = AnswerReviewWarn
		result.Warnings = appendUniqueStrings(result.Warnings, "answer_review_missing_verdict")
	}
	return result
}

func answerReviewInput(prepared brainresearch.PreparedSynthesis, synthesis brainresearch.SynthesisResult) string {
	var b strings.Builder
	b.WriteString("# dbrain Answer Review Input\n\n")
	b.WriteString("## Answer\n")
	b.WriteString(strings.TrimSpace(synthesis.Answer))
	b.WriteString("\n\n")
	b.WriteString("## Answer Status\n")
	b.WriteString(strings.TrimSpace(synthesis.AnswerStatus))
	b.WriteString("\n\n")
	b.WriteString("## Synthesis Input\n")
	b.WriteString(prepared.Input)
	return b.String()
}

func parseAnswerReview(raw string) (AnswerReviewResult, error) {
	var payload answerReviewJSON
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return AnswerReviewResult{}, err
	}
	verdict := AnswerReviewVerdict(strings.ToLower(strings.TrimSpace(payload.Verdict)))
	switch verdict {
	case AnswerReviewPass, AnswerReviewWarn, AnswerReviewFail:
	default:
		verdict = ""
	}
	return AnswerReviewResult{
		Verdict:  verdict,
		Warnings: cleanStrings(payload.Warnings),
		Errors:   cleanStrings(payload.Errors),
	}, nil
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
