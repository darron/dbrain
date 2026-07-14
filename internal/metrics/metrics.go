package metrics

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/runtimeenv"
)

const SchemaVersion = "dbrain.metrics.v1"

type Detail string

const (
	DetailStage     Detail = "stage"
	DetailItem      Detail = "item"
	DetailModelCall Detail = "model_call"
)

type Config struct {
	Enabled            bool
	Path               string
	Detail             Detail
	IncludeSubjectKeys bool
	Strict             bool
}

type Event map[string]any

type SQLiteArchiveFailureCode string

const (
	SQLiteArchiveFailureArchive  SQLiteArchiveFailureCode = "archive_failed"
	SQLiteArchiveFailureCanceled SQLiteArchiveFailureCode = "canceled"
	SQLiteArchiveFailureLock     SQLiteArchiveFailureCode = "lock_failed"
	SQLiteArchiveFailureState    SQLiteArchiveFailureCode = "state_failed"
	SQLiteArchiveFailureTimeout  SQLiteArchiveFailureCode = "timeout"
)

func SQLiteArchiveAttemptEvent() Event {
	return Event{
		"event":         "scheduler.sqlite_archive.attempt",
		"status":        "attempted",
		"attempt_count": int64(1),
	}
}

func SQLiteArchiveStartedEvent() Event {
	return Event{
		"event":         "scheduler.sqlite_archive.started",
		"status":        "running",
		"started_count": int64(1),
	}
}

func SQLiteArchiveCompletedEvent(duration time.Duration, snapshotBytes int64, archiveBytes int64) Event {
	return Event{
		"event":           "scheduler.sqlite_archive.completed",
		"status":          "ok",
		"completed_count": int64(1),
		"duration_ms":     DurationMillis(duration),
		"snapshot_bytes":  snapshotBytes,
		"archive_bytes":   archiveBytes,
	}
}

func SQLiteArchiveFailedEvent(duration time.Duration, code SQLiteArchiveFailureCode) Event {
	switch code {
	case SQLiteArchiveFailureArchive, SQLiteArchiveFailureCanceled, SQLiteArchiveFailureLock, SQLiteArchiveFailureState, SQLiteArchiveFailureTimeout:
	default:
		code = SQLiteArchiveFailureArchive
	}
	return Event{
		"event":        "scheduler.sqlite_archive.failed",
		"status":       "failed",
		"failed_count": int64(1),
		"duration_ms":  DurationMillis(duration),
		"failure_code": string(code),
	}
}

func SQLiteArchiveLockSkippedEvent() Event {
	return Event{
		"event":         "scheduler.sqlite_archive.lock_skipped",
		"status":        "skipped",
		"skipped_count": int64(1),
	}
}

func SQLiteArchiveOverlapSkippedEvent() Event {
	return Event{
		"event":         "scheduler.sqlite_archive.overlap_skipped",
		"status":        "skipped",
		"skipped_count": int64(1),
	}
}

func SQLiteArchiveIntervalSkippedEvent(remaining time.Duration) Event {
	remainingSeconds := int64(remaining / time.Second)
	if remainingSeconds < 0 {
		remainingSeconds = 0
	}
	return Event{
		"event":             "scheduler.sqlite_archive.interval_skipped",
		"status":            "skipped",
		"skipped_count":     int64(1),
		"remaining_seconds": remainingSeconds,
	}
}

type Sink interface {
	Enabled() bool
	Detail() Detail
	IncludeSubjectKeys() bool
	Emit(Event) error
	Close() error
}

type RunContext struct {
	RunID      string
	Command    string
	Invocation string
	Sink       Sink
}

func ResolveConfig(rootDir string, logDir string) (Config, error) {
	enabled := runtimeenv.FirstBool(rootDir, "DBRAIN_METRICS_ENABLED")
	detail := Detail(strings.TrimSpace(runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_METRICS_DETAIL")))
	if detail == "" {
		detail = DetailStage
	}
	if err := validateDetail(detail); err != nil {
		return Config{}, err
	}

	cfg := Config{
		Enabled:            enabled,
		Detail:             detail,
		IncludeSubjectKeys: runtimeenv.FirstBool(rootDir, "DBRAIN_METRICS_INCLUDE_SUBJECT_KEYS"),
		Strict:             runtimeenv.FirstBool(rootDir, "DBRAIN_METRICS_STRICT"),
	}
	if !enabled {
		return cfg, nil
	}

	path := strings.TrimSpace(runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_METRICS_PATH"))
	if path == "" {
		path = filepath.Join(logDir, "metrics.jsonl")
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(logDir, path)
	}
	cfg.Path = path
	return cfg, nil
}

func validateDetail(detail Detail) error {
	switch detail {
	case DetailStage, DetailItem, DetailModelCall:
		return nil
	default:
		return fmt.Errorf("invalid metrics.detail %q; expected stage, item, or model_call", detail)
	}
}

func Open(cfg Config) (Sink, error) {
	if !cfg.Enabled {
		return NoopSink(), nil
	}
	if cfg.Detail == "" {
		cfg.Detail = DetailStage
	}
	if err := validateDetail(cfg.Detail); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Path) == "" {
		return nil, fmt.Errorf("metrics.path is required when metrics are enabled")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		return nil, fmt.Errorf("create metrics directory: %w", err)
	}
	file, err := os.OpenFile(cfg.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open metrics file: %w", err)
	}
	return &jsonlSink{
		cfg: cfg,
		w:   file,
		c:   file,
	}, nil
}

func NoopSink() Sink {
	return noopSink{}
}

func NewRunID(prefix string, now time.Time) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "run"
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		n := uint32(now.UnixNano())
		b[0], b[1], b[2], b[3] = byte(n>>24), byte(n>>16), byte(n>>8), byte(n)
	}
	return fmt.Sprintf("%s_%s_%s", prefix, now.UTC().Format("20060102T150405Z"), hex.EncodeToString(b[:]))
}

func (ctx RunContext) Enabled() bool {
	return ctx.Sink != nil && ctx.Sink.Enabled()
}

func (ctx RunContext) Detail() Detail {
	if ctx.Sink == nil {
		return DetailStage
	}
	return ctx.Sink.Detail()
}

func (ctx RunContext) IncludeSubjectKeys() bool {
	return ctx.Sink != nil && ctx.Sink.IncludeSubjectKeys()
}

func (ctx RunContext) Emit(event Event) error {
	if ctx.Sink == nil || !ctx.Sink.Enabled() {
		return nil
	}
	out := make(Event, len(event)+6)
	out["schema"] = SchemaVersion
	out["emitted_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	if ctx.RunID != "" {
		out["run_id"] = ctx.RunID
	}
	if ctx.Command != "" {
		out["command"] = ctx.Command
	}
	if ctx.Invocation != "" {
		out["invocation"] = ctx.Invocation
	}
	for key, value := range event {
		out[key] = value
	}
	return ctx.Sink.Emit(out)
}

func (e Event) WithSubject(kind string, key string, includeKey bool) Event {
	out := make(Event, len(e)+3)
	for name, value := range e {
		out[name] = value
	}
	kind = strings.TrimSpace(kind)
	key = strings.TrimSpace(key)
	if kind != "" {
		out["subject_kind"] = kind
	}
	if key != "" {
		out["subject_hash"] = SubjectHash(kind, key)
		if includeKey {
			out["subject_key"] = key
		}
	}
	return out
}

func SubjectHash(kind string, key string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(SchemaVersion))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.TrimSpace(kind)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.TrimSpace(key)))
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func DurationMillis(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64(d / time.Millisecond)
}

func ErrorObject(err any) map[string]any {
	message := ""
	switch value := err.(type) {
	case nil:
		return nil
	case string:
		message = value
	case error:
		message = value.Error()
	default:
		message = fmt.Sprint(value)
	}
	message = strings.TrimSpace(message)
	if len(message) > 300 {
		message = message[:300]
	}
	class := "error"
	lower := strings.ToLower(message)
	if strings.Contains(lower, "context deadline exceeded") || strings.Contains(lower, "timeout") {
		class = "timeout"
	} else if strings.Contains(lower, "context canceled") || strings.Contains(lower, "cancel") {
		class = "canceled"
	}
	return map[string]any{
		"class":   class,
		"message": message,
	}
}

type noopSink struct{}

func (noopSink) Enabled() bool            { return false }
func (noopSink) Detail() Detail           { return DetailStage }
func (noopSink) IncludeSubjectKeys() bool { return false }
func (noopSink) Emit(Event) error         { return nil }
func (noopSink) Close() error             { return nil }

type jsonlSink struct {
	mu       sync.Mutex
	cfg      Config
	w        io.Writer
	c        io.Closer
	disabled bool
	firstErr error
}

func (s *jsonlSink) Enabled() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.disabled
}

func (s *jsonlSink) Detail() Detail {
	if s == nil || s.cfg.Detail == "" {
		return DetailStage
	}
	return s.cfg.Detail
}

func (s *jsonlSink) IncludeSubjectKeys() bool {
	return s != nil && s.cfg.IncludeSubjectKeys
}

func (s *jsonlSink) Emit(event Event) error {
	if s == nil {
		return nil
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disabled {
		if s.cfg.Strict {
			if s.firstErr != nil {
				return s.firstErr
			}
			return fmt.Errorf("metrics sink disabled after previous write failure")
		}
		return nil
	}
	if _, err := s.w.Write(data); err != nil {
		s.disabled = true
		s.firstErr = err
		if s.cfg.Strict {
			return err
		}
		return nil
	}
	return nil
}

func (s *jsonlSink) Close() error {
	if s == nil || s.c == nil {
		return nil
	}
	closeErr := s.c.Close()
	if s.cfg.Strict && s.firstErr != nil {
		return s.firstErr
	}
	return closeErr
}
