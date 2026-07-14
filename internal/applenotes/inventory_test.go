package applenotes

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/config"

	_ "modernc.org/sqlite"
)

func TestAuditInventoryMatchesVisibleImportScopeWithoutReadingLockedMetadata(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := auditTestConfig(root)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	dbPath := filepath.Join(root, "NoteStore.sqlite")
	writeAuditNotesFixture(t, dbPath)

	inventory := NewAuditInventory(cfg, Options{
		DBPath:          dbPath,
		ExcludeFolders:  []string{"Archive"},
		ExcludeAccounts: []string{"Work"},
		ExcludeShared:   true,
		IncludeLocked:   true, // Audit privacy still must not project locked metadata.
	})
	result, err := inventory.Inventory(context.Background(), audit.InventoryBudget{MaxIdentities: 10, MaxPages: 1})
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	want, err := audit.HashUpstreamIdentity(audit.SourceAppleNotes, "apple-note:default:visible-note")
	if err != nil {
		t.Fatalf("HashUpstreamIdentity: %v", err)
	}
	if !result.Complete || result.PageCount != 1 || len(result.IdentityHashes) != 1 || result.IdentityHashes[0] != want {
		t.Fatalf("unexpected inventory: %+v", result)
	}
}

func TestAuditInventoryUsesPKFallbackAndDeduplicatesIdentity(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := auditTestConfig(root)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	dbPath := filepath.Join(root, "NoteStore.sqlite")
	writeSimpleAuditNotes(t, dbPath, []auditNoteRow{
		{PK: 7, Identifier: "", Snippet: "first"},
		{PK: 8, Identifier: "same", Snippet: "second"},
		{PK: 9, Identifier: "same", Snippet: "duplicate"},
	})

	result, err := NewAuditInventory(cfg, Options{DBPath: dbPath}).Inventory(context.Background(), audit.InventoryBudget{MaxIdentities: 2, MaxPages: 1})
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	wantPK, _ := audit.HashUpstreamIdentity(audit.SourceAppleNotes, "apple-note:default:7")
	wantSame, _ := audit.HashUpstreamIdentity(audit.SourceAppleNotes, "apple-note:default:same")
	if !result.Complete || len(result.IdentityHashes) != 2 || !containsHash(result.IdentityHashes, wantPK) || !containsHash(result.IdentityHashes, wantSame) {
		t.Fatalf("unexpected inventory: %+v", result)
	}
}

func TestAuditInventoryHonorsIgnoreMarkerThroughNoteDataBackReference(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := auditTestConfig(root)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	dbPath := filepath.Join(root, "NoteStore.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE ZICCLOUDSYNCINGOBJECT (Z_PK INTEGER, ZIDENTIFIER TEXT, ZTITLE1 TEXT, ZISPASSWORDPROTECTED INTEGER DEFAULT 0, ZMARKEDFORDELETION INTEGER DEFAULT 0)`,
		`CREATE TABLE ZICNOTEDATA (Z_PK INTEGER, ZNOTE INTEGER, ZDATA BLOB)`,
		`INSERT INTO ZICCLOUDSYNCINGOBJECT (Z_PK, ZIDENTIFIER, ZTITLE1) VALUES (1, 'body-note', 'Body Note')`,
		`INSERT INTO ZICCLOUDSYNCINGOBJECT (Z_PK, ZIDENTIFIER, ZTITLE1) VALUES (2, 'ignored-body-note', 'Ignored Body Note')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec fixture: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO ZICNOTEDATA (Z_PK, ZNOTE, ZDATA) VALUES (11, 1, ?), (12, 2, ?)`, encodedAuditNoteBody("ordinary decoded body"), encodedAuditNoteBody("prefix [[dbrain-ignore]] suffix")); err != nil {
		t.Fatalf("insert bodies: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	result, err := NewAuditInventory(cfg, Options{DBPath: dbPath}).Inventory(context.Background(), audit.InventoryBudget{MaxIdentities: 10, MaxPages: 1})
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	want, _ := audit.HashUpstreamIdentity(audit.SourceAppleNotes, "apple-note:default:body-note")
	if !result.Complete || len(result.IdentityHashes) != 1 || result.IdentityHashes[0] != want {
		t.Fatalf("unexpected inventory: %+v", result)
	}
}

func TestAuditInventoryUsesDecodedBodyBeforeSnippetForIgnorePolicy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := auditTestConfig(root)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	dbPath := filepath.Join(root, "NoteStore.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE ZICCLOUDSYNCINGOBJECT (
		Z_PK INTEGER, ZIDENTIFIER TEXT, ZTITLE1 TEXT, ZSNIPPET TEXT, ZDATA BLOB,
		ZISPASSWORDPROTECTED INTEGER DEFAULT 0, ZMARKEDFORDELETION INTEGER DEFAULT 0
	)`); err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO ZICCLOUDSYNCINGOBJECT (Z_PK, ZIDENTIFIER, ZTITLE1, ZSNIPPET, ZDATA) VALUES (1, 'decoded-wins', 'Decoded wins', '[[dbrain-ignore]] stale snippet', ?)`, encodedAuditNoteBody("ordinary decoded body")); err != nil {
		t.Fatalf("insert fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	result, err := NewAuditInventory(cfg, Options{DBPath: dbPath}).Inventory(context.Background(), audit.InventoryBudget{MaxIdentities: 1, MaxPages: 1})
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	want, _ := audit.HashUpstreamIdentity(audit.SourceAppleNotes, "apple-note:default:decoded-wins")
	if !result.Complete || len(result.IdentityHashes) != 1 || result.IdentityHashes[0] != want {
		t.Fatalf("unexpected inventory: %+v", result)
	}
}

func TestAuditInventoryHonorsLegacyAccountAndFolderColumns(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := auditTestConfig(root)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	dbPath := filepath.Join(root, "NoteStore.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE ZICCLOUDSYNCINGOBJECT (
			Z_PK INTEGER, ZIDENTIFIER TEXT, ZTITLE1 TEXT, ZSNIPPET TEXT, ZNAME TEXT, ZTITLE2 TEXT,
			ZISPASSWORDPROTECTED INTEGER DEFAULT 0, ZMARKEDFORDELETION INTEGER DEFAULT 0
		)`,
		`INSERT INTO ZICCLOUDSYNCINGOBJECT (Z_PK, ZIDENTIFIER, ZTITLE1, ZSNIPPET) VALUES (1, 'visible', 'Visible', 'visible')`,
		`INSERT INTO ZICCLOUDSYNCINGOBJECT (Z_PK, ZIDENTIFIER, ZTITLE1, ZSNIPPET, ZNAME) VALUES (2, 'work', 'Work', 'work', 'Work')`,
		`INSERT INTO ZICCLOUDSYNCINGOBJECT (Z_PK, ZIDENTIFIER, ZTITLE1, ZSNIPPET, ZTITLE2) VALUES (3, 'archive', 'Archive', 'archive', 'iCloud/Archive')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	result, err := NewAuditInventory(cfg, Options{DBPath: dbPath, ExcludeAccounts: []string{"Work"}, ExcludeFolders: []string{"Archive"}}).Inventory(context.Background(), audit.InventoryBudget{MaxIdentities: 1, MaxPages: 1})
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	want, _ := audit.HashUpstreamIdentity(audit.SourceAppleNotes, "apple-note:default:visible")
	if !result.Complete || len(result.IdentityHashes) != 1 || result.IdentityHashes[0] != want {
		t.Fatalf("unexpected inventory: %+v", result)
	}
}

func TestAuditInventoryUsesPerRowSparseIdentityAndExclusionFallbacks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := auditTestConfig(root)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	dbPath := filepath.Join(root, "NoteStore.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE ZICCLOUDSYNCINGOBJECT (
			Z_PK INTEGER, ZIDENTIFIER TEXT, ZSERVERRECORDID TEXT,
			ZTITLE1 TEXT, ZSNIPPET TEXT,
			ZACCOUNTNAME TEXT, ZACCOUNT TEXT, ZFOLDERPATH TEXT, ZFOLDER TEXT,
			ZISPASSWORDPROTECTED INTEGER DEFAULT 0, ZMARKEDFORDELETION INTEGER DEFAULT 0
		)`,
		`INSERT INTO ZICCLOUDSYNCINGOBJECT (Z_PK, ZIDENTIFIER, ZSERVERRECORDID, ZTITLE1, ZSNIPPET) VALUES (1, '', 'server-visible', 'Visible', 'visible')`,
		`INSERT INTO ZICCLOUDSYNCINGOBJECT (Z_PK, ZIDENTIFIER, ZSERVERRECORDID, ZTITLE1, ZSNIPPET, ZACCOUNTNAME, ZACCOUNT) VALUES (2, '', 'server-work', 'Work', 'work', '', 'Work')`,
		`INSERT INTO ZICCLOUDSYNCINGOBJECT (Z_PK, ZIDENTIFIER, ZSERVERRECORDID, ZTITLE1, ZSNIPPET, ZFOLDERPATH, ZFOLDER) VALUES (3, '', 'server-archive', 'Archive', 'archive', '', 'iCloud/Archive')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	result, err := NewAuditInventory(cfg, Options{
		DBPath: dbPath, ExcludeAccounts: []string{"Work"}, ExcludeFolders: []string{"Archive"},
	}).Inventory(context.Background(), audit.InventoryBudget{MaxIdentities: 1, MaxPages: 1})
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	want, _ := audit.HashUpstreamIdentity(audit.SourceAppleNotes, "apple-note:default:server-visible")
	if !result.Complete || len(result.IdentityHashes) != 1 || result.IdentityHashes[0] != want {
		t.Fatalf("unexpected inventory: %+v", result)
	}
}

func TestAuditInventoryCapPlusOneIsIncomplete(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := auditTestConfig(root)
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	dbPath := filepath.Join(root, "NoteStore.sqlite")
	writeSimpleAuditNotes(t, dbPath, []auditNoteRow{
		{PK: 1, Identifier: "one", Snippet: "one"},
		{PK: 2, Identifier: "two", Snippet: "two"},
	})

	result, err := NewAuditInventory(cfg, Options{DBPath: dbPath}).Inventory(context.Background(), audit.InventoryBudget{MaxIdentities: 1, MaxPages: 1})
	if !errors.Is(err, audit.ErrInventoryBudget) {
		t.Fatalf("error = %v, want ErrInventoryBudget", err)
	}
	if result.Complete || result.PageCount != 1 || len(result.IdentityHashes) != 1 {
		t.Fatalf("unexpected bounded result: %+v", result)
	}
}

func TestAuditInventoryRejectsInvalidBudgetBeforeSnapshotAndHonorsCancellation(t *testing.T) {
	t.Parallel()

	cfg := auditTestConfig(t.TempDir())
	inventory := NewAuditInventory(cfg, Options{DBPath: "/private/locked-note-secret.sqlite"})
	if _, err := inventory.Inventory(context.Background(), audit.InventoryBudget{}); !errors.Is(err, audit.ErrInventoryInvalid) || strings.Contains(err.Error(), "locked-note-secret") {
		t.Fatalf("invalid-budget error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := inventory.Inventory(ctx, audit.InventoryBudget{MaxIdentities: 1, MaxPages: 1}); !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "locked-note-secret") {
		t.Fatalf("canceled error = %v", err)
	}
}

func TestAuditInventorySnapshotFailureDoesNotLeakSourcePath(t *testing.T) {
	t.Parallel()

	secret := "private-notes-owner"
	cfg := auditTestConfig(t.TempDir())
	_, err := NewAuditInventory(cfg, Options{DBPath: filepath.Join(t.TempDir(), secret+".sqlite")}).Inventory(context.Background(), audit.InventoryBudget{MaxIdentities: 1, MaxPages: 1})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("privacy-safe snapshot error = %v", err)
	}
}

type auditNoteRow struct {
	PK         int
	Identifier string
	Snippet    string
}

func auditTestConfig(root string) config.Config {
	return config.Config{
		RootDir: root, ConfigDir: root, ConfigPath: filepath.Join(root, "config.yaml"),
		DataDir: filepath.Join(root, "data"), TempDir: filepath.Join(root, "tmp"),
		CacheDir: filepath.Join(root, "cache"), LogDir: filepath.Join(root, "logs"),
		VaultDir: filepath.Join(root, "vault"), MediaDir: filepath.Join(root, "vault", "media"),
		DBPath: filepath.Join(root, "data", "brain.db"),
	}
}

func writeSimpleAuditNotes(t *testing.T, path string, notes []auditNoteRow) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open notes fixture: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE ZICCLOUDSYNCINGOBJECT (
		Z_PK INTEGER, ZIDENTIFIER TEXT, ZTITLE1 TEXT, ZSNIPPET TEXT,
		ZISPASSWORDPROTECTED INTEGER DEFAULT 0, ZMARKEDFORDELETION INTEGER DEFAULT 0
	)`); err != nil {
		t.Fatalf("create notes fixture: %v", err)
	}
	for _, note := range notes {
		if _, err := db.Exec(`INSERT INTO ZICCLOUDSYNCINGOBJECT (Z_PK, ZIDENTIFIER, ZTITLE1, ZSNIPPET) VALUES (?, ?, 'note', ?)`, note.PK, note.Identifier, note.Snippet); err != nil {
			t.Fatalf("insert note: %v", err)
		}
	}
}

func writeAuditNotesFixture(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open notes fixture: %v", err)
	}
	defer func() { _ = db.Close() }()
	stmts := []string{
		`CREATE TABLE AUDIT_OBJECTS (
			Z_PK INTEGER,
			ZRAWIDENTIFIER TEXT,
			ZTITLE1 TEXT, ZSNIPPET TEXT, ZACCOUNTNAME TEXT, ZFOLDERPATH TEXT,
			ZISSHARED INTEGER DEFAULT 0, ZISPASSWORDPROTECTED INTEGER DEFAULT 0,
			ZMARKEDFORDELETION INTEGER DEFAULT 0, ZNOTE INTEGER, ZURL TEXT
		)`,
		`INSERT INTO AUDIT_OBJECTS (Z_PK, ZRAWIDENTIFIER, ZTITLE1, ZSNIPPET) VALUES (1, 'visible-note', 'Visible', 'ordinary body')`,
		`INSERT INTO AUDIT_OBJECTS (Z_PK, ZRAWIDENTIFIER, ZTITLE1, ZSNIPPET, ZISPASSWORDPROTECTED) VALUES (2, 'locked-private-identity', 'Locked private title', 'locked private body', 1)`,
		`INSERT INTO AUDIT_OBJECTS (Z_PK, ZRAWIDENTIFIER, ZTITLE1, ZSNIPPET, ZMARKEDFORDELETION) VALUES (3, 'deleted', 'Deleted', 'deleted', 1)`,
		`INSERT INTO AUDIT_OBJECTS (Z_PK, ZRAWIDENTIFIER, ZTITLE1, ZSNIPPET, ZACCOUNTNAME) VALUES (4, 'work', 'Work', 'work', 'Work')`,
		`INSERT INTO AUDIT_OBJECTS (Z_PK, ZRAWIDENTIFIER, ZTITLE1, ZSNIPPET, ZFOLDERPATH) VALUES (5, 'archive', 'Archive', 'archive', 'iCloud/Archive')`,
		`INSERT INTO AUDIT_OBJECTS (Z_PK, ZRAWIDENTIFIER, ZTITLE1, ZSNIPPET, ZISSHARED) VALUES (6, 'shared', 'Shared', 'shared', 1)`,
		`INSERT INTO AUDIT_OBJECTS (Z_PK, ZRAWIDENTIFIER, ZTITLE1, ZSNIPPET) VALUES (7, 'ignored', 'Ignored', 'prefix [[dbrain-ignore]] suffix')`,
		`INSERT INTO AUDIT_OBJECTS (Z_PK, ZRAWIDENTIFIER, ZTITLE1, ZNOTE) VALUES (8, 'attachment-private-name', 'Attachment masquerading as a note', 1)`,
		`INSERT INTO AUDIT_OBJECTS (Z_PK, ZRAWIDENTIFIER, ZTITLE1) VALUES (9, 'empty-blocked', 'Title only blocked note')`,
		`CREATE VIEW ZICCLOUDSYNCINGOBJECT AS
			SELECT Z_PK,
				CASE WHEN COALESCE(ZISPASSWORDPROTECTED, 0) != 0
				THEN json_extract('not-json', '$.identity') ELSE ZRAWIDENTIFIER END AS ZIDENTIFIER,
				ZTITLE1, ZSNIPPET, ZACCOUNTNAME, ZFOLDERPATH, ZISSHARED,
				ZISPASSWORDPROTECTED, ZMARKEDFORDELETION, ZNOTE, ZURL
			FROM AUDIT_OBJECTS`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec notes fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close notes fixture: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		t.Fatalf("notes fixture unreadable: %v", err)
	}
}

func containsHash(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func encodedAuditNoteBody(value string) []byte {
	data := []byte{0x0a, byte(len(value))}
	return append(data, []byte(value)...)
}
