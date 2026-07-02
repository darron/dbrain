package modelbakeoff

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/itemcategorize"
	"github.com/darron/dbrain/internal/llmprovider"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/store"
)

const (
	ModeSourceSummary    = "source-summary"
	ModeCategorizeItem   = "categorize-item"
	ModeCategorizeSource = "categorize-source"
)

func Run(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Result, error) {
	started := time.Now().UTC()
	opts.Mode = strings.TrimSpace(opts.Mode)
	opts.Lookups = cleanList(opts.Lookups)
	opts.Models = cleanList(opts.Models)
	opts.ParityPreset = strings.TrimSpace(opts.ParityPreset)
	switch opts.ParityPreset {
	case "", llmprovider.ParityPresetNone, llmprovider.ParityPresetDbrainModelfile:
	default:
		return Result{}, fmt.Errorf("unsupported parity preset %q", opts.ParityPreset)
	}
	if opts.Mode == "" {
		opts.Mode = ModeSourceSummary
	}
	if len(opts.Lookups) == 0 {
		return Result{}, fmt.Errorf("at least one lookup is required")
	}
	if len(opts.Models) == 0 {
		return Result{}, fmt.Errorf("at least one model is required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	reg, err := llmprovider.RegistryForRoot(cfg.RootDir)
	if err != nil {
		return Result{}, err
	}

	result := Result{
		SchemaVersion: SchemaVersion,
		Mode:          opts.Mode,
		StartedAt:     started,
		Models:        opts.Models,
		Lookups:       opts.Lookups,
	}

	for _, lookup := range opts.Lookups {
		target, err := loadTarget(ctx, st, opts.Mode, lookup)
		if err != nil {
			return result, err
		}
		target.Runs = make([]ModelRun, 0, len(opts.Models))
		for _, candidateModel := range opts.Models {
			run := runModel(ctx, cfg, st, opts, target, candidateModel, reg)
			target.Runs = append(target.Runs, run)
			if run.Status != "ok" {
				result.Errors++
			}
		}
		annotateOverlap(target.Runs)
		result.Targets = append(result.Targets, target)
	}

	result.Summary = summarizeRuns(opts.Models, result.Targets)
	result.FinishedAt = time.Now().UTC()
	result.DurationMS = result.FinishedAt.Sub(started).Milliseconds()
	return result, nil
}

func loadTarget(ctx context.Context, st *store.Store, mode string, lookup string) (TargetRun, error) {
	switch mode {
	case ModeSourceSummary, ModeCategorizeSource:
		source, err := st.GetSource(ctx, lookup)
		if err != nil {
			return TargetRun{}, err
		}
		return TargetRun{
			Lookup:       lookup,
			SourceKey:    source.SourceKey,
			Title:        source.Title,
			SourceType:   source.SourceType,
			CanonicalURL: source.CanonicalURL,
		}, nil
	case ModeCategorizeItem:
		item, err := st.GetItem(ctx, lookup)
		if err != nil {
			return TargetRun{}, err
		}
		return TargetRun{
			Lookup:       lookup,
			SourceKey:    item.SourceKey,
			Title:        item.Title,
			SourceType:   item.SourceType,
			CanonicalURL: item.CanonicalURL,
		}, nil
	default:
		return TargetRun{}, fmt.Errorf("unsupported mode %q", mode)
	}
}

func runModel(ctx context.Context, cfg config.Config, st *store.Store, opts Options, target TargetRun, candidateModel string, reg llmprovider.Registry) ModelRun {
	started := time.Now()
	ref := reg.ParseModelRef(candidateModel)
	parity := parityParamsForRun(opts.ParityPreset, ref)
	run := newModelRunMetadata(candidateModel, ref, parity, opts.ParityPreset)
	finish := func(run ModelRun) ModelRun {
		run.DurationMS = time.Since(started).Milliseconds()
		return run
	}

	switch opts.Mode {
	case ModeSourceSummary:
		source, err := st.GetSource(ctx, target.Lookup)
		if err != nil {
			run.Status = "error"
			run.Error = err.Error()
			return finish(run)
		}
		summary, err := sourceenrich.SummarizeSourceReadOnly(ctx, cfg, source, sourceenrich.Options{
			Model:           candidateModel,
			Length:          opts.Length,
			Language:        opts.Language,
			Timeout:         opts.Timeout,
			InferenceParams: parity,
		})
		if err != nil {
			run.Status = "error"
			run.Error = err.Error()
			return finish(run)
		}
		run.Summary = &summary
		run.OutputChars = len(strings.TrimSpace(summary.Text))
	case ModeCategorizeItem:
		item, err := st.GetItem(ctx, target.Lookup)
		if err != nil {
			run.Status = "error"
			run.Error = err.Error()
			return finish(run)
		}
		cat, err := itemcategorize.Run(ctx, cfg, st, item, itemcategorize.Options{
			Model:           candidateModel,
			Timeout:         opts.Timeout,
			Apply:           false,
			IncludeImages:   opts.IncludeImages,
			InferenceParams: parity,
		})
		if err != nil {
			run.Status = "error"
			run.Error = err.Error()
			return finish(run)
		}
		run.Categorize = &cat
		run.ExistingTags = item.UserTags
		run.OutputChars = categorizeOutputChars(cat)
	case ModeCategorizeSource:
		source, err := st.GetSource(ctx, target.Lookup)
		if err != nil {
			run.Status = "error"
			run.Error = err.Error()
			return finish(run)
		}
		cat, err := itemcategorize.RunSource(ctx, cfg, st, source, itemcategorize.Options{
			Model:           candidateModel,
			Timeout:         opts.Timeout,
			Apply:           false,
			InferenceParams: parity,
		})
		if err != nil {
			run.Status = "error"
			run.Error = err.Error()
			return finish(run)
		}
		run.Categorize = &cat
		run.ExistingTags = source.UserTags
		run.OutputChars = categorizeOutputChars(cat)
	default:
		run.Status = "error"
		run.Error = fmt.Sprintf("unsupported mode %q", opts.Mode)
	}
	return finish(run)
}

func newModelRunMetadata(candidateModel string, ref llmprovider.ModelRef, parity llmprovider.ParityParams, preset string) ModelRun {
	run := ModelRun{
		Model:           candidateModel,
		Provider:        string(ref.Provider),
		APIModel:        ref.APIModel,
		Status:          "ok",
		RequestedParams: parity.Requested,
		SentParams:      parity.Sent,
		OmittedParams:   parity.Omitted,
		ParamStrictness: parity.Strictness,
		RuntimeContext:  runtimeContextForRun(ref),
	}
	if ref.Spec != nil {
		run.Transport = string(ref.Spec.Transport)
		local := ref.Spec.Local
		run.Local = &local
		run.PromptParityStatus = promptParityStatusForSpec(preset, ref.Spec)
		run.ReasoningModeStatus = ref.Spec.ReasoningPolicy.StatusWithDirectCall
	}
	return run
}

func parityParamsForRun(preset string, ref llmprovider.ModelRef) llmprovider.ParityParams {
	switch strings.TrimSpace(preset) {
	case "", llmprovider.ParityPresetNone:
		return llmprovider.EmptyParityParams()
	case llmprovider.ParityPresetDbrainModelfile:
		if ref.Spec == nil {
			return llmprovider.EmptyParityParams()
		}
		return llmprovider.DbrainParityForSpec(*ref.Spec)
	default:
		return llmprovider.EmptyParityParams()
	}
}

func promptParityStatusForSpec(preset string, spec *llmprovider.ProviderSpec) string {
	if spec == nil || strings.TrimSpace(preset) == "" || preset == llmprovider.ParityPresetNone {
		return ""
	}
	if !spec.Local {
		return "not-applicable"
	}
	return spec.PromptPolicy.ParityStatusWithPreset
}

// runtimeContextForRun captures the minimal known runtime context for a run.
// v1 does not probe live runtime metadata; Status is always "not-collected"
// so reports stay honest about what dbrain actually verified.
func runtimeContextForRun(ref llmprovider.ModelRef) RuntimeContext {
	return RuntimeContext{
		Status:   "not-collected",
		Provider: string(ref.Provider),
		APIModel: ref.APIModel,
	}
}

func cleanList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed == "" || seen[trimmed] {
				continue
			}
			seen[trimmed] = true
			out = append(out, trimmed)
		}
	}
	return out
}

func categorizeOutputChars(result itemcategorize.Result) int {
	return len(strings.Join(append(append([]string{}, result.Categories...), result.Tags...), " ")) + len(result.PrimaryCategory)
}

func outputText(run ModelRun) string {
	if run.Summary != nil {
		return run.Summary.Text
	}
	if run.Categorize != nil {
		parts := append(append([]string{}, run.Categorize.Categories...), run.Categorize.Tags...)
		parts = append(parts, run.Categorize.PrimaryCategory)
		return strings.Join(parts, " ")
	}
	return ""
}

func annotateOverlap(runs []ModelRun) {
	if len(runs) == 0 || runs[0].Status != "ok" {
		return
	}
	baseline := wordSet(outputText(runs[0]))
	if len(baseline) == 0 {
		return
	}
	for idx := range runs {
		if runs[idx].Status != "ok" {
			continue
		}
		runs[idx].WordOverlap = overlapRatio(baseline, wordSet(outputText(runs[idx])))
	}
}

func summarizeRuns(models []string, targets []TargetRun) []ModelStats {
	stats := make([]ModelStats, 0, len(models))
	for _, name := range models {
		var okCount int
		var errorCount int
		var durationTotal int64
		var charsTotal int
		var overlapTotal float64
		for _, target := range targets {
			for _, run := range target.Runs {
				if run.Model != name {
					continue
				}
				if run.Status == "ok" {
					okCount++
					durationTotal += run.DurationMS
					charsTotal += run.OutputChars
					overlapTotal += run.WordOverlap
				} else {
					errorCount++
				}
			}
		}
		row := ModelStats{Model: name, OK: okCount, Errors: errorCount}
		if okCount > 0 {
			row.AverageDurationMS = durationTotal / int64(okCount)
			row.AverageOutputChars = charsTotal / okCount
			row.AverageBaselineWordOverlap = overlapTotal / float64(okCount)
		}
		stats = append(stats, row)
	}
	return stats
}

func wordSet(value string) map[string]struct{} {
	fields := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-'
	})
	set := map[string]struct{}{}
	for _, field := range fields {
		if len(field) < 3 {
			continue
		}
		set[field] = struct{}{}
	}
	return set
}

func overlapRatio(a map[string]struct{}, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var intersection int
	for word := range b {
		if _, ok := a[word]; ok {
			intersection++
		}
	}
	return float64(intersection) / float64(len(a))
}
