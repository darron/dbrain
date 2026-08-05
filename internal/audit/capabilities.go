package audit

import (
	"context"
	"time"

	"github.com/darron/dbrain/internal/metrics"
)

type PipelineStage string

const (
	PipelineHydration     PipelineStage = "hydration"
	PipelineExtraction    PipelineStage = "extraction"
	PipelineSummary       PipelineStage = "summary"
	PipelineTranscription PipelineStage = "transcription"
	PipelineOCR           PipelineStage = "ocr"
)

var allPipelineStages = []PipelineStage{PipelineHydration, PipelineExtraction, PipelineSummary, PipelineTranscription, PipelineOCR}
var allSources = []Source{SourceAppleNotes, SourceFeeds, SourceGitHubStars, SourceSafariTabs, SourceXBookmarks, SourceYouTubeLiked, SourceYouTubeWatchLater}

type Features struct {
	Layout                            string
	ConfigSource                      string
	ConfigVerified                    bool
	DatabaseOpenedQueryOnly           bool
	SchedulerEnabled                  bool
	SchedulerInterval                 time.Duration
	SchedulerJitter                   time.Duration
	SelectedStages                    []string
	Sources                           map[Source]bool
	Stages                            map[PipelineStage]bool
	MediaLocalEnabled                 bool
	MediaRemoteEnabled                bool
	MediaProvider                     string
	SQLiteArchiveCapabilityConfigured bool
	SQLiteBackupSchedulerEnabled      bool
	SQLiteBackupAuditRequired         bool
	SQLiteProviderConfigured          bool
	SQLiteCredentialConfigured        bool
	SQLiteResolutionError             bool
	OKFEnabled                        bool
	// SemanticConfigured is intentionally separate from feature eligibility:
	// semantic audit checks stay visible when disabled or unsupported.
	SemanticConfigured          bool
	SemanticCapabilityAvailable bool
	Timeouts                    map[TimeoutClass]time.Duration
	RemoteRequestTimeout        time.Duration
}

type KindPartition struct {
	Kind                                                        string
	Total, Current, Pending, Blocked, Terminal, Failed, Unknown int
	PartitionValid                                              bool
}
type PipelineEvidence struct {
	Total, Current, Pending, Blocked, Terminal, Failed, Unknown int
	PartitionValid                                              bool
	OldestPendingAt                                             time.Time
	OldestPendingKnown                                          bool
	ByKind                                                      []KindPartition
}
type ProvenanceEvidence struct {
	CheckID                                                                     CheckID
	SuccessfulCount, CompleteCount, LegacyMissingCount, PostCutoverMissingCount int
	CutoverAt                                                                   time.Time
	CutoverKnown                                                                bool
	MissingByField                                                              map[string]int
}
type MediaLocalEvidence struct{ EligibleLocalCount, UncoveredPrunedCount, OrphanCount int }
type ArchivedMediaRecord struct {
	Key             string
	SizeBytes       int64
	ArchivedAt      time.Time
	ArchivedAtValid bool
}

type StoreSnapshot interface {
	Pipeline(context.Context) (map[PipelineStage]PipelineEvidence, error)
	Provenance(context.Context) ([]ProvenanceEvidence, error)
	MediaLocal(context.Context) (MediaLocalEvidence, error)
	ArchivedMedia(context.Context) ([]ArchivedMediaRecord, error)
	CountLocalIdentityMatches(context.Context, Source, []string) (int, error)
}
type DatabaseInspection struct {
	SchemaCompatibility                           string
	MigrationCompatibility                        string
	MissingTableCount, MissingColumnCount         int
	QuickCheck                                    string
	QuickViolationCount, ForeignKeyViolationCount int
	UserVersion, SupportedVersion, AppliedCount   int
}
type DatabaseInspector interface {
	Inspect(context.Context, bool) (DatabaseInspection, error)
}
type MetricsReader interface {
	Read(context.Context, time.Time) (metrics.Window, error)
}
type OKFInspection struct {
	ManifestValid                                        bool
	ExportedAt                                           time.Time
	DocumentCount, BrokenLinkCount, ValidationErrorCount int
	TraversalComplete                                    bool
}
type OKFInspector interface {
	Inspect(context.Context, bool) (OKFInspection, error)
}
type ArchiveObject struct {
	Key          string
	ValidKey     bool
	SizeBytes    int64
	LastModified time.Time
}
type SQLiteArchiveListing struct {
	ConfigurationState string
	Complete           bool
	Objects            []ArchiveObject
}
type ArchiveLister interface {
	List(context.Context) (SQLiteArchiveListing, error)
}
type ObjectMetadata struct {
	Exists    bool
	SizeBytes int64
}
type MediaArchiveInspector interface {
	HeadObject(context.Context, string) (ObjectMetadata, error)
}

type RuntimeVersion struct {
	ReleaseVersion        string
	Commit                string
	GitStatus             string
	Platform              string
	SecurityBaselineID    string
	SecurityBaselineEpoch int
}

type Dependencies struct {
	Features       Features
	Store          StoreSnapshot
	Database       DatabaseInspector
	Metrics        MetricsReader
	Archives       ArchiveLister
	Media          MediaArchiveInspector
	MediaErrorCode ErrorCode
	OKF            OKFInspector
	Semantic       SemanticInspector
	Runtime        RuntimeVersion
	Clock          func() time.Time
}

// SemanticInspector is the bounded query-only seam populated by the app
// adapter. It deliberately carries only evidence fields declared by the v2
// registry; raw run IDs, paths, checkpoints, and error text do not cross it.
type SemanticInspector interface {
	InspectAuditSemantic(context.Context) (SemanticAuditSnapshot, error)
}

type SemanticAuditSnapshot struct {
	Configured, CapabilityAvailable bool
	Backend                         SemanticBackend
	ProfileID                       SemanticProfileIdentifier
	ActiveGenerationID              SemanticIdentifier
	Readiness                       SemanticReadiness
	DirtyParentCount                int
	PendingParentCount              int
	DueEmbeddingCount               int
	BlockedEmbeddingCount           int
	FailedEmbeddingCount            int
	IndexedVectorCount              int
	L0VectorCount                   int
	TombstoneCount                  int
	SegmentCount                    int
	Latest                          SemanticRefreshSnapshot
}

type SemanticRefreshSnapshot struct {
	State                                                         SemanticRefreshState
	StartedAt, CompletedAt, FailureAt                             time.Time
	Duration                                                      time.Duration
	ErrorCode                                                     SemanticErrorCode
	ProjectedParentCount, EmbeddedChunkCount                      int
	FlushedVectorCount, CompactedVectorCount, VerifiedVectorCount int
	SuccessorRunCount                                             int
	Stages                                                        []SemanticStageSnapshot
}

type SemanticStageSnapshot struct {
	Stage    SemanticStage
	Status   SemanticStageStatus
	Duration time.Duration
}

type SemanticBackend string
type SemanticProfileIdentifier string
type SemanticIdentifier string
type SemanticReadiness string
type SemanticRefreshState string
type SemanticErrorCode string
type SemanticStage string
type SemanticStageStatus string

// Valid applies the same closed, content-free identifier boundary enforced
// for semantic evidence before adapters bind store values to audit snapshots.
func (v SemanticIdentifier) Valid() bool { return semanticIdentifierPattern.MatchString(string(v)) }

func (v SemanticProfileIdentifier) Valid() bool {
	return semanticProfileIdentifierPattern.MatchString(string(v))
}

func (v SemanticBackend) Valid() bool { return v == "ollama" || v == "none" || v == "unsupported" }
func (v SemanticReadiness) Valid() bool {
	switch v {
	case "ready", "catching_up", "needs_projection", "needs_embeddings", "needs_index", "retry_scheduled", "building", "stale", "degraded_blocked", "corrupt", "disabled", "unavailable":
		return true
	default:
		return false
	}
}
func (v SemanticRefreshState) Valid() bool {
	switch v {
	case "succeeded", "failed", "canceled", "running", "skipped", "unsupported", "unknown":
		return true
	default:
		return false
	}
}
func (v SemanticStage) Valid() bool {
	switch v {
	case "projection", "embedding", "flush", "compaction", "verification", "readiness":
		return true
	default:
		return false
	}
}
func (v SemanticStageStatus) Valid() bool {
	switch v {
	case "succeeded", "failed", "canceled", "skipped", "unknown":
		return true
	default:
		return false
	}
}
func (v SemanticErrorCode) Valid() bool {
	switch v {
	case "semantic_backend_broken", "semantic_run_conflict", "semantic_projection_failed", "semantic_embedding_failed", "semantic_embedding_circuit_open", "semantic_flush_failed", "semantic_compaction_failed", "semantic_verify_failed", "semantic_native_root_failed", "semantic_readiness_not_ready", "semantic_lock_unavailable", "semantic_refresh_cancelled", "semantic_refresh_failed":
		return true
	default:
		return false
	}
}
