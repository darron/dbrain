package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/okf"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/semanticreadiness"
	"github.com/darron/dbrain/internal/sqlitearchive"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vaultfs"
)

type auditSnapshotAdapter struct{ snapshot *store.AuditReadSnapshot }

type auditSemanticInspector struct {
	rootDir           string
	resolveDiagnostic func(string) (semanticconfig.Config, error)
	capability        func() semanticindex.Capability
	readRuntime       func(context.Context, embedding.Profile, int, time.Time) (semanticreadiness.Snapshot, error)
	now               func() time.Time
}

func newAuditSemanticInspector(rootDir string, snapshot *store.AuditReadSnapshot) audit.SemanticInspector {
	if snapshot == nil {
		return nil
	}
	return auditSemanticInspector{
		rootDir: rootDir, resolveDiagnostic: semanticconfig.ResolveDiagnostic,
		capability: semanticindex.RuntimeCapability, readRuntime: snapshot.SemanticRuntimeReadinessSnapshotAt,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (i auditSemanticInspector) InspectAuditSemantic(ctx context.Context) (audit.SemanticAuditSnapshot, error) {
	resolve := i.resolveDiagnostic
	if resolve == nil {
		resolve = semanticconfig.ResolveDiagnostic
	}
	semantic, err := resolve(i.rootDir)
	if err != nil {
		return audit.SemanticAuditSnapshot{}, err
	}
	configured := strings.TrimSpace(semantic.Model) != "" && semantic.Dimensions > 0
	if !configured || semantic.Mode == semanticconfig.ModeOff {
		return audit.SemanticAuditSnapshot{Configured: configured, Backend: "none", Readiness: "disabled"}, nil
	}
	profile := semanticbuild.Profile(embedding.Info{
		Provider: string(semantic.Provider), Model: semantic.Model, Dimensions: semantic.Dimensions,
	})
	profileID, err := profile.ID()
	if err != nil {
		return audit.SemanticAuditSnapshot{}, err
	}
	capability := i.capability
	if capability == nil {
		capability = semanticindex.RuntimeCapability
	}
	runtimeCapability := capability()
	base := audit.SemanticAuditSnapshot{Configured: true, ProfileID: audit.SemanticProfileIdentifier(profileID)}
	switch runtimeCapability.State {
	case semanticindex.CapabilityUnsupported:
		base.Backend, base.Readiness = "unsupported", "unavailable"
		return base, nil
	case semanticindex.CapabilitySupportedBroken:
		base.Backend, base.Readiness = "ollama", "unavailable"
		return base, nil
	case semanticindex.CapabilitySupportedReady:
		base.CapabilityAvailable, base.Backend = true, "ollama"
	default:
		base.Backend, base.Readiness = "ollama", "unavailable"
		return base, nil
	}
	if i.readRuntime == nil {
		base.Readiness = "unavailable"
		return base, nil
	}
	now := time.Now().UTC()
	if i.now != nil {
		now = i.now().UTC()
	}
	runtimeSnapshot, err := i.readRuntime(ctx, profile, semantic.ExactFallbackMaxChunks, now)
	if err != nil {
		if errors.Is(err, store.ErrRetrievalUnavailable) {
			base.Readiness = "unavailable"
			return base, nil
		}
		return audit.SemanticAuditSnapshot{}, err
	}
	runtimeSnapshot.Configured = true
	runtimeSnapshot.Enabled = true
	runtimeSnapshot.ExactMaxChunks = semantic.ExactFallbackMaxChunks
	runtimeSnapshot.Now = now
	decision := semanticreadiness.Evaluate(runtimeSnapshot)
	if runtimeSnapshot.ActiveGenerationID != "" {
		if admitted, _ := runtimeCapability.Admit(runtimeSnapshot.ActiveGenerationBackend, runtimeSnapshot.ActiveGenerationBackendVersion); !admitted {
			decision.State = semanticreadiness.StateUnavailable
		}
	}
	base.ActiveGenerationID = boundedAuditSemanticIdentifier(runtimeSnapshot.ActiveGenerationID)
	if runtimeSnapshot.ActiveGenerationID != "" && base.ActiveGenerationID == "" {
		decision.State = semanticreadiness.StateCorrupt
	}
	base.Readiness = audit.SemanticReadiness(decision.State)
	base.DirtyParentCount = nonnegativeAuditSemanticCount(runtimeSnapshot.DirtyParents)
	base.PendingParentCount = nonnegativeAuditSemanticCount(runtimeSnapshot.PendingParents)
	base.DueEmbeddingCount = saturatingAuditSemanticSum(runtimeSnapshot.PendingEmbeddings, runtimeSnapshot.DueRetries)
	base.BlockedEmbeddingCount = nonnegativeAuditSemanticCount(runtimeSnapshot.BlockedEmbeddings)
	base.FailedEmbeddingCount = nonnegativeAuditSemanticCount(runtimeSnapshot.ErrorEmbeddings)
	base.IndexedVectorCount = nonnegativeAuditSemanticCount(runtimeSnapshot.ActiveIndexedCount)
	base.L0VectorCount = nonnegativeAuditSemanticCount(runtimeSnapshot.L0ReadyCount)
	base.TombstoneCount = nonnegativeAuditSemanticCount(runtimeSnapshot.ActiveTombstones)
	base.SegmentCount = nonnegativeAuditSemanticCount(runtimeSnapshot.ActiveSegmentCount)
	return base, nil
}

func boundedAuditSemanticIdentifier(value string) audit.SemanticGenerationIdentifier {
	identifier := audit.SemanticGenerationIdentifier(value)
	if !identifier.Valid() {
		return ""
	}
	return identifier
}

func nonnegativeAuditSemanticCount(value int) int { return max(value, 0) }

func saturatingAuditSemanticSum(left, right int) int {
	left, right = nonnegativeAuditSemanticCount(left), nonnegativeAuditSemanticCount(right)
	if left > math.MaxInt-right {
		return math.MaxInt
	}
	return left + right
}

func (a auditSnapshotAdapter) Pipeline(ctx context.Context) (map[audit.PipelineStage]audit.PipelineEvidence, error) {
	partitions, err := a.snapshot.PipelinePartitions(ctx)
	if err != nil {
		return nil, err
	}
	return map[audit.PipelineStage]audit.PipelineEvidence{
		audit.PipelineHydration:     auditPipelineEvidence(partitions.Hydration),
		audit.PipelineExtraction:    auditPipelineEvidence(partitions.Extraction),
		audit.PipelineSummary:       auditPipelineEvidence(partitions.Summary),
		audit.PipelineTranscription: auditPipelineEvidence(partitions.Transcription),
		audit.PipelineOCR:           auditPipelineEvidence(partitions.OCR),
	}, nil
}

func auditPipelineEvidence(rows []store.PipelineStageRow) audit.PipelineEvidence {
	byKind := make([]audit.KindPartition, 0, min(len(rows), audit.MaxByKindEvidence))
	for _, row := range rows {
		if row.Kind == store.PipelineKindAll {
			continue
		}
		if len(byKind) == audit.MaxByKindEvidence {
			break
		}
		byKind = append(byKind, audit.KindPartition{Kind: row.Kind, Total: row.Total, Current: row.Current, Pending: row.Pending, Blocked: row.Blocked, Terminal: row.Terminal, Failed: row.Failed, Unknown: row.Unknown, PartitionValid: row.PartitionValid})
	}
	for _, row := range rows {
		if row.Kind == store.PipelineKindAll {
			return audit.PipelineEvidence{
				Total: row.Total, Current: row.Current, Pending: row.Pending, Blocked: row.Blocked,
				Terminal: row.Terminal, Failed: row.Failed, Unknown: row.Unknown,
				PartitionValid: row.PartitionValid, ByKind: byKind,
				OldestPendingAt: row.OldestPendingAt, OldestPendingKnown: row.OldestPendingKnown,
			}
		}
	}
	if len(rows) == 1 {
		row := rows[0]
		return audit.PipelineEvidence{
			Total: row.Total, Current: row.Current, Pending: row.Pending, Blocked: row.Blocked,
			Terminal: row.Terminal, Failed: row.Failed, Unknown: row.Unknown,
			PartitionValid: row.PartitionValid, ByKind: byKind,
			OldestPendingAt: row.OldestPendingAt, OldestPendingKnown: row.OldestPendingKnown,
		}
	}
	return audit.PipelineEvidence{PartitionValid: true, ByKind: byKind}
}

func (a auditSnapshotAdapter) Provenance(ctx context.Context) ([]audit.ProvenanceEvidence, error) {
	values, err := a.snapshot.Provenance(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]audit.ProvenanceEvidence, 0, len(values))
	for _, value := range values {
		missing := make(map[string]int, len(value.MissingByField))
		for key, count := range value.MissingByField {
			missing[key] = count
		}
		out = append(out, audit.ProvenanceEvidence{
			CheckID: audit.CheckID(value.CheckID), SuccessfulCount: value.SuccessfulCount,
			CompleteCount: value.CompleteCount, LegacyMissingCount: value.LegacyMissingCount,
			PostCutoverMissingCount: value.PostCutoverMissingCount, CutoverAt: value.CutoverAt,
			CutoverKnown: value.CutoverKnown, MissingByField: missing,
		})
	}
	return out, nil
}

func (a auditSnapshotAdapter) MediaLocal(ctx context.Context) (audit.MediaLocalEvidence, error) {
	value, err := a.snapshot.MediaLocalEvidence(ctx)
	return audit.MediaLocalEvidence{EligibleLocalCount: value.EligibleLocalCount, UncoveredPrunedCount: value.UncoveredPrunedCount, OrphanCount: value.OrphanCount}, err
}

func (a auditSnapshotAdapter) ArchivedMedia(ctx context.Context) ([]audit.ArchivedMediaRecord, error) {
	values, err := a.snapshot.ArchivedMediaRecords(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]audit.ArchivedMediaRecord, 0, len(values))
	for _, value := range values {
		out = append(out, audit.ArchivedMediaRecord{Key: value.Key, SizeBytes: value.SizeBytes, ArchivedAt: value.ArchivedAt, ArchivedAtValid: value.ArchivedAtValid})
	}
	return out, nil
}

func (a auditSnapshotAdapter) CountLocalIdentityMatches(ctx context.Context, source audit.Source, hashes []string) (int, error) {
	var local store.AuditSource
	switch source {
	case audit.SourceAppleNotes:
		local = store.AuditSourceAppleNotes
	case audit.SourceSafariTabs:
		local = store.AuditSourceSafariTabs
	case audit.SourceXBookmarks:
		local = store.AuditSourceXBookmarks
	case audit.SourceGitHubStars:
		local = store.AuditSourceGitHubStars
	case audit.SourceYouTubeLiked:
		local = store.AuditSourceYouTubeLiked
	case audit.SourceYouTubeWatchLater:
		local = store.AuditSourceYouTubeWatchLater
	case audit.SourceFeeds:
		local = store.AuditSourceFeeds
	default:
		return 0, fmt.Errorf("unsupported audit identity source %q", source)
	}
	return a.snapshot.CountLocalIdentityMatches(ctx, local, hashes)
}

type auditOKFInspector struct{ path string }

func (i auditOKFInspector) Inspect(ctx context.Context, full bool) (audit.OKFInspection, error) {
	root, err := vaultfs.Open(i.path)
	if err != nil {
		return audit.OKFInspection{}, err
	}
	defer func() { _ = root.Close() }()
	var value okf.InspectionSummary
	if full {
		value, err = okf.InspectBundle(ctx, root)
	} else {
		value, err = okf.InspectManifest(ctx, root)
	}
	return audit.OKFInspection{
		ManifestValid: value.ManifestValid, ExportedAt: value.ExportedAt, DocumentCount: value.DocumentCount,
		BrokenLinkCount: value.BrokenLinkCount, ValidationErrorCount: value.ValidationErrorCount,
		TraversalComplete: value.TraversalComplete,
	}, err
}

type auditMediaInspector struct{ inspector *mediaarchive.S3Inspector }

func (i auditMediaInspector) HeadObject(ctx context.Context, key string) (audit.ObjectMetadata, error) {
	value, err := i.inspector.HeadObject(ctx, key)
	return audit.ObjectMetadata{Exists: value.Exists, SizeBytes: value.SizeBytes}, err
}

type auditMediaInventory struct {
	inventory *mediaarchive.S3Inventory
	prefix    string
}

func (i auditMediaInventory) ListPage(ctx context.Context, token string, limit int) (audit.MediaInventoryPage, error) {
	value, err := i.inventory.ListPage(ctx, i.prefix, token, limit)
	objects := make([]audit.MediaInventoryObject, 0, len(value.Objects))
	for _, object := range value.Objects {
		objects = append(objects, audit.MediaInventoryObject{Key: object.Key, SizeBytes: object.SizeBytes})
	}
	return audit.MediaInventoryPage{Objects: objects, NextToken: value.NextToken, Complete: value.Complete}, err
}

type auditArchiveVerifier struct{}

func (auditArchiveVerifier) Verify(ctx context.Context, body io.ReadCloser, temp *vaultfs.PrivateTemp, limits audit.DeepLimits) (audit.DeepArchiveResult, error) {
	value, err := sqlitearchive.StreamCandidate(ctx, body, temp, sqlitearchive.StreamLimits{
		MaxCompressedBytes: limits.MaxArchiveBytes, MaxDatabaseBytes: limits.MaxDatabaseBytes,
		MaxTempBytes: limits.MaxTempBytes, ReadIdleTimeout: limits.ReadIdleTimeout,
	})
	result := audit.DeepArchiveResult{
		CompressedBytes: value.CompressedBytes, DecompressedBytes: value.DecompressedBytes,
		QuickCheck: value.QuickCheck, QuickCheckObserved: value.IntegrityObserved,
		ForeignKeyViolationCount: value.ForeignKeyViolationCount, ForeignKeysObserved: value.IntegrityObserved,
		SchemaCompatibility: value.SchemaCompatibility, SchemaObserved: value.SchemaObserved,
		MigrationCompatibility: value.MigrationCompatibility, MigrationObserved: value.MigrationObserved,
	}
	switch {
	case errors.Is(err, sqlitearchive.ErrCandidateInvalid):
		return result, fmt.Errorf("%w: candidate validation", audit.ErrDeepCandidateInvalid)
	case errors.Is(err, sqlitearchive.ErrStreamBudget):
		return result, fmt.Errorf("%w: stream ceiling", audit.ErrDeepBudget)
	case errors.Is(err, sqlitearchive.ErrStreamInterrupted):
		return result, fmt.Errorf("%w: stream interrupted", audit.ErrDeepInterrupted)
	default:
		return result, err
	}
}

type auditArchiveLister struct {
	inspector   *sqlitearchive.S3Inspector
	prefix      string
	pageLimit   int
	pageTimeout time.Duration
}

func (l auditArchiveLister) List(ctx context.Context) (audit.SQLiteArchiveListing, error) {
	objectLimit := 10_000
	pageLimit := l.pageLimit
	if pageLimit <= 0 {
		pageLimit = 100
	}
	if pageLimit > 100 {
		objectLimit = audit.DeepMaxObjects
	}
	value, err := l.inspector.ListObjectsBounded(ctx, l.prefix, objectLimit, pageLimit, l.pageTimeout)
	objects := make([]audit.ArchiveObject, 0, len(value.Objects))
	for _, object := range value.Objects {
		objects = append(objects, audit.ArchiveObject{Key: object.Key, ValidKey: sqlitearchive.IsSQLiteArchiveKey(object.Key, l.prefix), SizeBytes: object.Size, LastModified: object.LastModified})
	}
	return audit.SQLiteArchiveListing{ConfigurationState: "required_ready", Complete: value.Complete, Objects: objects}, err
}

func archiveRuntimeValues(ctx context.Context, root string) (mediaarchive.Options, error) {
	access, err := firstNonEmptySecret(ctx, root, "DBRAIN_R2_ACCESS_KEY_ID", "DBRAIN_S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID")
	if err != nil {
		return mediaarchive.Options{}, err
	}
	secret, err := firstNonEmptySecret(ctx, root, "DBRAIN_R2_SECRET_ACCESS_KEY", "DBRAIN_S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY")
	if err != nil {
		return mediaarchive.Options{}, err
	}
	token, err := firstNonEmptySecret(ctx, root, "DBRAIN_R2_SESSION_TOKEN", "DBRAIN_S3_SESSION_TOKEN", "AWS_SESSION_TOKEN")
	if err != nil {
		return mediaarchive.Options{}, err
	}
	region := firstNonEmptyEnv(root, "DBRAIN_R2_REGION", "DBRAIN_S3_REGION", "AWS_REGION", "AWS_DEFAULT_REGION")
	if strings.TrimSpace(region) == "" {
		region = "auto"
	}
	return mediaarchive.Options{
		Provider: firstNonEmptyEnv(root, "DBRAIN_ARCHIVE_PROVIDER", "DBRAIN_R2_PROVIDER"),
		Bucket:   firstNonEmptyEnv(root, "DBRAIN_R2_BUCKET", "DBRAIN_ARCHIVE_BUCKET", "DBRAIN_S3_BUCKET"),
		Endpoint: firstNonEmptyEnv(root, "DBRAIN_R2_ENDPOINT", "DBRAIN_S3_ENDPOINT"), Region: region,
		AccessKeyID: access, SecretKey: secret, SessionToken: token, PathStyle: true,
	}, nil
}
