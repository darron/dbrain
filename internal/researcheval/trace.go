package researcheval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/researchtrace"
	"github.com/darron/dbrain/internal/store"
)

func LoadTrace(path string) (researchtrace.ResearchTrace, string, error) {
	resolved, err := resolveTraceJSON(path, config.Config{})
	if err != nil {
		return researchtrace.ResearchTrace{}, "", err
	}
	return loadTraceJSON(resolved)
}

func DiffTrace(ctx context.Context, cfg config.Config, st *store.Store, path string) (TraceDiff, error) {
	resolved, err := resolveTraceJSON(path, cfg)
	if err != nil {
		return TraceDiff{}, err
	}
	trace, resolved, err := loadTraceJSON(resolved)
	if err != nil {
		return TraceDiff{}, err
	}
	if trace.Pack == nil {
		return TraceDiff{}, fmt.Errorf("trace %s does not contain a research pack", resolved)
	}

	opts := OptionsFromTrace(trace)
	pack, err := brainresearch.Build(ctx, cfg, st, opts)
	if err != nil {
		return TraceDiff{}, fmt.Errorf("rerun trace %s: %w", resolved, err)
	}

	oldKeys := evidenceSourceKeys(trace.Pack.Evidence)
	newKeys := evidenceSourceKeys(pack.Evidence)
	diff := TraceDiff{
		TracePath:       resolved,
		Question:        opts.Question,
		OldSourceKeys:   oldKeys,
		NewSourceKeys:   newKeys,
		Added:           addedKeys(oldKeys, newKeys),
		Removed:         addedKeys(newKeys, oldKeys),
		Reordered:       reorderedKeys(oldKeys, newKeys),
		TopEvidence:     summarizeEvidence(pack.Evidence),
		ProposalCommand: fmt.Sprintf("dbrain eval research propose --from-trace %s", path),
	}
	return diff, nil
}

func resolveTraceJSON(path string, cfg config.Config) (string, error) {
	candidates := []string{path}
	if !filepath.IsAbs(path) && cfg.DataDir != "" {
		candidates = append(candidates, filepath.Join(cfg.DataDir, filepath.FromSlash(path)))
	}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.IsDir() {
			candidate = filepath.Join(candidate, "run.json")
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("trace %s not found", path)
}

func loadTraceJSON(path string) (researchtrace.ResearchTrace, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return researchtrace.ResearchTrace{}, "", fmt.Errorf("read trace %s: %w", path, err)
	}
	var trace researchtrace.ResearchTrace
	if err := json.Unmarshal(data, &trace); err != nil {
		return researchtrace.ResearchTrace{}, "", fmt.Errorf("parse trace %s: %w", path, err)
	}
	return trace, path, nil
}

func OptionsFromTrace(trace researchtrace.ResearchTrace) brainresearch.Options {
	opts := brainresearch.Options{Question: strings.TrimSpace(trace.Question)}
	if trace.Pack == nil {
		return opts
	}
	plan := trace.Pack.QueryPlan
	if opts.Question == "" {
		opts.Question = trace.Pack.Question
	}
	includeTopic := plan.IncludeTopicBrief
	opts.SourceTypes = append([]string(nil), plan.SourceTypes...)
	opts.Limit = plan.Limit
	opts.MaxCharsPerDoc = plan.MaxCharsPerDoc
	opts.IncludeRelated = plan.IncludeRelated
	opts.RelatedLimit = plan.RelatedLimit
	opts.IncludeTopic = &includeTopic
	opts.PlannerModel = plan.PlannerModel
	opts.UseModelPlanner = plan.PlannerModel != "" || plan.Planner == "model_assisted"
	opts.DisablePlanner = plan.Planner == "deterministic" && plan.PlannerModel == ""
	for _, lane := range plan.RetrievalLanes {
		if strings.EqualFold(lane.Name, "semantic") {
			opts.UseSemantic = strings.EqualFold(lane.Status, "used")
			opts.DisableSemantic = strings.EqualFold(lane.Reason, "disabled_for_lexical_debugging")
			break
		}
	}
	return opts
}

func summarizeEvidence(rows []ask.Evidence) []EvidenceSummary {
	out := make([]EvidenceSummary, 0, len(rows))
	for _, row := range rows {
		summary := EvidenceSummary{
			SourceKey: row.SourceKey,
			Kind:      row.Kind,
			Title:     row.Title,
		}
		if row.Retrieval != nil {
			summary.Score = row.Retrieval.Score
			summary.MatchedTerms = append([]string(nil), row.Retrieval.MatchedTerms...)
			summary.MissingTerms = append([]string(nil), row.Retrieval.MissingTerms...)
			for _, signal := range row.Retrieval.Signals {
				summary.Signals = append(summary.Signals, signal.Name)
			}
		}
		out = append(out, summary)
	}
	return out
}

func addedKeys(base []string, candidate []string) []string {
	have := map[string]struct{}{}
	for _, key := range base {
		have[key] = struct{}{}
	}
	var out []string
	for _, key := range candidate {
		if _, ok := have[key]; !ok {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func reorderedKeys(oldKeys []string, newKeys []string) []ReorderedSource {
	oldIndex := map[string]int{}
	for i, key := range oldKeys {
		oldIndex[key] = i
	}
	var out []ReorderedSource
	for i, key := range newKeys {
		if old, ok := oldIndex[key]; ok && old != i {
			out = append(out, ReorderedSource{SourceKey: key, OldIndex: old, NewIndex: i})
		}
	}
	return out
}
