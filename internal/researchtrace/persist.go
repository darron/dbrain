package researchtrace

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
)

func Write(cfg config.Config, trace ResearchTrace, artifacts ArtifactContents, opts WriteOptions) (WriteResult, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	if trace.SchemaVersion == "" {
		trace.SchemaVersion = SchemaVersion
	}
	if trace.RunID == "" {
		trace.RunID = newRunID(now())
	}
	if trace.StartedAt.IsZero() {
		trace.StartedAt = now().UTC()
	}
	if trace.CompletedAt.IsZero() {
		trace.CompletedAt = now().UTC()
	}

	root := filepath.Join(cfg.DataDir, "research-runs")
	tmpRoot := filepath.Join(root, ".tmp")
	if err := os.MkdirAll(tmpRoot, 0o700); err != nil {
		return WriteResult{}, fmt.Errorf("create trace temp root: %w", err)
	}

	finalDir := filepath.Join(root, trace.RunID)
	tmpDir := filepath.Join(tmpRoot, trace.RunID)
	for i := 0; ; i++ {
		candidate := tmpDir
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", tmpDir, i)
		}
		if err := os.Mkdir(candidate, 0o700); err == nil {
			tmpDir = candidate
			break
		} else if !os.IsExist(err) {
			return WriteResult{}, fmt.Errorf("create trace temp dir: %w", err)
		}
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	var bytesWritten int64
	if strings.TrimSpace(artifacts.PlannerInput) != "" {
		trace.Artifacts.PlannerInputPath = "planner-input.md"
		n, err := writeTraceFile(cfg, tmpDir, trace.Artifacts.PlannerInputPath, artifacts.PlannerInput)
		if err != nil {
			return WriteResult{}, err
		}
		bytesWritten += n
	}
	if strings.TrimSpace(artifacts.PlannerOutput) != "" {
		outputName := "planner-output.txt"
		if json.Valid([]byte(strings.TrimSpace(artifacts.PlannerOutput))) {
			outputName = "planner-output.json"
		}
		trace.Artifacts.PlannerOutputPath = outputName
		n, err := writeTraceFile(cfg, tmpDir, trace.Artifacts.PlannerOutputPath, artifacts.PlannerOutput)
		if err != nil {
			return WriteResult{}, err
		}
		bytesWritten += n
	}
	if strings.TrimSpace(artifacts.SynthesisInput) != "" {
		trace.Artifacts.SynthesisInputPath = "synthesis-input.md"
		n, err := writeTraceFile(cfg, tmpDir, trace.Artifacts.SynthesisInputPath, artifacts.SynthesisInput)
		if err != nil {
			return WriteResult{}, err
		}
		bytesWritten += n
	}
	trace.Metrics = computeMetrics(trace, artifacts)

	jsonBytes, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return WriteResult{}, fmt.Errorf("marshal trace json: %w", err)
	}
	n, err := writeTraceFile(cfg, tmpDir, "run.json", string(jsonBytes)+"\n")
	if err != nil {
		return WriteResult{}, err
	}
	bytesWritten += n

	n, err = writeTraceFile(cfg, tmpDir, "run.md", renderMarkdown(trace))
	if err != nil {
		return WriteResult{}, err
	}
	bytesWritten += n

	if err := os.WriteFile(filepath.Join(tmpDir, CompleteMarker), []byte(trace.CompletedAt.Format(time.RFC3339Nano)+"\n"), 0o600); err != nil {
		return WriteResult{}, fmt.Errorf("write trace completion marker: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return WriteResult{}, fmt.Errorf("create trace root: %w", err)
	}
	if _, err := os.Stat(finalDir); err == nil {
		return WriteResult{}, fmt.Errorf("trace run already exists: %s", trace.RunID)
	} else if !os.IsNotExist(err) {
		return WriteResult{}, fmt.Errorf("stat trace dir: %w", err)
	}
	if err := os.Rename(tmpDir, finalDir); err != nil {
		return WriteResult{}, fmt.Errorf("publish trace dir: %w", err)
	}

	if !opts.Retention.KeepAll {
		if _, err := Prune(cfg, opts.Retention, trace.RunID); err != nil {
			return WriteResult{}, err
		}
	}
	return WriteResult{
		RunID:        trace.RunID,
		RelativePath: filepath.ToSlash(filepath.Join("research-runs", trace.RunID)),
		Directory:    finalDir,
		Bytes:        bytesWritten,
	}, nil
}

func Prune(cfg config.Config, opts RetentionOptions, activeRunID string) (PruneResult, error) {
	if opts.KeepAll {
		return PruneResult{}, nil
	}
	keepLatest := opts.KeepLatest
	if keepLatest <= 0 {
		keepLatest = DefaultKeepRuns
	}
	maxAge := opts.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}
	root := filepath.Join(cfg.DataDir, "research-runs")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return PruneResult{}, nil
		}
		return PruneResult{}, fmt.Errorf("read trace root: %w", err)
	}
	type runDir struct {
		name    string
		path    string
		modTime time.Time
	}
	runs := make([]runDir, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if _, err := os.Stat(filepath.Join(path, CompleteMarker)); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return PruneResult{}, fmt.Errorf("stat trace dir %s: %w", entry.Name(), err)
		}
		runs = append(runs, runDir{name: entry.Name(), path: path, modTime: info.ModTime()})
	}
	sort.SliceStable(runs, func(i, j int) bool {
		return runs[i].modTime.After(runs[j].modTime)
	})
	cutoff := time.Now().Add(-maxAge)
	result := PruneResult{}
	for i, run := range runs {
		if run.name == activeRunID || i < keepLatest || run.modTime.After(cutoff) {
			result.Kept++
			continue
		}
		if err := os.RemoveAll(run.path); err != nil {
			return result, fmt.Errorf("delete trace dir %s: %w", run.name, err)
		}
		result.Deleted++
	}
	return result, nil
}

func writeTraceFile(cfg config.Config, dir string, name string, content string) (int64, error) {
	path := filepath.Join(dir, name)
	redacted := redactText(cfg, content)
	if err := os.WriteFile(path, []byte(redacted), 0o600); err != nil {
		return 0, fmt.Errorf("write trace file %s: %w", name, err)
	}
	return int64(len(redacted)), nil
}

func newRunID(now time.Time) string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%s-%d", now.UTC().Format("20060102T150405.000000000Z"), now.UnixNano())
	}
	return fmt.Sprintf("%s-%s", now.UTC().Format("20060102T150405.000000000Z"), hex.EncodeToString(buf[:]))
}
