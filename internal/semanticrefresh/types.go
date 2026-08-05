package semanticrefresh

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/store"
)

type Outcome string

const (
	OutcomeSkipped   Outcome = "skipped"
	OutcomeCompleted Outcome = "completed"
)

type Debt struct {
	DirtyParents      int `json:"dirty_parents"`
	PendingEmbeddings int `json:"pending_embeddings"`
	DueRetries        int `json:"due_retries"`
	ScheduledRetries  int `json:"scheduled_retries"`
	BlockedEmbeddings int `json:"blocked_embeddings"`
	FailedEmbeddings  int `json:"failed_embeddings"`
	Indexed           int `json:"indexed"`
	L0Ready           int `json:"l0_ready"`
	Tombstones        int `json:"tombstones"`
	Segments          int `json:"segments"`
}

type Progress struct {
	RunID, ProfileID, Checkpoint, Readiness string
	Stage                                   store.SemanticRefreshStage
	Counters                                store.SemanticRefreshCounters
	Debt                                    Debt
	At                                      time.Time
}

type Result struct {
	Outcome     Outcome                   `json:"outcome"`
	SkipReason  string                    `json:"skip_reason,omitempty"`
	Capability  semanticindex.Capability  `json:"capability"`
	Run         *store.SemanticRefreshRun `json:"run,omitempty"`
	Debt        Debt                      `json:"remaining_debt"`
	StartedAt   time.Time                 `json:"started_at"`
	CompletedAt time.Time                 `json:"completed_at"`
	Duration    time.Duration             `json:"duration"`
	Stages      []StageStats              `json:"stages"`
}

type StageStats struct {
	Stage         store.SemanticRefreshStage    `json:"stage"`
	Duration      time.Duration                 `json:"duration"`
	Units         int                           `json:"units"`
	Status        string                        `json:"status"`
	RunIDs        []string                      `json:"run_ids"`
	Counters      store.SemanticRefreshCounters `json:"counters"`
	RemainingDebt Debt                          `json:"remaining_debt"`
	ErrorCode     string                        `json:"error_code,omitempty"`
}

type RootExpectation struct {
	CacheDir, DatabaseID, ProfileID, GenerationID string
	DescriptorSHA256                              string
	SnapshotRevision, PurgeEpoch                  int64
	Dimensions                                    int
	BackendVersion                                string
}

type NativeLifecycle interface {
	semanticbuild.SegmentPayloadBuilder
	semanticbuild.StreamingSegmentPayloadBuilder
	VerifyRoot(context.Context, RootExpectation) error
}

type ProgressCallback func(Progress) error

type Request struct {
	ProfileID                       string
	PurgeEpoch, ProjectionWatermark int64
	Capability                      semanticindex.Capability
	Progress                        ProgressCallback
	Now                             func() time.Time
	NewRunIDFunc                    func() (string, error)
}

func (r Request) Validate() error {
	profileID := strings.TrimSpace(r.ProfileID)
	if profileID == "" || len(profileID) > 192 {
		return fmt.Errorf("semantic refresh profile ID must be between 1 and 192 bytes")
	}
	if r.PurgeEpoch < 0 {
		return fmt.Errorf("semantic refresh purge epoch must not be negative")
	}
	if r.ProjectionWatermark < 0 {
		return fmt.Errorf("semantic refresh projection watermark must not be negative")
	}
	if r.Capability.State != semanticindex.CapabilitySupportedReady ||
		strings.TrimSpace(r.Capability.Backend) == "" ||
		strings.TrimSpace(r.Capability.Version) == "" {
		return fmt.Errorf("semantic refresh requires a supported-ready native capability")
	}
	if r.Now == nil {
		return fmt.Errorf("semantic refresh clock is required")
	}
	if r.NewRunIDFunc == nil {
		return fmt.Errorf("semantic refresh run ID generator is required")
	}
	return nil
}

type StageOutcome struct {
	NextStage           store.SemanticRefreshStage
	Checkpoint          string
	EmbeddingRevision   int64
	Counters            store.SemanticRefreshCounters
	CurrentGenerationID string
	Readiness           string
	Debt                Debt
	Complete            bool
	SuccessorWatermark  *int64
}

type StageExecutor interface {
	Execute(context.Context, store.SemanticRefreshRun) (StageOutcome, error)
}
