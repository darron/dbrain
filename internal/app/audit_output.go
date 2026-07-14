package app

import (
	"sort"

	"github.com/darron/dbrain/internal/audit"
)

const localAuditSchemaV1 = "dbrain.audit.local.v1"

type LocalArchiveTarget struct {
	Provider string `json:"provider,omitempty"`
	Bucket   string `json:"bucket,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
	Origin   string `json:"origin,omitempty"`
}

type LocalAuditTarget struct {
	ConfigPath    string             `json:"config_path,omitempty"`
	Database      string             `json:"database,omitempty"`
	Vault         string             `json:"vault,omitempty"`
	Metrics       string             `json:"metrics,omitempty"`
	Temporary     string             `json:"temporary,omitempty"`
	Media         string             `json:"media,omitempty"`
	OKFRoot       string             `json:"okf_root,omitempty"`
	MediaArchive  LocalArchiveTarget `json:"media_archive"`
	SQLiteArchive LocalArchiveTarget `json:"sqlite_archive"`
}

type LocalAuditIdentifiers struct {
	RowIDs       []int64  `json:"row_ids"`
	SourceKeys   []string `json:"source_keys"`
	CleanupPaths []string `json:"cleanup_paths"`
}

type LocalAuditCheckDetail struct {
	CheckID      audit.CheckID `json:"check_id"`
	RowIDs       []int64       `json:"row_ids"`
	SourceKeys   []string      `json:"source_keys"`
	CleanupPaths []string      `json:"cleanup_paths"`
	Truncated    bool          `json:"truncated"`
}

type LocalAuditDetails struct {
	Checks []LocalAuditCheckDetail `json:"checks"`
}

type LocalAuditWrapper struct {
	Schema       string            `json:"schema"`
	LocalTarget  LocalAuditTarget  `json:"local_target"`
	LocalDetails LocalAuditDetails `json:"local_details"`
	Report       audit.Report      `json:"report"`
}

func newLocalAuditWrapper(report audit.Report, target LocalAuditTarget, values map[audit.CheckID]LocalAuditIdentifiers) LocalAuditWrapper {
	ids := make([]audit.CheckID, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	checks := make([]LocalAuditCheckDetail, 0, len(ids))
	for _, id := range ids {
		value := values[id]
		sort.Slice(value.RowIDs, func(i, j int) bool { return value.RowIDs[i] < value.RowIDs[j] })
		sort.Strings(value.SourceKeys)
		sort.Strings(value.CleanupPaths)
		rowIDs, rowCut := boundedSlice(value.RowIDs, 100)
		sourceKeys, sourceCut := boundedSlice(value.SourceKeys, 100)
		cleanupPaths, cleanupCut := boundedSlice(value.CleanupPaths, 20)
		checks = append(checks, LocalAuditCheckDetail{
			CheckID: id, RowIDs: rowIDs, SourceKeys: sourceKeys,
			CleanupPaths: cleanupPaths, Truncated: rowCut || sourceCut || cleanupCut,
		})
	}
	return LocalAuditWrapper{
		Schema:       localAuditSchemaV1,
		LocalTarget:  target,
		Report:       report,
		LocalDetails: LocalAuditDetails{Checks: checks},
	}
}

func boundedSlice[T any](values []T, limit int) ([]T, bool) {
	if values == nil {
		return []T{}, false
	}
	cut := len(values) > limit
	if cut {
		values = values[:limit]
	}
	out := make([]T, len(values))
	copy(out, values)
	return out, cut
}
