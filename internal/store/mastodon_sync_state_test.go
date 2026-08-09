package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMastodonSyncStateRoundTripIsAccountAndOriginScoped(t *testing.T) {
	st := openTestStore(t)
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 8, 8, 15, 0, 0, 0, time.UTC)
	want := MastodonSyncState{
		AccountKey:          "hachyderm",
		CanonicalOrigin:     "https://hachyderm.io",
		VerifiedAccountID:   "42",
		Handle:              "alice@hachyderm.io",
		BackfillNextURL:     "https://hachyderm.io/api/v1/bookmarks?limit=40&max_id=opaque",
		BackfillComplete:    false,
		BackfillPageURL:     "https://hachyderm.io/api/v1/bookmarks?limit=40",
		BackfillPageDigest:  "page-digest",
		BackfillPageOffset:  7,
		BackfillIncremental: true,
		LastPageURL:         "https://hachyderm.io/api/v1/bookmarks?limit=40",
		LastPageDigest:      "sha256:page",
		CapabilitiesJSON:    `{"version":"4.3.1"}`,
		LastSuccessAt:       now,
		LastErrorAt:         now.Add(time.Minute),
		LastError:           "bounded error",
	}
	if err := st.UpsertMastodonSyncState(t.Context(), want); err != nil {
		t.Fatalf("UpsertMastodonSyncState: %v", err)
	}
	got, err := st.GetMastodonSyncState(t.Context(), want.AccountKey, want.CanonicalOrigin)
	if err != nil {
		t.Fatalf("GetMastodonSyncState: %v", err)
	}
	if got == nil || got.AccountKey != want.AccountKey || got.CanonicalOrigin != want.CanonicalOrigin || got.BackfillNextURL != want.BackfillNextURL || got.BackfillPageOffset != want.BackfillPageOffset || !got.BackfillIncremental || got.LastPageDigest != want.LastPageDigest || !got.LastSuccessAt.Equal(want.LastSuccessAt) {
		t.Fatalf("state = %#v, want %#v", got, want)
	}
	if err := st.UpsertMastodonSyncState(t.Context(), want); err != nil {
		t.Fatalf("idempotent UpsertMastodonSyncState: %v", err)
	}
	if err := st.UpsertMastodonSyncState(t.Context(), MastodonSyncState{AccountKey: "other", CanonicalOrigin: want.CanonicalOrigin, VerifiedAccountID: "42"}); err == nil {
		t.Fatal("expected verified account uniqueness error")
	}
}

func TestMastodonSyncStateMigrationCreatesTableAndIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openCurrentTestStoreAtPath(t, path)
	for _, name := range []string{"mastodon_sync_state", "idx_mastodon_sync_state_verified_account"} {
		var count int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name=?`, name).Scan(&count); err != nil {
			t.Fatalf("check %s: %v", name, err)
		}
		if count != 1 {
			t.Fatalf("schema object %s count=%d", name, count)
		}
	}
	if _, err := st.db.Exec(`DROP INDEX idx_mastodon_sync_state_verified_account`); err != nil {
		t.Fatalf("drop Mastodon state index: %v", err)
	}
	if _, err := st.db.Exec(`DROP TABLE mastodon_sync_state`); err != nil {
		t.Fatalf("drop Mastodon state table: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen stamped v30 store: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	for _, name := range []string{"mastodon_sync_state", "idx_mastodon_sync_state_verified_account"} {
		var count int
		if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name=?`, name).Scan(&count); err != nil {
			t.Fatalf("check repaired %s: %v", name, err)
		}
		if count != 1 {
			t.Fatalf("repaired schema object %s count=%d", name, count)
		}
	}
}

func TestMastodonSyncStateMigrationRebuildsDeficientStampedTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openCurrentTestStoreAtPath(t, path)
	if _, err := st.db.Exec(`DROP TABLE mastodon_sync_state`); err != nil {
		t.Fatalf("drop Mastodon state table: %v", err)
	}
	if _, err := st.db.Exec(`CREATE TABLE mastodon_sync_state (
		account_key TEXT NOT NULL,
		canonical_origin TEXT NOT NULL,
		verified_account_id TEXT NOT NULL DEFAULT '',
		handle TEXT NOT NULL DEFAULT '',
		backfill_next_url TEXT NOT NULL DEFAULT '',
		backfill_complete INTEGER NOT NULL DEFAULT 0,
		backfill_page_url TEXT NOT NULL DEFAULT '',
		backfill_page_digest TEXT NOT NULL DEFAULT '',
		backfill_page_offset INTEGER NOT NULL DEFAULT 0,
		last_page_url TEXT NOT NULL DEFAULT '',
		last_page_digest TEXT NOT NULL DEFAULT '',
		capabilities_json TEXT NOT NULL DEFAULT '',
		last_success_at TEXT NOT NULL DEFAULT '',
		last_error_at TEXT NOT NULL DEFAULT '',
		last_error TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create deficient Mastodon state table: %v", err)
	}
	if _, err := st.db.Exec(`INSERT INTO mastodon_sync_state (account_key, canonical_origin, verified_account_id, handle, last_page_url) VALUES (?, ?, ?, ?, ?)`, "hachyderm", "https://hachyderm.io:443", "42", "alice@hachyderm.io", "page-1"); err != nil {
		t.Fatalf("insert deficient Mastodon state: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close deficient store: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen deficient stamped v30 store: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	state, err := reopened.GetMastodonSyncState(t.Context(), "hachyderm", "https://hachyderm.io:443")
	if err != nil || state == nil || state.VerifiedAccountID != "42" || state.LastPageURL != "page-1" {
		t.Fatalf("repaired state = %#v, err=%v", state, err)
	}
	var accountPK, originPK, incrementalColumn int
	rows, err := reopened.db.Query(`PRAGMA table_info(mastodon_sync_state)`)
	if err != nil {
		t.Fatalf("inspect repaired Mastodon state columns: %v", err)
	}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			t.Fatalf("scan repaired Mastodon state column: %v", err)
		}
		if name == "account_key" {
			accountPK = primaryKey
		}
		if name == "canonical_origin" {
			originPK = primaryKey
		}
		if name == "backfill_incremental" {
			incrementalColumn++
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close repaired Mastodon state columns: %v", err)
	}
	if accountPK != 1 || originPK != 2 || incrementalColumn != 1 {
		t.Fatalf("repaired schema primary keys/column = account:%d origin:%d incremental:%d", accountPK, originPK, incrementalColumn)
	}
	if err := reopened.UpsertMastodonSyncState(t.Context(), MastodonSyncState{AccountKey: "other", CanonicalOrigin: "https://hachyderm.io:443", VerifiedAccountID: "42"}); err == nil {
		t.Fatal("repaired verified-account uniqueness constraint accepted duplicate identity")
	}
}

func TestMastodonSyncStateMigrationRejectsDuplicateIdentityDuringRepair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	st := openCurrentTestStoreAtPath(t, path)
	if _, err := st.db.Exec(`DROP TABLE mastodon_sync_state`); err != nil {
		t.Fatalf("drop Mastodon state table: %v", err)
	}
	if _, err := st.db.Exec(`CREATE TABLE mastodon_sync_state (account_key TEXT, canonical_origin TEXT, verified_account_id TEXT)`); err != nil {
		t.Fatalf("create deficient Mastodon state table: %v", err)
	}
	for _, account := range []string{"one", "two"} {
		if _, err := st.db.Exec(`INSERT INTO mastodon_sync_state (account_key, canonical_origin, verified_account_id) VALUES (?, ?, ?)`, account, "https://hachyderm.io:443", "42"); err != nil {
			t.Fatalf("insert duplicate Mastodon state %s: %v", account, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close deficient store: %v", err)
	}
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "duplicate verified identity") {
		t.Fatalf("duplicate identity repair error = %v, want explicit duplicate failure", err)
	}
}

func TestMastodonSyncStateConditionalWritesRejectChangedIdentityAndState(t *testing.T) {
	st := openTestStore(t)
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	expected := MastodonSyncState{
		AccountKey:        "hachyderm",
		CanonicalOrigin:   "https://hachyderm.io",
		VerifiedAccountID: "42",
		Handle:            "alice@hachyderm.io",
		BackfillComplete:  true,
		LastPageURL:       "head",
	}
	if err := st.UpsertMastodonSyncState(ctx, expected); err != nil {
		t.Fatalf("seed expected state: %v", err)
	}
	changed := expected
	changed.VerifiedAccountID = "99"
	changed.Handle = "replacement@hachyderm.io"
	if err := st.UpsertMastodonSyncState(ctx, changed); err != nil {
		t.Fatalf("seed changed identity: %v", err)
	}
	target := expected
	target.LastPageURL = "new-page"
	if err := st.UpsertMastodonSyncStateIfCurrent(ctx, target, &expected); err == nil {
		t.Fatal("expected conditional checkpoint to reject changed identity")
	}
	got, err := st.GetMastodonSyncState(ctx, expected.AccountKey, expected.CanonicalOrigin)
	if err != nil {
		t.Fatalf("load changed state: %v", err)
	}
	if got == nil || got.VerifiedAccountID != "99" || got.LastPageURL != "head" {
		t.Fatalf("conditional checkpoint overwrote changed state: %#v", got)
	}
	if err := st.RecordMastodonSyncErrorIfCurrent(ctx, expected.AccountKey, expected.CanonicalOrigin, "99", time.Now().UTC(), "diagnostic", got); err != nil {
		t.Fatalf("record error for current state: %v", err)
	}
	got, err = st.GetMastodonSyncState(ctx, expected.AccountKey, expected.CanonicalOrigin)
	if err != nil || got == nil || got.LastError != "diagnostic" || got.VerifiedAccountID != "99" {
		t.Fatalf("error recording state = %#v, err=%v", got, err)
	}
}

func TestResetMastodonSyncStateForVerifiedAccountUsesFullStateCAS(t *testing.T) {
	st := openTestStore(t)
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	expected := MastodonSyncState{
		AccountKey:         "hachyderm",
		CanonicalOrigin:    "https://hachyderm.io",
		VerifiedAccountID:  "42",
		BackfillNextURL:    "https://hachyderm.io/api/v1/bookmarks?max_id=old",
		BackfillPageURL:    "https://hachyderm.io/api/v1/bookmarks?max_id=old",
		BackfillPageDigest: "old-page",
		BackfillPageOffset: 3,
	}
	if err := st.UpsertMastodonSyncState(ctx, expected); err != nil {
		t.Fatalf("seed expected state: %v", err)
	}
	advanced := expected
	advanced.BackfillNextURL = "https://hachyderm.io/api/v1/bookmarks?max_id=new"
	advanced.BackfillPageURL = advanced.BackfillNextURL
	advanced.BackfillPageDigest = "new-page"
	advanced.BackfillPageOffset = 4
	if err := st.UpsertMastodonSyncState(ctx, advanced); err != nil {
		t.Fatalf("seed same-identity progress: %v", err)
	}
	if _, err := st.ResetMastodonSyncStateForVerifiedAccountIfCurrent(ctx, &expected); !errors.Is(err, ErrMastodonSyncStateChanged) {
		t.Fatalf("stale same-identity reset error = %v, want %v", err, ErrMastodonSyncStateChanged)
	}
	got, err := st.GetMastodonSyncState(ctx, expected.AccountKey, expected.CanonicalOrigin)
	if err != nil || got == nil || got.BackfillPageOffset != advanced.BackfillPageOffset || got.BackfillNextURL != advanced.BackfillNextURL {
		t.Fatalf("stale reset erased same-identity progress: state=%#v err=%v", got, err)
	}
	cleared, err := st.ResetMastodonSyncStateForVerifiedAccountIfCurrent(ctx, got)
	if err != nil {
		t.Fatalf("current reset: %v", err)
	}
	if cleared == nil || cleared.VerifiedAccountID != expected.VerifiedAccountID || cleared.BackfillPageOffset != 0 || cleared.BackfillNextURL != "" {
		t.Fatalf("cleared snapshot = %#v", cleared)
	}
}

func TestResetMastodonSyncStatePreservesVerifiedIdentity(t *testing.T) {
	st := openTestStore(t)
	defer func() { _ = st.Close() }()

	state := MastodonSyncState{
		AccountKey:         "hachyderm",
		CanonicalOrigin:    "https://hachyderm.io",
		VerifiedAccountID:  "42",
		BackfillNextURL:    "https://hachyderm.io/api/v1/bookmarks?max_id=stale",
		BackfillComplete:   true,
		BackfillPageURL:    "page",
		BackfillPageDigest: "digest",
		BackfillPageOffset: 4,
		LastPageURL:        "last-page",
		LastPageDigest:     "last-digest",
		LastSuccessAt:      time.Now().UTC(),
	}
	if err := st.UpsertMastodonSyncState(t.Context(), state); err != nil {
		t.Fatalf("UpsertMastodonSyncState: %v", err)
	}
	if err := st.ResetMastodonSyncState(t.Context(), state.AccountKey, state.CanonicalOrigin); err != nil {
		t.Fatalf("ResetMastodonSyncState: %v", err)
	}
	got, err := st.GetMastodonSyncState(t.Context(), state.AccountKey, state.CanonicalOrigin)
	if err != nil {
		t.Fatalf("GetMastodonSyncState: %v", err)
	}
	if got == nil || got.VerifiedAccountID != state.VerifiedAccountID || got.BackfillNextURL != "" || got.BackfillComplete || got.BackfillPageOffset != 0 || got.LastPageURL != "" || got.LastPageDigest != "" {
		t.Fatalf("reset state = %#v", got)
	}
}
