package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestRetrievalAvailableRequiresSemanticFoundationLedgerOnV15ReadOnly(t *testing.T) {
	path := semanticFoundationV15Database(t)
	control, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open v15 control connection: %v", err)
	}
	defer func() { _ = control.Close() }()

	var dataVersionBefore int
	if err := control.QueryRow(`PRAGMA data_version`).Scan(&dataVersionBefore); err != nil {
		t.Fatalf("read v15 data version: %v", err)
	}

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("open v15 database read-only: %v", err)
	}
	available, err := ro.RetrievalAvailable(context.Background())
	if err != nil {
		_ = ro.Close()
		t.Fatalf("check v15 retrieval availability: %v", err)
	}
	if available {
		_ = ro.Close()
		t.Fatal("v15 database without retrieval_parent_projections reported retrieval available")
	}
	_, err = ro.RetrievalStatus(context.Background(), "profile-a")
	if !errors.Is(err, ErrRetrievalUnavailable) {
		_ = ro.Close()
		t.Fatalf("v15 retrieval status error = %v, want ErrRetrievalUnavailable", err)
	}
	if err := ro.Close(); err != nil {
		t.Fatalf("close v15 read-only store: %v", err)
	}

	var dataVersionAfter, userVersion, ledgerTables int
	if err := control.QueryRow(`PRAGMA data_version`).Scan(&dataVersionAfter); err != nil {
		t.Fatalf("read v15 data version after retrieval probes: %v", err)
	}
	if err := control.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatalf("read v15 user_version after retrieval probes: %v", err)
	}
	if err := control.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table' AND name = 'retrieval_parent_projections'`).Scan(&ledgerTables); err != nil {
		t.Fatalf("inspect v15 semantic foundation ledger: %v", err)
	}
	if dataVersionAfter != dataVersionBefore || userVersion != 15 || ledgerTables != 0 {
		t.Fatalf("read-only retrieval probes changed v15 database: data_version %d -> %d, user_version %d, ledger tables %d",
			dataVersionBefore, dataVersionAfter, userVersion, ledgerTables)
	}
}
