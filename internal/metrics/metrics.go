package metrics

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/darron/dbrain/internal/runlock"
	"github.com/darron/dbrain/internal/runtimeenv"
)

const (
	SchemaVersion          = "dbrain.metrics.v1"
	DefaultRotateMaxBytes  = int64(32 << 20)
	DefaultRotateKeepFiles = 5
	maxRotateKeepFiles     = 128
)

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
	RotateMaxBytes     int64
	RotateKeepFiles    int
}

type Event map[string]any

type AuditStatusCounts struct {
	Pass, Warn, Fail, Unknown, Skipped int
}

var notificationMetricProviders = map[string]struct{}{
	"buzz":  {},
	"slack": {},
}

var notificationMetricKinds = map[string]struct{}{
	"failure": {}, "reminder": {}, "recovery": {}, "test": {},
}

var notificationMetricStatuses = map[string]struct{}{
	"accepted": {}, "temporary_error": {}, "permanent_error": {}, "ambiguous": {}, "retired": {}, "canceled": {},
}

var notificationMetricFailureTypes = map[string]struct{}{
	"none": {}, "multiple": {},
	"sync.config_resolution.failed": {}, "sync.metrics.open.failed": {}, "sync.metrics.close.failed": {},
	"sync.store.open.failed": {}, "sync.store.close.failed": {}, "sync.options.failed": {},
	"sync.output.failed": {}, "sync.apple_notes.permission_denied": {}, "sync.unknown": {},
	"sync.stage.apple_notes.failed": {}, "sync.stage.safari_tabs.failed": {},
	"sync.stage.x_frontier.failed": {}, "sync.stage.x_media.failed": {},
	"sync.stage.x_photo_ocr.failed": {}, "sync.stage.github.failed": {},
	"sync.stage.youtube.failed": {}, "sync.stage.feeds.failed": {},
	"sync.stage.sources.failed": {}, "sync.stage.categorize.failed": {},
	"sync.stage.media_archive.failed": {}, "sync.stage.okf_export.failed": {},
	"sync.semantic.semantic_backend_broken": {}, "sync.semantic.semantic_run_conflict": {},
	"sync.semantic.semantic_projection_failed": {}, "sync.semantic.semantic_embedding_failed": {},
	"sync.semantic.semantic_embedding_circuit_open": {}, "sync.semantic.semantic_flush_failed": {},
	"sync.semantic.semantic_compaction_failed": {}, "sync.semantic.semantic_verify_failed": {},
	"sync.semantic.semantic_native_root_failed": {}, "sync.semantic.semantic_readiness_not_ready": {},
	"sync.semantic.semantic_lock_unavailable": {},
}

func NotificationDeliveryCompletedEvent(provider, kind, failureType, status string, duration time.Duration) Event {
	if _, ok := notificationMetricProviders[provider]; !ok {
		provider = "unknown"
	}
	if _, ok := notificationMetricKinds[kind]; !ok {
		kind = "unknown"
	}
	if _, ok := notificationMetricFailureTypes[failureType]; !ok {
		failureType = "sync.unknown"
	}
	if _, ok := notificationMetricStatuses[status]; !ok {
		status = "unknown"
	}
	return Event{
		"event":        "notification.delivery.completed",
		"provider":     provider,
		"kind":         kind,
		"failure_type": failureType,
		"status":       status,
		"duration_ms":  DurationMillis(duration),
	}
}

func AuditRunCompletedEvent(profile, status string, duration time.Duration, counts AuditStatusCounts) Event {
	return Event{
		"event": "audit.run.completed", "profile": profile, "status": status,
		"duration_ms": DurationMillis(duration),
		"pass_count":  counts.Pass, "warn_count": counts.Warn, "fail_count": counts.Fail,
		"unknown_count": counts.Unknown, "skipped_count": counts.Skipped,
	}
}

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
	rotateMaxBytes, err := resolveRotateMaxBytes(rootDir)
	if err != nil {
		return Config{}, err
	}
	rotateKeepFiles, err := resolveRotateKeepFiles(rootDir)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Enabled:            enabled,
		Detail:             detail,
		IncludeSubjectKeys: runtimeenv.FirstBool(rootDir, "DBRAIN_METRICS_INCLUDE_SUBJECT_KEYS"),
		Strict:             runtimeenv.FirstBool(rootDir, "DBRAIN_METRICS_STRICT"),
		RotateMaxBytes:     rotateMaxBytes,
		RotateKeepFiles:    rotateKeepFiles,
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

func resolveRotateMaxBytes(rootDir string) (int64, error) {
	raw := strings.TrimSpace(runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_METRICS_ROTATE_MAX_BYTES"))
	if raw == "" {
		return DefaultRotateMaxBytes, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid metrics.rotate_max_bytes %q; expected a non-negative integer", raw)
	}
	return value, nil
}

func resolveRotateKeepFiles(rootDir string) (int, error) {
	raw := strings.TrimSpace(runtimeenv.FirstNonEmpty(rootDir, "DBRAIN_METRICS_ROTATE_KEEP_FILES"))
	if raw == "" {
		return DefaultRotateKeepFiles, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 || value > maxRotateKeepFiles {
		return 0, fmt.Errorf("invalid metrics.rotate_keep_files %q; expected an integer from 0 through %d", raw, maxRotateKeepFiles)
	}
	return int(value), nil
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
	if err := validateRotationConfig(cfg); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		return nil, fmt.Errorf("create metrics directory: %w", err)
	}
	lock, err := acquireMetricsLock(context.Background(), cfg.Path, runlock.Exclusive)
	if err != nil {
		return nil, fmt.Errorf("acquire metrics rotation lock: %w", err)
	}
	if err := repairOversizedActive(cfg); err != nil {
		_ = lock.Close()
		return nil, err
	}
	if err := lock.Close(); err != nil {
		return nil, fmt.Errorf("close metrics rotation lock: %w", err)
	}
	return &jsonlSink{
		cfg:  cfg,
		path: cfg.Path,
	}, nil
}

func validateRotationConfig(cfg Config) error {
	if cfg.RotateMaxBytes < 0 {
		return fmt.Errorf("invalid metrics.rotate_max_bytes %d; expected a non-negative integer", cfg.RotateMaxBytes)
	}
	if cfg.RotateKeepFiles < 0 || cfg.RotateKeepFiles > maxRotateKeepFiles {
		return fmt.Errorf("invalid metrics.rotate_keep_files %d; expected an integer from 0 through %d", cfg.RotateKeepFiles, maxRotateKeepFiles)
	}
	return nil
}

func metricsLockPath(path string) string {
	return filepath.Clean(path) + ".lock"
}

func acquireMetricsLock(ctx context.Context, path string, mode runlock.Mode) (*runlock.Lock, error) {
	lock, err := runlock.AcquireContext(ctx, metricsLockPath(path), runlock.AcquireOptions{
		Mode:     mode,
		Metadata: "owner=metrics\n",
	})
	if err != nil {
		return nil, err
	}
	metricsLockAcquired(mode)
	return lock, nil
}

func repairOversizedActive(cfg Config) error {
	if cfg.RotateMaxBytes <= 0 {
		file, err := os.OpenFile(cfg.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("open metrics file: %w", err)
		}
		return file.Close()
	}
	info, err := os.Stat(cfg.Path)
	if errors.Is(err, os.ErrNotExist) {
		file, openErr := os.OpenFile(cfg.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if openErr != nil {
			return fmt.Errorf("open metrics file: %w", openErr)
		}
		return file.Close()
	}
	if err != nil {
		return fmt.Errorf("stat metrics file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("metrics path is not a regular file")
	}
	if info.Size() <= cfg.RotateMaxBytes {
		return nil
	}
	if err := rotateActive(cfg.Path, cfg.RotateKeepFiles); err != nil {
		return fmt.Errorf("repair oversized metrics file: %w", err)
	}
	file, err := os.OpenFile(cfg.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open metrics file after repair: %w", err)
	}
	return file.Close()
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
	path     string
	w        io.Writer
	c        io.Closer
	disabled bool
	closed   bool
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
	if s.closed {
		if s.firstErr != nil && s.cfg.Strict {
			return s.firstErr
		}
		return s.recordFailureLocked(fmt.Errorf("metrics sink is closed"))
	}
	if s.disabled {
		if s.cfg.Strict {
			if s.firstErr != nil {
				return s.firstErr
			}
			return fmt.Errorf("metrics sink disabled after previous write failure")
		}
		return nil
	}
	if s.w != nil {
		if _, err := s.w.Write(data); err != nil {
			return s.recordFailureLocked(err)
		}
		return nil
	}
	if err := s.emitRotated(data); err != nil {
		return s.recordFailureLocked(err)
	}
	return nil
}

func (s *jsonlSink) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		if s.cfg.Strict && s.firstErr != nil {
			return s.firstErr
		}
		return nil
	}
	s.closed = true
	if s.c == nil {
		if s.cfg.Strict && s.firstErr != nil {
			return s.firstErr
		}
		return nil
	}
	closeErr := s.c.Close()
	if s.cfg.Strict && s.firstErr != nil {
		return s.firstErr
	}
	return closeErr
}

func (s *jsonlSink) recordFailureLocked(err error) error {
	s.disabled = true
	s.firstErr = err
	if s.cfg.Strict {
		return err
	}
	return nil
}

func (s *jsonlSink) emitRotated(data []byte) (err error) {
	lock, err := acquireMetricsLock(context.Background(), s.path, runlock.Exclusive)
	if err != nil {
		return fmt.Errorf("acquire metrics rotation lock: %w", err)
	}
	defer func() {
		if closeErr := lock.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close metrics rotation lock: %w", closeErr)
		}
	}()

	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open metrics file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("stat metrics file: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return fmt.Errorf("metrics path is not a regular file")
	}
	rotate := s.cfg.RotateMaxBytes > 0 && info.Size() > s.cfg.RotateMaxBytes
	if !rotate && s.cfg.RotateMaxBytes > 0 && info.Size() > 0 {
		rotate = int64(len(data)) > s.cfg.RotateMaxBytes-info.Size()
	}
	if rotate {
		if err := file.Close(); err != nil {
			return fmt.Errorf("close metrics file before rotation: %w", err)
		}
		if err := rotateActive(s.path, s.cfg.RotateKeepFiles); err != nil {
			return fmt.Errorf("rotate metrics file: %w", err)
		}
		file, err = os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("open metrics file after rotation: %w", err)
		}
	}
	if written, writeErr := file.Write(data); writeErr != nil {
		_ = file.Close()
		return fmt.Errorf("write metrics file: %w", writeErr)
	} else if written != len(data) {
		_ = file.Close()
		return fmt.Errorf("write metrics file: %w", io.ErrShortWrite)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close metrics file: %w", err)
	}
	return nil
}
