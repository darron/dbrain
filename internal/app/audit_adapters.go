package app

import (
	"context"
	"strings"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/okf"
	"github.com/darron/dbrain/internal/sqlitearchive"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vaultfs"
)

type auditSnapshotAdapter struct{ snapshot *store.AuditReadSnapshot }

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
			}
		}
	}
	if len(rows) == 1 {
		row := rows[0]
		return audit.PipelineEvidence{
			Total: row.Total, Current: row.Current, Pending: row.Pending, Blocked: row.Blocked,
			Terminal: row.Terminal, Failed: row.Failed, Unknown: row.Unknown,
			PartitionValid: row.PartitionValid, ByKind: byKind,
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

type auditArchiveLister struct {
	inspector *sqlitearchive.S3Inspector
	prefix    string
}

func (l auditArchiveLister) List(ctx context.Context) (audit.SQLiteArchiveListing, error) {
	value, err := l.inspector.ListObjects(ctx, l.prefix, 10_000)
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
