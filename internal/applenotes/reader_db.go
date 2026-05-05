package applenotes

import (
	"context"
	"database/sql"
	"fmt"
)

func loadObjectRows(ctx context.Context, db *sql.DB) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, `SELECT * FROM ZICCLOUDSYNCINGOBJECT`)
	if err != nil {
		return nil, fmt.Errorf("read Apple Notes objects: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("load Apple Notes object columns: %w", err)
	}
	var objectRows []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		scan := make([]any, len(columns))
		for i := range values {
			scan[i] = &values[i]
		}
		if err := rows.Scan(scan...); err != nil {
			return nil, fmt.Errorf("scan Apple Notes object: %w", err)
		}
		objectRows = append(objectRows, valuesByColumn(columns, values))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Apple Notes objects: %w", err)
	}
	return objectRows, nil
}

func loadNoteData(ctx context.Context, db *sql.DB) (map[int64][]byte, map[int64][]byte) {
	columns, err := tableColumns(ctx, db, "ZICNOTEDATA")
	if err != nil || len(columns) == 0 {
		return nil, nil
	}
	if firstColumn(columns, "ZDATA") == "" {
		return nil, nil
	}

	rows, err := db.QueryContext(ctx, `SELECT * FROM ZICNOTEDATA`)
	if err != nil {
		return nil, nil
	}
	defer func() {
		_ = rows.Close()
	}()
	resultColumns, err := rows.Columns()
	if err != nil {
		return nil, nil
	}
	byNotePK := map[int64][]byte{}
	byDataPK := map[int64][]byte{}
	for rows.Next() {
		values := make([]any, len(resultColumns))
		scan := make([]any, len(resultColumns))
		for i := range values {
			scan[i] = &values[i]
		}
		if err := rows.Scan(scan...); err != nil {
			return nil, nil
		}
		row := valuesByColumn(resultColumns, values)
		data := bytesValue(row, "ZDATA")
		if len(data) == 0 {
			continue
		}
		if pk, ok := int64Value(row, "Z_PK"); ok {
			byDataPK[pk] = data
		}
		if notePK, ok := int64Value(row, "ZNOTE"); ok {
			byNotePK[notePK] = data
		}
	}
	return byNotePK, byDataPK
}

func bodyDataForRow(row map[string]any, pk int64, bodyByNotePK map[int64][]byte, bodyByDataPK map[int64][]byte) []byte {
	if data := bytesValue(row, "ZDATA"); len(data) > 0 {
		return data
	}
	if data, ok := bodyByNotePK[pk]; ok {
		return data
	}
	if dataPK, ok := int64Value(row, "ZNOTEDATA"); ok {
		if data, ok := bodyByDataPK[dataPK]; ok {
			return data
		}
	}
	if data, ok := bodyByDataPK[pk]; ok {
		return data
	}
	return nil
}
