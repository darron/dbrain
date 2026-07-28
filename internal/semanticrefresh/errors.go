package semanticrefresh

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/darron/dbrain/internal/store"
)

const (
	ErrorBackendBroken    = "semantic_backend_broken"
	ErrorRunConflict      = "semantic_run_conflict"
	ErrorProjection       = "semantic_projection_failed"
	ErrorEmbedding        = "semantic_embedding_failed"
	ErrorEmbeddingCircuit = "semantic_embedding_circuit_open"
	ErrorFlush            = "semantic_flush_failed"
	ErrorCompaction       = "semantic_compaction_failed"
	ErrorVerify           = "semantic_verify_failed"
	ErrorNativeRoot       = "semantic_native_root_failed"
	ErrorReadiness        = "semantic_readiness_not_ready"
	ErrorCancelled        = "semantic_refresh_cancelled"
)

const (
	errorCodeLimit       = 64
	errorCheckpointLimit = 256
	errorMessageLimit    = 512
	errorReadinessLimit  = 64
	errorRunIDLimit      = 64
)

type RefreshError struct {
	Code       string                     `json:"code"`
	Stage      store.SemanticRefreshStage `json:"stage,omitempty"`
	RunID      string                     `json:"run_id,omitempty"`
	Checkpoint string                     `json:"checkpoint,omitempty"`
	Readiness  string                     `json:"readiness,omitempty"`
	Debt       Debt                       `json:"remaining_debt"`
	Message    string                     `json:"message"`
	cause      error
}

func (e *RefreshError) Error() string {
	if e == nil {
		return ""
	}
	return boundedDiagnostic(e.Message, errorMessageLimit)
}

func (e *RefreshError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *RefreshError) MarshalJSON() ([]byte, error) {
	if e == nil {
		return []byte("null"), nil
	}
	return json.Marshal(struct {
		Code       string                     `json:"code"`
		Stage      store.SemanticRefreshStage `json:"stage,omitempty"`
		RunID      string                     `json:"run_id,omitempty"`
		Checkpoint string                     `json:"checkpoint,omitempty"`
		Readiness  string                     `json:"readiness,omitempty"`
		Debt       Debt                       `json:"remaining_debt"`
		Message    string                     `json:"message"`
	}{
		Code:       boundedDiagnostic(e.Code, errorCodeLimit),
		Stage:      e.Stage,
		RunID:      boundedDiagnostic(e.RunID, errorRunIDLimit),
		Checkpoint: boundedDiagnostic(e.Checkpoint, errorCheckpointLimit),
		Readiness:  boundedDiagnostic(e.Readiness, errorReadinessLimit),
		Debt:       e.Debt,
		Message:    boundedDiagnostic(e.Message, errorMessageLimit),
	})
}

func NewError(
	code string,
	run store.SemanticRefreshRun,
	readiness string,
	debt Debt,
	cause error,
) *RefreshError {
	message := code
	if cause != nil {
		message = cause.Error()
	}
	return &RefreshError{
		Code:       boundedDiagnostic(code, errorCodeLimit),
		Stage:      run.Stage,
		RunID:      boundedDiagnostic(run.RunID, errorRunIDLimit),
		Checkpoint: boundedDiagnostic(run.Checkpoint, errorCheckpointLimit),
		Readiness:  boundedDiagnostic(readiness, errorReadinessLimit),
		Debt:       debt,
		Message:    boundedDiagnostic(message, errorMessageLimit),
		cause:      cause,
	}
}

var diagnosticPathPattern = regexp.MustCompile(
	`(?i)(?:file://|[a-z]:[\\/]|\\\\|/(?:[^\s/,;:()[\]{}]+[/\\]?)+)[^\s,;]*`,
)

func boundedDiagnostic(value string, limit int) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	value = strings.Join(strings.Fields(value), " ")
	value = redactDiagnosticPayload(value)
	value = diagnosticPathPattern.ReplaceAllString(value, "[path]")
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func redactDiagnosticPayload(value string) string {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"provider response body",
		"provider response",
		"response body",
		"response payload",
		"source text",
		"vector payload",
	} {
		start := strings.Index(lower, marker)
		if start < 0 {
			continue
		}
		end := start + len(marker)
		for end < len(value) && value[end] == ' ' {
			end++
		}
		if end < len(value) && (value[end] == ':' || value[end] == '=') {
			prefix := strings.TrimSpace(value[:start])
			if prefix != "" {
				prefix += " "
			}
			return prefix + marker + "=[redacted]"
		}
	}
	return value
}
