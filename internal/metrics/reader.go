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
	"syscall"
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
	CoverageStart          time.Time
	CoverageEnd            time.Time
	Runs                   []RunRecord
	Imports                map[string]ImportRecord
	Markers                []Marker
	DurationSamples        []time.Duration
	ParseErrorCount        int
	ParseErrorPositions    []int64
	BytesRead              int64
	EventsRead             int
	ByteBudgetExhausted    bool
	EventBudgetExhausted   bool
	AttemptCount           int
	CompletedCount         int
	LatestAttemptPresent   bool
	LatestCompletedPresent bool
	Semantic               SemanticActivity
}

// SemanticActivity is the bounded, content-free semantic refresh view attached
// to the newest parent sync run observed in this metrics window.
type SemanticActivity struct {
	Present    bool
	Incomplete bool
	Latest     SemanticRefreshRecord
}

type SemanticRefreshRecord struct {
	State                             string
	StartedAt, CompletedAt, FailureAt time.Time
	Duration                          time.Duration
	ErrorCode                         string
	Counters                          SemanticRefreshCounters
	Stages                            []SemanticStageRecord
}

type SemanticRefreshCounters struct {
	ProjectedParents, EmbeddedChunks                  int64
	FlushedVectors, CompactedVectors, VerifiedVectors int64
	SuccessorRuns                                     int64
}

type SemanticStageRecord struct {
	Stage    string
	Status   string
	Duration time.Duration
}

var semanticStageOrder = []string{"projection", "embedding", "flush", "compaction", "verification", "readiness"}

var semanticErrorCodes = map[string]struct{}{
	"semantic_backend_broken": {}, "semantic_run_conflict": {}, "semantic_projection_failed": {},
	"semantic_embedding_failed": {}, "semantic_embedding_circuit_open": {}, "semantic_flush_failed": {},
	"semantic_compaction_failed": {}, "semantic_verify_failed": {}, "semantic_native_root_failed": {},
	"semantic_readiness_not_ready": {}, "semantic_lock_unavailable": {}, "semantic_refresh_cancelled": {},
	"semantic_refresh_failed": {},
}

type semanticRefreshInternal struct {
	SemanticRefreshRecord
	started, terminal, invalid bool
	stages                     map[string]bool
	counters                   map[string]bool
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
	AttemptedAt   time.Time
	SucceededAt   time.Time
	LastArrivalAt time.Time
	AttemptCount  int
	SuccessCount  int
	FailureCount  int
	Daily         []DailyArrival
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

	// Open first with O_NONBLOCK, then validate the descriptor. This avoids a
	// path-check/open race turning a swapped FIFO into a blocking audit read.
	f, err := os.OpenFile(r.Path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return window, fmt.Errorf("open resolved metrics file: %w", err)
	}
	defer func() { _ = f.Close() }()
	openedInfo, err := f.Stat()
	if err != nil {
		return window, fmt.Errorf("stat resolved metrics file: %w", err)
	}
	if !openedInfo.Mode().IsRegular() {
		return window, fmt.Errorf("resolved metrics path is not a regular file")
	}
	startOffset := int64(0)
	if openedInfo.Size() > maxBytes {
		startOffset = openedInfo.Size() - maxBytes
	}

	runs := map[string]*RunRecord{}
	semantic := map[string]*semanticRefreshInternal{}
	daily := map[string]map[string]*DailyArrival{}
	foundBoundary := false
	window.BytesRead, err = readLinesReverse(ctx, f, startOffset, openedInfo.Size(), blockBytes, func(line []byte, linePosition int64) bool {
		if err := ctx.Err(); err != nil {
			return false
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			return true
		}
		if window.EventsRead >= maxEvents {
			window.EventBudgetExhausted = true
			return false
		}
		window.EventsRead++
		if len(line) > maxLine {
			window.ParseErrorCount++
			window.ParseErrorPositions = append(window.ParseErrorPositions, linePosition)
			return true
		}
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			window.ParseErrorCount++
			window.ParseErrorPositions = append(window.ParseErrorPositions, linePosition)
			return true
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			window.ParseErrorCount++
			window.ParseErrorPositions = append(window.ParseErrorPositions, linePosition)
			return true
		}
		if schema, _ := event["schema"].(string); schema != SchemaVersion {
			window.ParseErrorCount++
			window.ParseErrorPositions = append(window.ParseErrorPositions, linePosition)
			return true
		}
		name, _ := event["event"].(string)
		if strings.TrimSpace(name) == "" {
			window.ParseErrorCount++
			window.ParseErrorPositions = append(window.ParseErrorPositions, linePosition)
			return true
		}
		at, ok := eventTime(event, "emitted_at")
		if !ok {
			window.ParseErrorCount++
			window.ParseErrorPositions = append(window.ParseErrorPositions, linePosition)
			return true
		}
		if at.Before(start) {
			window.CoverageStart = at
			foundBoundary = true
			return false
		}
		if window.CoverageStart.IsZero() || at.Before(window.CoverageStart) {
			window.CoverageStart = at
		}
		if window.CoverageEnd.IsZero() || at.After(window.CoverageEnd) {
			window.CoverageEnd = at
		}
		runID, _ := event["run_id"].(string)
		var run *RunRecord
		if runID != "" {
			run = runs[runID]
			if run == nil {
				run = &RunRecord{ID: runID, CompletedStages: map[string]StageRecord{}, SelectedStages: []string{}}
				runs[runID] = run
			}
		}
		semanticRecord := semantic[runID]
		if runID != "" && isSemanticLifecycleEvent(name) && semanticRecord == nil {
			semanticRecord = &semanticRefreshInternal{stages: map[string]bool{}, counters: map[string]bool{}}
			semantic[runID] = semanticRecord
		}
		switch name {
		case "semantic.refresh.started":
			if semanticRecord == nil || semanticRecord.started {
				return true
			}
			semanticRecord.started = true
			startedAt, ok := eventTime(event, "started_at")
			if !ok {
				semanticRecord.invalid = true
				return true
			}
			semanticRecord.StartedAt = startedAt
		case "semantic.stage.completed":
			collectSemanticStage(semanticRecord, event)
		case "semantic.refresh.completed":
			collectSemanticTerminal(semanticRecord, event)
		case "sync.run.started":
			if run == nil {
				return true
			}
			if startedAt, ok := eventTime(event, "started_at"); ok {
				run.StartedAt = startedAt
			}
			if invocation, _ := event["invocation"].(string); invocation != "" {
				run.Invocation = invocation
			}
			if selected := stringSlice(event["selected_stages"]); len(selected) > 0 {
				run.SelectedStages = selected
			}
		case "sync.stage.completed":
			if run == nil {
				return true
			}
			stage, _ := event["stage"].(string)
			if stage == "" {
				return true
			}
			status, _ := event["status"].(string)
			run.CompletedStages[stage] = StageRecord{Status: status, CompletedAt: firstTime(event, "completed_at", at), Duration: millisDuration(event["duration_ms"])}
		case "sync.run.completed":
			if run == nil {
				return true
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
		return true
	})
	if err != nil {
		return window, err
	}
	if err := ctx.Err(); err != nil {
		return window, err
	}
	window.ByteBudgetExhausted = startOffset > 0 && !foundBoundary
	window.Semantic = latestSemanticActivity(semantic, window.ByteBudgetExhausted || window.EventBudgetExhausted)

	for _, run := range runs {
		if run.Duration == 0 && !run.StartedAt.IsZero() && !run.CompletedAt.IsZero() {
			run.Duration = run.CompletedAt.Sub(run.StartedAt)
		}
		run.RecordComplete = !run.StartedAt.IsZero() && !run.CompletedAt.IsZero()
		if !run.StartedAt.IsZero() {
			window.AttemptCount++
		}
		if !run.CompletedAt.IsZero() {
			window.CompletedCount++
		}
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
	window.LatestAttemptPresent = window.AttemptCount > 0
	window.LatestCompletedPresent = window.CompletedCount > 0
	sort.Slice(window.Runs, func(i, j int) bool { return window.Runs[i].StartedAt.Before(window.Runs[j].StartedAt) })
	sort.Slice(window.Markers, func(i, j int) bool { return window.Markers[i].At.Before(window.Markers[j].At) })
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

func collectSemanticStage(record *semanticRefreshInternal, event map[string]any) {
	if record == nil {
		return
	}
	stage, ok := canonicalSemanticStage(event["stage"])
	if !ok {
		record.invalid = true
		return
	}
	if record.stages[stage] { // reverse reader sees the newest duplicate first.
		return
	}
	status, ok := semanticStageStatus(event["status"])
	duration, durationOK := semanticDuration(event["duration_ms"])
	if !ok || !durationOK {
		record.invalid = true
		return
	}
	record.stages[stage] = true
	record.Stages = append(record.Stages, SemanticStageRecord{Stage: stage, Status: status, Duration: duration})
	collectSemanticCounters(record, event["counts"])
}

func collectSemanticTerminal(record *semanticRefreshInternal, event map[string]any) {
	if record == nil || record.terminal { // reverse reader sees the newest duplicate first.
		return
	}
	record.terminal = true
	startedAt, startedOK := eventTime(event, "started_at")
	completedAt, completedOK := eventTime(event, "completed_at")
	duration, durationOK := semanticDuration(event["duration_ms"])
	state, stateOK := semanticRefreshState(event["status"], event["outcome"])
	if !startedOK || !completedOK || !durationOK || !stateOK {
		record.invalid = true
		return
	}
	record.StartedAt, record.CompletedAt, record.Duration, record.State = startedAt, completedAt, duration, state
	if state == "failed" {
		code, ok := event["semantic_error_code"].(string)
		if !ok {
			record.invalid = true
			return
		}
		if _, ok := semanticErrorCodes[code]; !ok {
			record.invalid = true
			return
		}
		record.ErrorCode, record.FailureAt = code, completedAt
	}
}

func canonicalSemanticStage(value any) (string, bool) {
	stage, _ := value.(string)
	if stage == "verify" {
		stage = "verification"
	}
	for _, known := range semanticStageOrder {
		if stage == known {
			return stage, true
		}
	}
	return "", false
}

func semanticStageStatus(value any) (string, bool) {
	status, _ := value.(string)
	switch status {
	case "ok":
		return "succeeded", true
	case "error":
		return "failed", true
	case "skipped":
		return "skipped", true
	default:
		return "unknown", false
	}
}

func semanticRefreshState(statusValue, outcomeValue any) (string, bool) {
	status, _ := statusValue.(string)
	outcome, _ := outcomeValue.(string)
	switch status {
	case "ok":
		if outcome == "completed" || outcome == "" {
			return "succeeded", true
		}
	case "error":
		return "failed", true
	case "skipped":
		return "skipped", true
	}
	return "unknown", false
}

func semanticDuration(value any) (time.Duration, bool) {
	n, ok := integer(value)
	if !ok || n < 0 || n > int64(time.Duration(1<<63-1)/time.Millisecond) {
		return 0, false
	}
	return time.Duration(n) * time.Millisecond, true
}

func isSemanticLifecycleEvent(name string) bool {
	switch name {
	case "semantic.refresh.started", "semantic.stage.completed", "semantic.refresh.completed":
		return true
	default:
		return false
	}
}

func collectSemanticCounters(record *semanticRefreshInternal, value any) {
	counts, ok := value.(map[string]any)
	if !ok {
		record.invalid = true
		return
	}
	for key, target := range map[string]*int64{
		"projected_parents": &record.Counters.ProjectedParents, "embedded_chunks": &record.Counters.EmbeddedChunks,
		"flushed_vectors": &record.Counters.FlushedVectors, "compacted_vectors": &record.Counters.CompactedVectors,
		"verified_vectors": &record.Counters.VerifiedVectors, "successor_runs": &record.Counters.SuccessorRuns,
	} {
		value, present := counts[key]
		if !present || record.counters[key] {
			continue
		}
		n, valid := integer(value)
		if !valid || n < 0 {
			record.invalid = true
			continue
		}
		*target, record.counters[key] = n, true
	}
}

func latestSemanticActivity(records map[string]*semanticRefreshInternal, bounded bool) SemanticActivity {
	var latest *semanticRefreshInternal
	for _, record := range records {
		if latest == nil || semanticRecordAt(record).After(semanticRecordAt(latest)) {
			latest = record
		}
	}
	if latest == nil {
		return SemanticActivity{}
	}
	stages := make(map[string]SemanticStageRecord, len(latest.Stages))
	for _, stage := range latest.Stages {
		stages[stage.Stage] = stage
	}
	latest.Stages = latest.Stages[:0]
	for _, stage := range semanticStageOrder {
		if value, ok := stages[stage]; ok {
			latest.Stages = append(latest.Stages, value)
		} else {
			latest.Stages = append(latest.Stages, SemanticStageRecord{Stage: stage, Status: "unknown"})
		}
	}
	incomplete := bounded || latest.invalid || !latest.started || !latest.terminal
	if incomplete {
		latest.State = "unknown"
		latest.ErrorCode = ""
		latest.FailureAt = time.Time{}
	}
	return SemanticActivity{Present: true, Incomplete: incomplete, Latest: latest.SemanticRefreshRecord}
}

func semanticRecordAt(record *semanticRefreshInternal) time.Time {
	if !record.CompletedAt.IsZero() {
		return record.CompletedAt
	}
	return record.StartedAt
}

func readLinesReverse(ctx context.Context, f *os.File, start, end int64, block int, visit func([]byte, int64) bool) (int64, error) {
	var carry []byte
	fileEnd := end
	var bytesRead int64
	for end > start {
		if err := ctx.Err(); err != nil {
			return bytesRead, err
		}
		size := int64(block)
		if end-start < size {
			size = end - start
		}
		offset := end - size
		buf := make([]byte, size)
		if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
			return bytesRead, fmt.Errorf("read resolved metrics file: %w", err)
		}
		bytesRead += int64(len(buf))
		data := make([]byte, 0, len(buf)+len(carry))
		data = append(data, buf...)
		data = append(data, carry...)
		limit := len(data)
		if end == fileEnd && limit > 0 && data[limit-1] == '\n' {
			limit--
		}
		for limit > 0 {
			index := bytes.LastIndexByte(data[:limit], '\n')
			if index < 0 {
				break
			}
			if !visit(data[index+1:limit], offset+int64(index+1)) {
				return bytesRead, nil
			}
			limit = index
		}
		carry = append(carry[:0], data[:limit]...)
		end = offset
	}
	if start == 0 && len(carry) > 0 {
		if !visit(carry, 0) {
			return bytesRead, nil
		}
	}
	return bytesRead, nil
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
	if row.Created+row.Updated > 0 && (record.LastArrivalAt.IsZero() || completed.After(record.LastArrivalAt)) {
		record.LastArrivalAt = completed
	}
	imports[source] = record
}
func count(value any) int {
	n, ok := integer(value)
	if !ok || n < 0 {
		return 0
	}
	return int(n)
}
