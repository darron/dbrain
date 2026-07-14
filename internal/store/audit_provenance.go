package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

type AuditProvenanceEvidence struct {
	CheckID                 string
	SuccessfulCount         int
	CompleteCount           int
	LegacyMissingCount      int
	PostCutoverMissingCount int
	CutoverAt               time.Time
	CutoverKnown            bool
	MissingByField          map[string]int
}

type auditProvenanceSpec struct {
	checkID string
	query   string
	args    []any
	fields  []string
}

var auditProvenanceSpecs = []auditProvenanceSpec{
	{
		checkID: "pipeline.item_summary.provenance",
		query: `SELECT raw_json, model, prompt_version, tool, tool_version, input_hash, completed_at, updated_at
			FROM item_enrichments WHERE role = ? AND status = ?`,
		args:   []any{model.ItemEnrichmentRoleSummary, model.ItemSummaryStatusOK},
		fields: []string{"raw_json", "model", "prompt_version", "tool", "tool_version", "input_hash", "completed_at"},
	},
	{
		checkID: "pipeline.item_ocr.provenance",
		query: `SELECT raw_json, model, tool, tool_version, input_hash, completed_at, updated_at
			FROM item_enrichments WHERE role = ? AND status = ?`,
		args:   []any{model.ItemEnrichmentRoleOCR, model.ItemOCRStatusOK},
		fields: []string{"raw_json", "model", "tool", "tool_version", "input_hash", "completed_at"},
	},
	{
		checkID: "pipeline.x_media_transcript.provenance",
		query: `SELECT raw_json, model, tool, tool_version, input_hash, completed_at, updated_at
			FROM item_enrichments WHERE role = ? AND status = ?`,
		args:   []any{model.ItemEnrichmentRoleXMediaTranscript, model.XMediaTranscriptStatusOK},
		fields: []string{"raw_json", "model", "tool", "tool_version", "input_hash", "completed_at"},
	},
	{
		checkID: "pipeline.source_summary.provenance",
		query: `SELECT summary_json, summary_model, summary_prompt_version, summary_tool,
				summary_tool_version, content_hash, summarized_at, summarized_at
			FROM source_summary_versions WHERE summary_status = ?`,
		args:   []any{model.SourceSummaryStatusOK},
		fields: []string{"summary_json", "summary_model", "summary_prompt_version", "summary_tool", "summary_tool_version", "content_hash", "summarized_at"},
	},
}

func (s *AuditReadSnapshot) Provenance(ctx context.Context) ([]AuditProvenanceEvidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("audit read snapshot is closed")
	}
	tx := s.tx
	s.mu.Unlock()

	cutover, cutoverKnown, err := auditProvenanceCutover(ctx, tx)
	if err != nil {
		return nil, err
	}
	evidence := make([]AuditProvenanceEvidence, 0, len(auditProvenanceSpecs))
	for _, spec := range auditProvenanceSpecs {
		item, err := collectAuditProvenance(ctx, tx, spec, cutover, cutoverKnown)
		if err != nil {
			return nil, fmt.Errorf("collect %s: %w", spec.checkID, err)
		}
		evidence = append(evidence, item)
	}
	return evidence, nil
}

func auditProvenanceCutover(ctx context.Context, tx *sql.Tx) (time.Time, bool, error) {
	var migrationTableExists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM sqlite_master
			WHERE type = 'table' AND name = 'schema_migrations'
		)`).Scan(&migrationTableExists); err != nil {
		return time.Time{}, false, fmt.Errorf("check provenance cutover metadata: %w", err)
	}
	if !migrationTableExists {
		return time.Time{}, false, nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT version, name, applied_at
		FROM schema_migrations
		WHERE version = ? OR name = ?`, auditProvenanceMigrationVersion, auditProvenanceMigrationName)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("query provenance cutover migration: %w", err)
	}
	defer func() { _ = rows.Close() }()
	type migrationRow struct {
		version   int
		name      string
		appliedAt string
	}
	var found []migrationRow
	for rows.Next() {
		var row migrationRow
		if err := rows.Scan(&row.version, &row.name, &row.appliedAt); err != nil {
			return time.Time{}, false, fmt.Errorf("scan provenance cutover migration: %w", err)
		}
		found = append(found, row)
	}
	if err := rows.Err(); err != nil {
		return time.Time{}, false, fmt.Errorf("iterate provenance cutover migration: %w", err)
	}
	if len(found) != 1 || found[0].version != auditProvenanceMigrationVersion || found[0].name != auditProvenanceMigrationName {
		return time.Time{}, false, nil
	}
	cutover, err := time.Parse(time.RFC3339, strings.TrimSpace(found[0].appliedAt))
	if err != nil {
		return time.Time{}, false, nil
	}
	return cutover.UTC(), true, nil
}

func collectAuditProvenance(ctx context.Context, tx *sql.Tx, spec auditProvenanceSpec, cutover time.Time, cutoverKnown bool) (AuditProvenanceEvidence, error) {
	evidence := AuditProvenanceEvidence{
		CheckID: spec.checkID, CutoverAt: cutover, CutoverKnown: cutoverKnown,
		MissingByField: make(map[string]int, len(spec.fields)),
	}
	for _, field := range spec.fields {
		evidence.MissingByField[field] = 0
	}
	rows, err := tx.QueryContext(ctx, spec.query, spec.args...)
	if err != nil {
		return AuditProvenanceEvidence{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		values := make([]sql.NullString, len(spec.fields)+1)
		dest := make([]any, len(values))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return AuditProvenanceEvidence{}, err
		}
		evidence.SuccessfulCount++
		complete := true
		for i, field := range spec.fields {
			if !values[i].Valid || strings.TrimSpace(values[i].String) == "" {
				evidence.MissingByField[field]++
				complete = false
			}
		}
		if complete {
			evidence.CompleteCount++
			continue
		}
		if !cutoverKnown {
			continue
		}
		rowTime, err := time.Parse(time.RFC3339, strings.TrimSpace(values[len(spec.fields)].String))
		if err == nil && rowTime.Before(cutover) {
			evidence.LegacyMissingCount++
		} else {
			evidence.PostCutoverMissingCount++
		}
	}
	if err := rows.Err(); err != nil {
		return AuditProvenanceEvidence{}, err
	}
	return evidence, nil
}
