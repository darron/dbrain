package semanticrefresh

import (
	"encoding/json"
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
	errorGeneric         = "semantic_refresh_failed"
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
	return safeRefreshErrorCode(e.Code)
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
	code := safeRefreshErrorCode(e.Code)
	return json.Marshal(struct {
		Code       string                     `json:"code"`
		Stage      store.SemanticRefreshStage `json:"stage,omitempty"`
		RunID      string                     `json:"run_id,omitempty"`
		Checkpoint string                     `json:"checkpoint,omitempty"`
		Readiness  string                     `json:"readiness,omitempty"`
		Debt       Debt                       `json:"remaining_debt"`
		Message    string                     `json:"message"`
	}{
		Code:       code,
		Stage:      safeRefreshStage(e.Stage),
		RunID:      boundedProtocolField(e.RunID, errorRunIDLimit),
		Checkpoint: boundedProtocolField(e.Checkpoint, errorCheckpointLimit),
		Readiness:  boundedProtocolField(e.Readiness, errorReadinessLimit),
		Debt:       e.Debt,
		Message:    code,
	})
}

func NewError(
	code string,
	run store.SemanticRefreshRun,
	readiness string,
	debt Debt,
	cause error,
) *RefreshError {
	code = safeRefreshErrorCode(code)
	return &RefreshError{
		Code:       code,
		Stage:      safeRefreshStage(run.Stage),
		RunID:      boundedProtocolField(run.RunID, errorRunIDLimit),
		Checkpoint: boundedProtocolField(run.Checkpoint, errorCheckpointLimit),
		Readiness:  boundedProtocolField(readiness, errorReadinessLimit),
		Debt:       debt,
		Message:    code,
		cause:      cause,
	}
}

func safeRefreshErrorCode(code string) string {
	switch code {
	case ErrorBackendBroken,
		ErrorRunConflict,
		ErrorProjection,
		ErrorEmbedding,
		ErrorEmbeddingCircuit,
		ErrorFlush,
		ErrorCompaction,
		ErrorVerify,
		ErrorNativeRoot,
		ErrorReadiness,
		ErrorCancelled:
		return code
	default:
		return errorGeneric
	}
}

func safeRefreshStage(stage store.SemanticRefreshStage) store.SemanticRefreshStage {
	switch stage {
	case store.SemanticRefreshProjection,
		store.SemanticRefreshEmbedding,
		store.SemanticRefreshFlush,
		store.SemanticRefreshCompaction,
		store.SemanticRefreshVerify,
		store.SemanticRefreshReadiness:
		return stage
	default:
		return ""
	}
}

func boundedDiagnostic(value string, limit int) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func boundedProtocolField(value string, limit int) string {
	value = boundedDiagnostic(value, limit)
	if value == "" {
		return ""
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case strings.ContainsRune("._:=+-", character):
		default:
			return ""
		}
	}
	return value
}
