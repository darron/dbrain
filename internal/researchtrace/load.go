package researchtrace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
)

type TraceSummary struct {
	RunID         string    `json:"run_id"`
	RelativePath  string    `json:"relative_path"`
	Surface       string    `json:"surface,omitempty"`
	Question      string    `json:"question,omitempty"`
	CompletedAt   time.Time `json:"completed_at,omitempty"`
	StopReason    string    `json:"stop_reason,omitempty"`
	EvidenceCount int       `json:"evidence_count,omitempty"`
	AnswerStatus  string    `json:"answer_status,omitempty"`
}

func Load(cfg config.Config, path string) (ResearchTrace, string, error) {
	resolved, err := ResolvePath(cfg, path)
	if err != nil {
		return ResearchTrace{}, "", err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return ResearchTrace{}, "", fmt.Errorf("read trace %s: %w", path, err)
	}
	var trace ResearchTrace
	if err := json.Unmarshal(data, &trace); err != nil {
		return ResearchTrace{}, "", fmt.Errorf("parse trace %s: %w", path, err)
	}
	return trace, resolved, nil
}

func List(cfg config.Config, limit int) ([]TraceSummary, error) {
	if limit <= 0 {
		limit = 25
	}
	root := traceRoot(cfg)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read trace root: %w", err)
	}
	summaries := make([]TraceSummary, 0, min(limit, len(entries)))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, CompleteMarker)); err != nil {
			continue
		}
		trace, _, err := Load(cfg, filepath.Join("research-runs", entry.Name()))
		if err != nil {
			continue
		}
		summaries = append(summaries, summarizeTrace(trace, filepath.ToSlash(filepath.Join("research-runs", entry.Name()))))
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		left := summaries[i].CompletedAt
		right := summaries[j].CompletedAt
		if left.Equal(right) {
			return summaries[i].RunID > summaries[j].RunID
		}
		return left.After(right)
	})
	if len(summaries) > limit {
		summaries = summaries[:limit]
	}
	return summaries, nil
}

func ResolvePath(cfg config.Config, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("trace path is required")
	}
	root := traceRoot(cfg)
	var candidate string
	if filepath.IsAbs(path) {
		candidate = filepath.Clean(path)
	} else {
		cleaned := filepath.Clean(filepath.FromSlash(path))
		if cleaned == "." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || cleaned == ".." {
			return "", fmt.Errorf("trace path must stay under research-runs")
		}
		if strings.HasPrefix(cleaned, "research-runs"+string(filepath.Separator)) {
			candidate = filepath.Join(cfg.DataDir, cleaned)
		} else {
			candidate = filepath.Join(root, cleaned)
		}
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return "", fmt.Errorf("trace %s not found", path)
	}
	if info.IsDir() {
		candidate = filepath.Join(candidate, "run.json")
	}
	candidate = filepath.Clean(candidate)
	if !withinPath(candidate, root) {
		return "", fmt.Errorf("trace path must stay under research-runs")
	}
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("trace %s not found", path)
	}
	return candidate, nil
}

func summarizeTrace(trace ResearchTrace, relativePath string) TraceSummary {
	summary := TraceSummary{
		RunID:        trace.RunID,
		RelativePath: relativePath,
		Surface:      trace.Surface,
		Question:     trace.Question,
		CompletedAt:  trace.CompletedAt,
		StopReason:   trace.StopReason,
	}
	if trace.Pack != nil {
		summary.EvidenceCount = len(trace.Pack.Evidence)
	}
	if trace.Synthesis != nil {
		summary.AnswerStatus = trace.Synthesis.AnswerStatus
	}
	return summary
}

func traceRoot(cfg config.Config) string {
	return filepath.Join(cfg.DataDir, "research-runs")
}
