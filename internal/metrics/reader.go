package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	DefaultReaderMaxLineBytes = 1 << 20
	DefaultReaderMaxBytes     = 64 << 20
	DefaultReaderMaxEvents    = 100_000
	defaultReaderBlockBytes   = 64 << 10
)

type Reader struct {
	Path         string
	MaxLineBytes int
	MaxBytes     int64
	MaxEvents    int
	BlockBytes   int
}

type Window struct {
	CoverageStart        time.Time
	CoverageEnd          time.Time
	Runs                 []RunRecord
	Imports              map[string]ImportRecord
	Markers              []Marker
	DurationSamples      []time.Duration
	ParseErrorCount      int
	ParseErrorPositions  []int64
	BytesRead            int64
	EventsRead           int
	ByteBudgetExhausted  bool
	EventBudgetExhausted bool
}

func (w Window) String() string {
	return fmt.Sprintf("metrics window: events=%d parse_errors=%d bytes=%d", w.EventsRead, w.ParseErrorCount, w.BytesRead)
}

type RunRecord struct {
	ID                   string
	Invocation           string
	StartedAt            time.Time
	CompletedAt          time.Time
	Status               string
	SelectedStages       []string
	CompletedStages      map[string]StageRecord
	Duration             time.Duration
	RecordComplete       bool
	ToleratedStageErrors int
}

type StageRecord struct {
	Status      string
	CompletedAt time.Time
	Duration    time.Duration
}

type ImportRecord struct {
	AttemptedAt  time.Time
	SucceededAt  time.Time
	AttemptCount int
	SuccessCount int
	FailureCount int
	Daily        []DailyArrival
}

type DailyArrival struct {
	Day       string `json:"day"`
	Created   int    `json:"created"`
	Updated   int    `json:"updated"`
	Unchanged int    `json:"unchanged"`
	Skipped   int    `json:"skipped"`
	Linked    int    `json:"linked"`
	Blocked   int    `json:"blocked"`
	Failed    int    `json:"failed"`
}

type Marker struct {
	Event              string
	At                 time.Time
	ExplainsContinuity bool
}

func NewReader(path string) *Reader {
	return &Reader{Path: path, MaxLineBytes: DefaultReaderMaxLineBytes, MaxBytes: DefaultReaderMaxBytes, MaxEvents: DefaultReaderMaxEvents, BlockBytes: defaultReaderBlockBytes}
}

func (r *Reader) Read(ctx context.Context, start time.Time) (Window, error) {
	window := Window{Runs: []RunRecord{}, Imports: map[string]ImportRecord{}, Markers: []Marker{}, DurationSamples: []time.Duration{}, ParseErrorPositions: []int64{}}
	if err := ctx.Err(); err != nil {
		return window, err
	}
	if r == nil || strings.TrimSpace(r.Path) == "" {
		return window, fmt.Errorf("metrics path is required")
	}
	maxLine := r.MaxLineBytes
	if maxLine <= 0 {
		maxLine = DefaultReaderMaxLineBytes
	}
	maxBytes := r.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultReaderMaxBytes
	}
	maxEvents := r.MaxEvents
	if maxEvents <= 0 {
		maxEvents = DefaultReaderMaxEvents
	}
	blockBytes := r.BlockBytes
	if blockBytes <= 0 {
		blockBytes = defaultReaderBlockBytes
	}

	f, err := os.Open(r.Path)
	if err != nil {
		return window, fmt.Errorf("open resolved metrics file: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return window, fmt.Errorf("stat resolved metrics file: %w", err)
	}
	startOffset := int64(0)
	if info.Size() > maxBytes {
		startOffset = info.Size() - maxBytes
		window.ByteBudgetExhausted = true
	}
	data, err := readTailBlocks(ctx, f, startOffset, info.Size(), blockBytes)
	if err != nil {
		return window, err
	}
	window.BytesRead = int64(len(data))
	baseOffset := startOffset
	if startOffset > 0 {
		if index := bytes.IndexByte(data, '\n'); index >= 0 {
			baseOffset += int64(index + 1)
			data = data[index+1:]
		} else {
			return window, nil
		}
	}

	runs := map[string]*RunRecord{}
	daily := map[string]map[string]*DailyArrival{}
	position := baseOffset
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return window, err
		}
		lineEnd := bytes.IndexByte(data, '\n')
		var line []byte
		if lineEnd < 0 {
			line = data
			data = nil
		} else {
			line = data[:lineEnd]
			data = data[lineEnd+1:]
		}
		linePosition := position
		position += int64(len(line) + 1)
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if window.EventsRead >= maxEvents {
			window.EventBudgetExhausted = true
			break
		}
		window.EventsRead++
		if len(line) > maxLine {
			window.ParseErrorCount++
			window.ParseErrorPositions = append(window.ParseErrorPositions, linePosition)
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			window.ParseErrorCount++
			window.ParseErrorPositions = append(window.ParseErrorPositions, linePosition)
			continue
		}
		at, ok := eventTime(event, "emitted_at")
		if !ok {
			window.ParseErrorCount++
			window.ParseErrorPositions = append(window.ParseErrorPositions, linePosition)
			continue
		}
		if at.Before(start) {
			continue
		}
		if window.CoverageStart.IsZero() || at.Before(window.CoverageStart) {
			window.CoverageStart = at
		}
		if window.CoverageEnd.IsZero() || at.After(window.CoverageEnd) {
			window.CoverageEnd = at
		}
		name, _ := event["event"].(string)
		runID, _ := event["run_id"].(string)
		var run *RunRecord
		if runID != "" {
			run = runs[runID]
			if run == nil {
				run = &RunRecord{ID: runID, CompletedStages: map[string]StageRecord{}, SelectedStages: []string{}}
				runs[runID] = run
			}
		}
		switch name {
		case "sync.run.started":
			if run == nil {
				continue
			}
			run.StartedAt, _ = eventTime(event, "started_at")
			run.Invocation, _ = event["invocation"].(string)
			run.SelectedStages = stringSlice(event["selected_stages"])
		case "sync.stage.completed":
			if run == nil {
				continue
			}
			stage, _ := event["stage"].(string)
			if stage == "" {
				continue
			}
			status, _ := event["status"].(string)
			run.CompletedStages[stage] = StageRecord{Status: status, CompletedAt: firstTime(event, "completed_at", at), Duration: millisDuration(event["duration_ms"])}
		case "sync.run.completed":
			if run == nil {
				continue
			}
			if run.StartedAt.IsZero() {
				run.StartedAt, _ = eventTime(event, "started_at")
			}
			run.CompletedAt = firstTime(event, "completed_at", at)
			run.Status, _ = event["status"].(string)
			if invocation, _ := event["invocation"].(string); run.Invocation == "" {
				run.Invocation = invocation
			}
			run.Duration = millisDuration(event["duration_ms"])
			if run.Duration == 0 && !run.StartedAt.IsZero() {
				run.Duration = run.CompletedAt.Sub(run.StartedAt)
			}
		case "sync.import.completed":
			collectImport(event, at, window.Imports, daily)
		case "scheduler.sync.enabled", "scheduler.sync.stopped", "scheduler.sync.lock_skipped", "scheduler.sync.overlap_skipped", "process.started", "process.stopped":
			explains := name != "scheduler.sync.overlap_skipped"
			window.Markers = append(window.Markers, Marker{Event: name, At: at, ExplainsContinuity: explains})
		}
	}

	for _, run := range runs {
		run.RecordComplete = !run.StartedAt.IsZero() && !run.CompletedAt.IsZero()
		if run.Status == "ok" {
			for _, stage := range run.CompletedStages {
				if stage.Status == "error" {
					run.ToleratedStageErrors++
				}
			}
		}
		window.Runs = append(window.Runs, *run)
		if run.RecordComplete && run.Status == "ok" && run.Duration > 0 {
			window.DurationSamples = append(window.DurationSamples, run.Duration)
		}
	}
	sort.Slice(window.Runs, func(i, j int) bool { return window.Runs[i].StartedAt.Before(window.Runs[j].StartedAt) })
	for source, days := range daily {
		record := window.Imports[source]
		keys := make([]string, 0, len(days))
		for day := range days {
			keys = append(keys, day)
		}
		sort.Strings(keys)
		for _, day := range keys {
			record.Daily = append(record.Daily, *days[day])
		}
		window.Imports[source] = record
	}
	return window, nil
}

func readTailBlocks(ctx context.Context, f *os.File, start, end int64, block int) ([]byte, error) {
	var chunks [][]byte
	for end > start {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		size := int64(block)
		if end-start < size {
			size = end - start
		}
		offset := end - size
		buf := make([]byte, size)
		if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
			return nil, fmt.Errorf("read resolved metrics file: %w", err)
		}
		chunks = append(chunks, buf)
		end = offset
	}
	var out []byte
	for i := len(chunks) - 1; i >= 0; i-- {
		out = append(out, chunks[i]...)
	}
	return out, nil
}

func eventTime(event map[string]any, key string) (time.Time, bool) {
	text, ok := event[key].(string)
	if !ok {
		return time.Time{}, false
	}
	value, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}, false
	}
	return value.UTC(), true
}
func firstTime(event map[string]any, key string, fallback time.Time) time.Time {
	if value, ok := eventTime(event, key); ok {
		return value
	}
	return fallback
}
func millisDuration(value any) time.Duration {
	n, ok := integer(value)
	if !ok || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Millisecond
}
func integer(value any) (int64, bool) {
	switch n := value.(type) {
	case json.Number:
		v, e := n.Int64()
		return v, e == nil
	case float64:
		return int64(n), n == float64(int64(n))
	case int:
		return int64(n), true
	case int64:
		return n, true
	default:
		return 0, false
	}
}
func stringSlice(value any) []string {
	values, ok := value.([]any)
	if !ok {
		if direct, ok := value.([]string); ok {
			return append([]string(nil), direct...)
		}
		return []string{}
	}
	out := make([]string, 0, len(values))
	for _, item := range values {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func collectImport(event map[string]any, at time.Time, imports map[string]ImportRecord, daily map[string]map[string]*DailyArrival) {
	source, _ := event["source"].(string)
	if source == "" {
		return
	}
	record := imports[source]
	attempted := firstTime(event, "attempted_at", at)
	completed := firstTime(event, "completed_at", at)
	record.AttemptCount++
	if record.AttemptedAt.IsZero() || attempted.After(record.AttemptedAt) {
		record.AttemptedAt = attempted
	}
	status, _ := event["status"].(string)
	if status == "ok" {
		record.SuccessCount++
		if record.SucceededAt.IsZero() || completed.After(record.SucceededAt) {
			record.SucceededAt = completed
		}
	} else {
		record.FailureCount++
	}
	imports[source] = record
	if daily[source] == nil {
		daily[source] = map[string]*DailyArrival{}
	}
	day := completed.UTC().Format("2006-01-02")
	row := daily[source][day]
	if row == nil {
		row = &DailyArrival{Day: day}
		daily[source][day] = row
	}
	counts, _ := event["counts"].(map[string]any)
	row.Created += count(counts["created"])
	row.Updated += count(counts["updated"])
	row.Unchanged += count(counts["unchanged"])
	row.Skipped += count(counts["skipped"])
	row.Linked += count(counts["linked"])
	row.Blocked += count(counts["blocked"])
	row.Failed += count(counts["failed"])
}
func count(value any) int {
	n, ok := integer(value)
	if !ok || n < 0 {
		return 0
	}
	return int(n)
}
