package app

import (
	"sort"

	"github.com/darron/dbrain/internal/audit"
)

const localAuditSchemaV1 = "dbrain.audit.local.v1"

type LocalAuditTarget struct {
	ConfigPath string `json:"config_path,omitempty"`
	Database   string `json:"database,omitempty"`
	Metrics    string `json:"metrics,omitempty"`
	OKFRoot    string `json:"okf_root,omitempty"`
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
	Target LocalAuditTarget        `json:"target"`
	Checks []LocalAuditCheckDetail `json:"checks"`
}

type LocalAuditWrapper struct {
	Schema       string            `json:"schema"`
	Report       audit.Report      `json:"report"`
	LocalDetails LocalAuditDetails `json:"local_details"`
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
		Report:       report,
		LocalDetails: LocalAuditDetails{Target: target, Checks: checks},
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
