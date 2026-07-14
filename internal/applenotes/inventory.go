package applenotes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/config"
)

const appleNotesAuditMaxBodyBytes = 16 << 20

type auditInventory struct {
	cfg  config.Config
	opts Options
}

// NewAuditInventory returns a read-only Apple Notes inventory. It snapshots
// the live Notes SQLite triplet and never inspects attachment rows or files.
func NewAuditInventory(cfg config.Config, opts Options) audit.UpstreamInventory {
	return &auditInventory{cfg: cfg, opts: opts}
}

func (i *auditInventory) Inventory(ctx context.Context, budget audit.InventoryBudget) (audit.InventoryResult, error) {
	result := audit.InventoryResult{}
	if err := validateAppleNotesAuditBudget(budget); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if i == nil {
		return result, fmt.Errorf("%w: apple notes audit inventory unavailable", audit.ErrInventoryInvalid)
	}

	info, cleanup, err := CreateSnapshot(i.cfg, i.opts)
	if err != nil {
		return result, privacySafeAppleNotesAuditError(ctx, "snapshot", err)
	}
	defer func() {
		if cleanup != nil {
			_ = cleanup()
		}
	}()

	db, err := openSnapshotDB(info.DBPath)
	if err != nil {
		return result, privacySafeAppleNotesAuditError(ctx, "open snapshot", err)
	}
	defer func() { _ = db.Close() }()
	if err := validateSnapshotDB(ctx, db); err != nil {
		return result, privacySafeAppleNotesAuditError(ctx, "validate snapshot", err)
	}

	query, args, err := buildAppleNotesAuditQuery(ctx, db, i.opts)
	if err != nil {
		return result, privacySafeAppleNotesAuditError(ctx, "prepare inventory", err)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return result, privacySafeAppleNotesAuditError(ctx, "read inventory", err)
	}
	defer func() { _ = rows.Close() }()
	result.PageCount = 1

	columns, err := rows.Columns()
	if err != nil {
		return result, fmt.Errorf("%w: apple notes audit row shape", audit.ErrInventoryInvalid)
	}
	seen := make(map[string]struct{}, min(budget.MaxIdentities, 1024))
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			result.IdentityHashes = sortedAppleNotesAuditHashes(seen)
			return result, err
		}
		values := make([]any, len(columns))
		scan := make([]any, len(columns))
		for index := range values {
			scan[index] = &values[index]
		}
		if err := rows.Scan(scan...); err != nil {
			return result, privacySafeAppleNotesAuditError(ctx, "scan inventory", err)
		}
		row := valuesByColumn(columns, values)
		if matchesAny(firstStringValue(row, "audit_account"), i.opts.ExcludeAccounts) ||
			matchesAny(firstStringValue(row, "audit_folder"), i.opts.ExcludeFolders) {
			continue
		}
		body, err := appleNotesAuditBody(row)
		if err != nil {
			result.IdentityHashes = sortedAppleNotesAuditHashes(seen)
			return result, err
		}
		if strings.TrimSpace(body) == "" || containsIgnoreMarker(body) {
			continue
		}

		externalID := firstStringValue(row, "audit_identifier")
		if externalID == "" {
			pk, ok := int64Value(row, "audit_pk")
			if !ok || pk <= 0 {
				result.IdentityHashes = sortedAppleNotesAuditHashes(seen)
				return result, fmt.Errorf("%w: apple notes identity missing", audit.ErrInventoryInvalid)
			}
			externalID = strconv.FormatInt(pk, 10)
		}
		sourceKey := appleNoteSourceKey(externalID)
		hash, err := audit.HashUpstreamIdentity(audit.SourceAppleNotes, sourceKey)
		if err != nil {
			result.IdentityHashes = sortedAppleNotesAuditHashes(seen)
			return result, fmt.Errorf("%w: apple notes identity invalid", audit.ErrInventoryInvalid)
		}
		if _, exists := seen[hash]; exists {
			continue
		}
		if len(seen) == budget.MaxIdentities {
			result.IdentityHashes = sortedAppleNotesAuditHashes(seen)
			return result, fmt.Errorf("%w: apple notes identity budget exhausted", audit.ErrInventoryBudget)
		}
		seen[hash] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		result.IdentityHashes = sortedAppleNotesAuditHashes(seen)
		return result, privacySafeAppleNotesAuditError(ctx, "iterate inventory", err)
	}
	result.IdentityHashes = sortedAppleNotesAuditHashes(seen)
	result.Complete = true
	return result, nil
}

func validateAppleNotesAuditBudget(budget audit.InventoryBudget) error {
	if budget.MaxIdentities <= 0 || budget.MaxIdentities > audit.InventoryMaxIdentities || budget.MaxPages <= 0 || budget.MaxPages > audit.InventoryMaxPages {
		return fmt.Errorf("%w: apple notes inventory budget", audit.ErrInventoryInvalid)
	}
	return nil
}

func buildAppleNotesAuditQuery(ctx context.Context, db *sql.DB, opts Options) (string, []any, error) {
	columns, err := tableColumns(ctx, db, "ZICCLOUDSYNCINGOBJECT")
	if err != nil {
		return "", nil, err
	}
	if len(columns) == 0 {
		return "", nil, fmt.Errorf("%w: apple notes object table unavailable", audit.ErrInventoryInvalid)
	}

	selects := make([]string, 0, 10)
	if column := firstColumn(columns, "Z_PK"); column != "" {
		selects = append(selects, "o."+quoteIdent(column)+" AS audit_pk")
	}
	if column := firstColumn(columns, "ZIDENTIFIER", "ZSERVERRECORDID", "ZCLOUDKITRECORDID"); column != "" {
		selects = append(selects, "o."+quoteIdent(column)+" AS audit_identifier")
	}
	if len(selects) == 0 {
		return "", nil, fmt.Errorf("%w: apple notes identity columns unavailable", audit.ErrInventoryInvalid)
	}
	if len(opts.ExcludeAccounts) > 0 {
		if column := firstColumn(columns, "ZACCOUNTNAME", "ZACCOUNT", "ZNAME"); column != "" {
			selects = append(selects, "o."+quoteIdent(column)+" AS audit_account")
		}
	}
	if len(opts.ExcludeFolders) > 0 {
		if column := firstColumn(columns, "ZFOLDERPATH", "ZFOLDER", "ZTITLE2"); column != "" {
			selects = append(selects, "o."+quoteIdent(column)+" AS audit_folder")
		}
	}
	for index, name := range availableColumns(columns, "ZSNIPPET", "ZPLAINTEXT", "ZTEXT") {
		selects = append(selects, "o."+quoteIdent(name)+fmt.Sprintf(" AS audit_snippet_%d", index))
	}

	dataColumns, dataErr := tableColumns(ctx, db, "ZICNOTEDATA")
	if dataErr != nil {
		return "", nil, dataErr
	}
	if dataExpression := appleNotesAuditDataExpression(columns, dataColumns); dataExpression != "" {
		selects = append(selects, dataExpression+" AS audit_data")
	}

	predicates := make([]string, 0, 6)
	if column := firstColumn(columns, "ZISPASSWORDPROTECTED", "ZPASSWORDPROTECTED"); column != "" {
		predicates = append(predicates, falseBooleanSQL("o."+quoteIdent(column)))
	}
	if column := firstColumn(columns, "ZMARKEDFORDELETION", "ZDELETED"); column != "" {
		predicates = append(predicates, falseBooleanSQL("o."+quoteIdent(column)))
	}
	if opts.ExcludeShared {
		if column := firstColumn(columns, "ZISSHARED", "ZSHARED"); column != "" {
			predicates = append(predicates, falseBooleanSQL("o."+quoteIdent(column)))
		}
	}
	noteEvidence := make([]string, 0, 6)
	for _, name := range availableColumns(columns, "ZTITLE1", "ZSNIPPET", "ZPLAINTEXT", "ZTEXT") {
		noteEvidence = append(noteEvidence, "TRIM(CAST(o."+quoteIdent(name)+" AS TEXT)) <> ''")
	}
	for _, name := range availableColumns(columns, "ZNOTEDATA", "ZDATA") {
		noteEvidence = append(noteEvidence, "o."+quoteIdent(name)+" IS NOT NULL")
	}
	if len(noteEvidence) == 0 {
		return "", nil, fmt.Errorf("%w: apple notes note-shape columns unavailable", audit.ErrInventoryInvalid)
	}
	predicates = append(predicates, "("+strings.Join(noteEvidence, " OR ")+")")
	if attachmentPredicate := appleNotesAttachmentPredicate(columns); attachmentPredicate != "" {
		predicates = append(predicates, "NOT ("+attachmentPredicate+")")
	}

	query := "SELECT " + strings.Join(selects, ", ") +
		" FROM ZICCLOUDSYNCINGOBJECT o" +
		" WHERE " + strings.Join(predicates, " AND ")
	if pk := firstColumn(columns, "Z_PK"); pk != "" {
		query += " ORDER BY o." + quoteIdent(pk)
	}
	return query, nil, nil
}

func appleNotesAuditDataExpression(objectColumns, dataColumns []string) string {
	candidates := make([]string, 0, 4)
	if direct := firstColumn(objectColumns, "ZDATA"); direct != "" {
		candidates = append(candidates, nonEmptyAppleNotesDataSQL("o."+quoteIdent(direct)))
	}
	dataBody := firstColumn(dataColumns, "ZDATA")
	dataPK := firstColumn(dataColumns, "Z_PK")
	objectPK := firstColumn(objectColumns, "Z_PK")
	if dataBody != "" {
		if dataNote := firstColumn(dataColumns, "ZNOTE"); dataNote != "" && objectPK != "" {
			query := "(SELECT nd." + quoteIdent(dataBody) + " FROM ZICNOTEDATA nd WHERE nd." + quoteIdent(dataNote) + " = o." + quoteIdent(objectPK) + " AND LENGTH(nd." + quoteIdent(dataBody) + ") > 0"
			if dataPK != "" {
				query += " ORDER BY nd." + quoteIdent(dataPK)
			}
			candidates = append(candidates, query+" LIMIT 1)")
		}
		if objectDataFK := firstColumn(objectColumns, "ZNOTEDATA"); objectDataFK != "" && dataPK != "" {
			candidates = append(candidates, "(SELECT nd."+quoteIdent(dataBody)+" FROM ZICNOTEDATA nd WHERE nd."+quoteIdent(dataPK)+" = o."+quoteIdent(objectDataFK)+" AND LENGTH(nd."+quoteIdent(dataBody)+") > 0 LIMIT 1)")
		}
		if objectPK != "" && dataPK != "" {
			candidates = append(candidates, "(SELECT nd."+quoteIdent(dataBody)+" FROM ZICNOTEDATA nd WHERE nd."+quoteIdent(dataPK)+" = o."+quoteIdent(objectPK)+" AND LENGTH(nd."+quoteIdent(dataBody)+") > 0 LIMIT 1)")
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	return "COALESCE(" + strings.Join(candidates, ", ") + ")"
}

func nonEmptyAppleNotesDataSQL(expression string) string {
	return "CASE WHEN LENGTH(" + expression + ") > 0 THEN " + expression + " END"
}

func availableColumns(columns []string, names ...string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if actual := firstColumn(columns, name); actual != "" {
			out = append(out, actual)
		}
	}
	return out
}

func falseBooleanSQL(expression string) string {
	return "LOWER(TRIM(CAST(COALESCE(" + expression + ", 0) AS TEXT))) NOT IN ('1', 'true', 'yes')"
}

func appleNotesAttachmentPredicate(columns []string) string {
	parents := availableColumns(columns, "ZNOTE", "ZNOTE1", "ZPARENTNOTE", "ZATTACHEDTO", "ZOWNINGNOTE")
	if len(parents) == 0 {
		return ""
	}
	parentTests := make([]string, 0, len(parents))
	for _, name := range parents {
		parentTests = append(parentTests, "o."+quoteIdent(name)+" IS NOT NULL")
	}
	attachmentTests := make([]string, 0, 12)
	for _, name := range availableColumns(columns,
		"ZIDENTIFIER", "ZCONTENTID", "ZCONTENTIDENTIFIER", "ZFILENAME", "ZFILENAME1", "ZFILEURL", "ZURL", "ZURLSTRING", "ZMEDIAURL",
		"ZUTI", "ZUNIFORMTYPEIDENTIFIER", "ZTYPEUTI", "ZMIMETYPE", "ZATTACHMENT", "ZATTACHMENT1", "ZATTACHMENTIDENTIFIER",
		"ZADDITIONALINDEXABLETEXT", "ZALTTEXT", "ZINDEXABLETEXT", "ZTRANSCRIPT") {
		attachmentTests = append(attachmentTests, "TRIM(CAST(o."+quoteIdent(name)+" AS TEXT)) <> ''")
	}
	for _, name := range availableColumns(columns, "ZFILESIZE", "ZSIZEINBYTES", "ZBYTESIZE") {
		attachmentTests = append(attachmentTests, "COALESCE(o."+quoteIdent(name)+", 0) > 0")
	}
	if len(attachmentTests) == 0 {
		return ""
	}
	return "(" + strings.Join(parentTests, " OR ") + ") AND (" + strings.Join(attachmentTests, " OR ") + ")"
}

func appleNotesAuditBody(row map[string]any) (string, error) {
	data := bytesValue(row, "audit_data")
	if len(data) > appleNotesAuditMaxBodyBytes {
		return "", fmt.Errorf("%w: apple notes body exceeds audit bound", audit.ErrInventoryBudget)
	}
	if len(data) > 0 {
		if decoded, err := DecodeZData(data); err == nil && strings.TrimSpace(decoded) != "" {
			return strings.TrimSpace(decoded), nil
		}
	}
	return firstStringValue(row, "audit_snippet_0", "audit_snippet_1", "audit_snippet_2"), nil
}

func privacySafeAppleNotesAuditError(ctx context.Context, operation string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, audit.ErrInventoryBudget) || errors.Is(err, audit.ErrInventoryInvalid) {
		return err
	}
	return fmt.Errorf("apple notes audit %s failed", operation)
}

func sortedAppleNotesAuditHashes(seen map[string]struct{}) []string {
	hashes := make([]string, 0, len(seen))
	for hash := range seen {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	return hashes
}
