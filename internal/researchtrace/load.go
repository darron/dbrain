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
	LoadError     string    `json:"load_error,omitempty"`
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
	trace, err := Decode(data)
	if err != nil {
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
			summaries = append(summaries, summarizeUnreadableTrace(entry.Name(), filepath.ToSlash(filepath.Join("research-runs", entry.Name())), filepath.Join(dir, CompleteMarker), err))
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

func summarizeUnreadableTrace(runID string, relativePath string, completePath string, loadErr error) TraceSummary {
	summary := TraceSummary{
		RunID:        runID,
		RelativePath: relativePath,
		StopReason:   "trace_unreadable",
		LoadError:    loadErr.Error(),
	}
	if data, err := os.ReadFile(completePath); err == nil {
		if completedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data))); err == nil {
			summary.CompletedAt = completedAt
		}
	}
	return summary
}

func traceRoot(cfg config.Config) string {
	return filepath.Join(cfg.DataDir, "research-runs")
}

func Decode(data []byte) (ResearchTrace, error) {
	var trace ResearchTrace
	if err := unmarshalTrace(data, &trace); err != nil {
		return ResearchTrace{}, err
	}
	return trace, nil
}

func unmarshalTrace(data []byte, trace *ResearchTrace) error {
	if err := json.Unmarshal(data, trace); err != nil {
		repaired := repairLegacyRedactedJSON(data)
		if string(repaired) == string(data) {
			return err
		}
		if repairErr := json.Unmarshal(repaired, trace); repairErr == nil {
			return nil
		}
		return err
	}
	return nil
}

func repairLegacyRedactedJSON(data []byte) []byte {
	const marker = `[redacted-path]"`
	text := string(data)
	if !strings.Contains(text, marker) {
		return data
	}
	var b strings.Builder
	b.Grow(len(text) + strings.Count(text, marker))
	for index := 0; index < len(text); {
		next := strings.Index(text[index:], marker)
		if next < 0 {
			b.WriteString(text[index:])
			break
		}
		start := index + next
		quote := start + len(marker) - 1
		b.WriteString(text[index:quote])
		if shouldEscapeLegacyRedactedQuote(text, quote) {
			b.WriteByte('\\')
		}
		b.WriteByte('"')
		index = quote + 1
	}
	return []byte(b.String())
}

func shouldEscapeLegacyRedactedQuote(text string, quote int) bool {
	for index := quote + 1; index < len(text); index++ {
		switch text[index] {
		case ' ', '\t':
			continue
		case ',', '}', ']', '\n', '\r', ':':
			return false
		default:
			return true
		}
	}
	return false
}
