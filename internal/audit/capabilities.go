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
}

type KindPartition struct {
	Kind                                                        string
	Total, Current, Pending, Blocked, Terminal, Failed, Unknown int
	PartitionValid                                              bool
}
type PipelineEvidence struct {
	Total, Current, Pending, Blocked, Terminal, Failed, Unknown int
	PartitionValid                                              bool
	OldestPendingAge                                            time.Duration
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
	Features Features
	Store    StoreSnapshot
	Database DatabaseInspector
	Metrics  MetricsReader
	Archives ArchiveLister
	Media    MediaArchiveInspector
	OKF      OKFInspector
	Runtime  RuntimeVersion
	Clock    func() time.Time
}
