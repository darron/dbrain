package sqlitearchive

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vaultfs"
)

type streamReadCloser struct{ io.Reader }

func (streamReadCloser) Close() error { return nil }

type blockingReadCloser struct {
	closed chan struct{}
	once   sync.Once
}

type cancelAfterRead struct {
	cancel context.CancelFunc
	done   bool
}

func (r *cancelAfterRead) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	for i := range p {
		p[i] = 'x'
	}
	r.cancel()
	return len(p), nil
}

func (b *blockingReadCloser) Read([]byte) (int, error) {
	<-b.closed
	return 0, errors.New("closed")
}
func (b *blockingReadCloser) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

type countingCandidateValidator struct{ calls int }

func (v *countingCandidateValidator) Validate(context.Context, *os.File) (CandidateValidation, error) {
	v.calls++
	return CandidateValidation{QuickCheck: "ok", IntegrityObserved: true, SchemaCompatibility: "current_compatible", SchemaObserved: true, MigrationCompatibility: "current_compatible", MigrationObserved: true}, nil
}

type quickViolationValidator struct{ calls int }

func (v *quickViolationValidator) Validate(context.Context, *os.File) (CandidateValidation, error) {
	v.calls++
	return CandidateValidation{QuickCheck: "violation", IntegrityObserved: true, ForeignKeyViolationCount: 0, SchemaCompatibility: "current_compatible", SchemaObserved: true, MigrationCompatibility: "current_compatible", MigrationObserved: true}, nil
}

type operationalCandidateValidator struct{ err error }

func (v operationalCandidateValidator) Validate(context.Context, *os.File) (CandidateValidation, error) {
	return CandidateValidation{}, v.err
}

type closingCandidateValidator struct{}

func (closingCandidateValidator) Validate(_ context.Context, file *os.File) (CandidateValidation, error) {
	if err := file.Close(); err != nil {
		return CandidateValidation{}, err
	}
	return CandidateValidation{QuickCheck: "ok", IntegrityObserved: true, SchemaCompatibility: "current_compatible", SchemaObserved: true, MigrationCompatibility: "current_compatible", MigrationObserved: true}, nil
}

func TestStoreCandidateValidatorAlwaysRunsInspectionAndRestoreIdentityValidation(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "candidate-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	inspectCalls, identityCalls := 0, 0
	validator := storeCandidateValidator{
		inspect: func(context.Context, string, bool) (store.DatabaseIntegrity, error) {
			inspectCalls++
			return store.DatabaseIntegrity{QuickCheckChecked: true, QuickCheck: "ok", SchemaCompatibility: "current_compatible", MigrationCompatibility: "current_compatible"}, nil
		},
		validate: func(context.Context, string) error {
			identityCalls++
			return nil
		},
	}
	if _, err := validator.Validate(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	if inspectCalls != 1 || identityCalls != 1 {
		t.Fatalf("inspection calls=%d identity calls=%d", inspectCalls, identityCalls)
	}
}

func TestStoreCandidateValidatorKeepsOperationalFailuresUnknown(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "candidate-*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	tests := []struct {
		name      string
		validator storeCandidateValidator
	}{
		{name: "descriptor", validator: storeCandidateValidator{descriptor: func(*os.File) (string, error) { return "", errors.New("descriptor unavailable") }}},
		{name: "inspect io", validator: storeCandidateValidator{
			descriptor: func(*os.File) (string, error) { return "candidate", nil },
			inspect: func(context.Context, string, bool) (store.DatabaseIntegrity, error) {
				return store.DatabaseIntegrity{}, errors.New("inspect io")
			},
			validate: func(context.Context, string) error { return nil },
		}},
		{name: "identity io", validator: storeCandidateValidator{
			descriptor: func(*os.File) (string, error) { return "candidate", nil },
			inspect: func(context.Context, string, bool) (store.DatabaseIntegrity, error) {
				return store.DatabaseIntegrity{QuickCheckChecked: true, QuickCheck: "ok", SchemaCompatibility: "current_compatible", MigrationCompatibility: "current_compatible"}, nil
			},
			validate: func(context.Context, string) error { return errors.New("identity io") },
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, gotErr := test.validator.Validate(t.Context(), file)
			if gotErr == nil || errors.Is(gotErr, ErrCandidateInvalid) {
				t.Fatalf("operational error classification = %v", gotErr)
			}
		})
	}
}

type stubGetClient struct {
	key string
}

func (s *stubGetClient) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	s.key = aws.ToString(input.Key)
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader([]byte("body")))}, nil
}

func TestS3ReaderHasGetOnlyCapability(t *testing.T) {
	client := &stubGetClient{}
	reader := newS3Reader("bucket", client)
	body, err := reader.Open(t.Context(), "archive/db/candidate.db.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	if client.key != "archive/db/candidate.db.gz" {
		t.Fatalf("key = %q", client.key)
	}
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := gzip.NewWriter(&out)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func currentDatabaseBytes(t *testing.T) []byte {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func quickCheckCorruptDatabaseBytes(t *testing.T) []byte {
	t.Helper()
	data := currentDatabaseBytes(t)
	path := filepath.Join(t.TempDir(), "quick-check-corrupt.db")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(sqliteDriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	var pageSize, rootPage int
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT rootpage FROM sqlite_master WHERE type = 'index' AND rootpage > 1 ORDER BY rootpage DESC LIMIT 1`).Scan(&rootPage); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	corruption := bytes.Repeat([]byte{0xff}, 64)
	if _, err := file.WriteAt(corruption, int64((rootPage-1)*pageSize)); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func candidateDatabaseBytes(t *testing.T, kind string) []byte {
	t.Helper()
	if kind == "current" || kind == "future" || kind == "migration_mismatch" || kind == "foreign_key" {
		data := currentDatabaseBytes(t)
		if kind == "current" {
			return data
		}
		path := filepath.Join(t.TempDir(), "candidate.db")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open(sqliteDriverName, path)
		if err != nil {
			t.Fatal(err)
		}
		switch kind {
		case "future":
			_, err = db.Exec(`PRAGMA user_version = 999`)
		case "migration_mismatch":
			_, err = db.Exec(`UPDATE schema_migrations SET name = 'wrong-name' WHERE version = 1`)
		case "foreign_key":
			_, err = db.Exec(`PRAGMA foreign_keys=OFF; CREATE TABLE audit_parent(id INTEGER PRIMARY KEY); CREATE TABLE audit_child(parent_id INTEGER REFERENCES audit_parent(id)); INSERT INTO audit_child(parent_id) VALUES(12345)`)
		}
		if err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		data, err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	path := filepath.Join(t.TempDir(), "candidate.db")
	db, err := sql.Open(sqliteDriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if kind == "legacy" {
		_, err = db.Exec(`
CREATE TABLE items(source_key TEXT, source_type TEXT, external_id TEXT, canonical_url TEXT, content_hash TEXT, note_path TEXT, raw_json TEXT, imported_at TEXT, updated_at TEXT, last_seen_at TEXT);
CREATE TABLE sources(source_key TEXT, canonical_url TEXT, normalized_url TEXT, source_type TEXT, extracted_text TEXT, summary_text TEXT, content_hash TEXT, note_path TEXT, created_at TEXT, updated_at TEXT);
`)
	} else {
		_, err = db.Exec(`CREATE TABLE foreign_table(id INTEGER PRIMARY KEY)`)
	}
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestStreamCandidateValidatesCompletedDatabaseAndEnforcesByteCeilings(t *testing.T) {
	database := currentDatabaseBytes(t)
	archive := gzipBytes(t, database)
	tests := []struct {
		name   string
		limits StreamLimits
	}{
		{"compressed", StreamLimits{MaxCompressedBytes: int64(len(archive) - 1), MaxDatabaseBytes: int64(len(database) + 1), MaxTempBytes: int64(len(archive) + len(database) + 1), ReadIdleTimeout: time.Second}},
		{"decompressed", StreamLimits{MaxCompressedBytes: int64(len(archive) + 1), MaxDatabaseBytes: int64(len(database) - 1), MaxTempBytes: int64(len(archive) + len(database) + 1), ReadIdleTimeout: time.Second}},
		{"temporary", StreamLimits{MaxCompressedBytes: int64(len(archive) + 1), MaxDatabaseBytes: int64(len(database) + 1), MaxTempBytes: int64(len(archive) + len(database) - 1), ReadIdleTimeout: time.Second}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmp, err := vaultfs.NewPrivateTemp(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tmp.Cleanup() }()
			validator := &countingCandidateValidator{}
			_, err = streamCandidate(t.Context(), streamReadCloser{bytes.NewReader(archive)}, tmp, test.limits, validator)
			if !errors.Is(err, ErrStreamBudget) {
				t.Fatalf("error = %v", err)
			}
			if validator.calls != 0 {
				t.Fatalf("validator calls = %d before completed candidate", validator.calls)
			}
			name := "candidate.db.gz"
			limit := test.limits.MaxCompressedBytes
			if test.name != "compressed" {
				name = "candidate.db"
				limit = test.limits.MaxDatabaseBytes
				if test.name == "temporary" {
					limit = test.limits.MaxTempBytes - int64(len(archive))
				}
			}
			file, openErr := tmp.Open(name)
			if openErr != nil {
				t.Fatal(openErr)
			}
			info, statErr := file.Stat()
			_ = file.Close()
			if statErr != nil {
				t.Fatal(statErr)
			}
			if info.Size() > limit {
				t.Fatalf("%s persisted %d bytes above hard limit %d", name, info.Size(), limit)
			}
		})
	}

	tmp, err := vaultfs.NewPrivateTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tmp.Cleanup() }()
	validator := &countingCandidateValidator{}
	result, err := streamCandidate(t.Context(), streamReadCloser{bytes.NewReader(archive)}, tmp, StreamLimits{
		MaxCompressedBytes: int64(len(archive) + 1), MaxDatabaseBytes: int64(len(database) + 1),
		MaxTempBytes: int64(len(archive) + len(database) + 1), ReadIdleTimeout: time.Second,
	}, validator)
	if err != nil {
		t.Fatal(err)
	}
	if validator.calls != 1 || result.CompressedBytes != int64(len(archive)) || result.DecompressedBytes != int64(len(database)) || result.QuickCheck != "ok" {
		t.Fatalf("result=%#v validator calls=%d", result, validator.calls)
	}
}

func TestCopyBoundedStopsOnContextCancellationWithoutExceedingLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	reader := &cancelAfterRead{cancel: cancel}
	var output bytes.Buffer
	written, err := copyBounded(ctx, &output, reader, 1024)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if written > 1024 || int64(output.Len()) > 1024 {
		t.Fatalf("written=%d persisted=%d", written, output.Len())
	}
}

type cancelingWriter struct {
	cancel context.CancelFunc
	wrote  bool
}

func (w *cancelingWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.wrote = true
		w.cancel()
	}
	return len(p), nil
}

func TestCopyBoundedStopsDuringLocalGzipDecompression(t *testing.T) {
	archive := gzipBytes(t, bytes.Repeat([]byte("decompress-me"), 1<<18))
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gzipReader.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	written, err := copyBounded(ctx, &cancelingWriter{cancel: cancel}, gzipReader, 100<<20)
	if !errors.Is(err, context.Canceled) || written <= 0 {
		t.Fatalf("written=%d error=%v", written, err)
	}
}

func TestStreamCandidateClassifiesCorruptGzipAsInvalid(t *testing.T) {
	tmp, err := vaultfs.NewPrivateTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tmp.Cleanup() }()
	validator := &countingCandidateValidator{}
	_, err = streamCandidate(t.Context(), streamReadCloser{bytes.NewReader([]byte("not gzip"))}, tmp, StreamLimits{
		MaxCompressedBytes: 1024, MaxDatabaseBytes: 1024, MaxTempBytes: 2048, ReadIdleTimeout: time.Second,
	}, validator)
	if !errors.Is(err, ErrCandidateInvalid) || validator.calls != 0 {
		t.Fatalf("error=%v validator calls=%d", err, validator.calls)
	}
}

func TestCompressedContentErrorClassificationExcludesOperationalIO(t *testing.T) {
	for _, err := range []error{io.EOF, gzip.ErrChecksum, gzip.ErrHeader, io.ErrUnexpectedEOF, flate.CorruptInputError(17)} {
		if !isInvalidCompressedContent(err) {
			t.Fatalf("verified compressed-content error was not invalid: %T %v", err, err)
		}
	}
	if isInvalidCompressedContent(os.ErrClosed) {
		t.Fatal("operational file error classified as invalid content")
	}
}

func TestStreamCandidateReadIdleWatchdogInterruptsAndCleansUpstream(t *testing.T) {
	tmp, err := vaultfs.NewPrivateTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tmp.Cleanup() }()
	body := &blockingReadCloser{closed: make(chan struct{})}
	started := time.Now()
	_, err = StreamCandidate(t.Context(), body, tmp, StreamLimits{
		MaxCompressedBytes: 1024, MaxDatabaseBytes: 1024, MaxTempBytes: 2048, ReadIdleTimeout: 20 * time.Millisecond,
	})
	if !errors.Is(err, ErrStreamInterrupted) || time.Since(started) > time.Second {
		t.Fatalf("idle result err=%v elapsed=%s", err, time.Since(started))
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("idle watchdog did not close upstream body")
	}
}

func TestStreamCandidateAcceptsCurrentAndLegacyAndRejectsInvalidDatabaseIdentity(t *testing.T) {
	for _, kind := range []string{"current", "legacy", "foreign", "future", "migration_mismatch", "foreign_key"} {
		t.Run(kind, func(t *testing.T) {
			database := candidateDatabaseBytes(t, kind)
			archive := gzipBytes(t, database)
			tmp, err := vaultfs.NewPrivateTemp(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tmp.Cleanup() }()
			before := sha256.Sum256(database)
			result, err := StreamCandidate(t.Context(), streamReadCloser{bytes.NewReader(archive)}, tmp, StreamLimits{
				MaxCompressedBytes: int64(len(archive) + 1), MaxDatabaseBytes: int64(len(database) + 1),
				MaxTempBytes: int64(len(archive) + len(database) + 1), ReadIdleTimeout: time.Second,
			})
			if kind == "current" || kind == "legacy" {
				if err != nil || result.QuickCheck != "ok" || result.ForeignKeyViolationCount != 0 {
					t.Fatalf("valid result=%#v err=%v", result, err)
				}
			} else if !errors.Is(err, ErrCandidateInvalid) {
				t.Fatalf("invalid result=%#v err=%v", result, err)
			}
			after := sha256.Sum256(database)
			if before != after {
				t.Fatal("source/active database bytes changed")
			}
		})
	}
}

func TestStreamCandidateRejectsQuickCheckViolationAfterMandatoryValidation(t *testing.T) {
	database := candidateDatabaseBytes(t, "current")
	archive := gzipBytes(t, database)
	tmp, err := vaultfs.NewPrivateTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tmp.Cleanup() }()
	validator := &quickViolationValidator{}
	_, err = streamCandidate(t.Context(), streamReadCloser{bytes.NewReader(archive)}, tmp, StreamLimits{
		MaxCompressedBytes: int64(len(archive) + 1), MaxDatabaseBytes: int64(len(database) + 1),
		MaxTempBytes: int64(len(archive) + len(database) + 1), ReadIdleTimeout: time.Second,
	}, validator)
	if !errors.Is(err, ErrCandidateInvalid) || validator.calls != 1 {
		t.Fatalf("quick-check result err=%v validator calls=%d", err, validator.calls)
	}
}

func TestStreamCandidateKeepsValidatorIOFailureOperational(t *testing.T) {
	database := candidateDatabaseBytes(t, "current")
	archive := gzipBytes(t, database)
	tmp, err := vaultfs.NewPrivateTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tmp.Cleanup() }()
	_, err = streamCandidate(t.Context(), streamReadCloser{bytes.NewReader(archive)}, tmp, StreamLimits{
		MaxCompressedBytes: int64(len(archive) + 1), MaxDatabaseBytes: int64(len(database) + 1),
		MaxTempBytes: int64(len(archive) + len(database) + 1), ReadIdleTimeout: time.Second,
	}, operationalCandidateValidator{err: errors.New("validator io")})
	if err == nil || errors.Is(err, ErrCandidateInvalid) {
		t.Fatalf("validator IO error classification = %v", err)
	}
}

func TestStreamCandidateKeepsCandidateFinalizationFailureOperational(t *testing.T) {
	database := candidateDatabaseBytes(t, "current")
	archive := gzipBytes(t, database)
	tmp, err := vaultfs.NewPrivateTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tmp.Cleanup() }()
	_, err = streamCandidate(t.Context(), streamReadCloser{bytes.NewReader(archive)}, tmp, StreamLimits{
		MaxCompressedBytes: int64(len(archive) + 1), MaxDatabaseBytes: int64(len(database) + 1),
		MaxTempBytes: int64(len(archive) + len(database) + 1), ReadIdleTimeout: time.Second,
	}, closingCandidateValidator{})
	if err == nil || errors.Is(err, ErrCandidateInvalid) {
		t.Fatalf("candidate finalization classification = %v", err)
	}
}

func TestStreamCandidateRejectsRealSQLiteQuickCheckViolation(t *testing.T) {
	database := quickCheckCorruptDatabaseBytes(t)
	archive := gzipBytes(t, database)
	tmp, err := vaultfs.NewPrivateTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tmp.Cleanup() }()
	result, err := StreamCandidate(t.Context(), streamReadCloser{bytes.NewReader(archive)}, tmp, StreamLimits{
		MaxCompressedBytes: int64(len(archive) + 1), MaxDatabaseBytes: int64(len(database) + 1),
		MaxTempBytes: int64(len(archive) + len(database) + 1), ReadIdleTimeout: time.Second,
	})
	if !errors.Is(err, ErrCandidateInvalid) || result.QuickCheck != "violation" {
		t.Fatalf("quick-check result=%#v error=%v", result, err)
	}
}

func TestStreamCandidateNeverTouchesSeparateActiveDatabaseArtifacts(t *testing.T) {
	activeDir := t.TempDir()
	active := map[string][]byte{
		"brain.db":     candidateDatabaseBytes(t, "current"),
		"brain.db-wal": []byte("active-wal-sentinel"),
		"brain.db-shm": []byte("active-shm-sentinel"),
	}
	hashes := map[string][32]byte{}
	for name, data := range active {
		if err := os.WriteFile(filepath.Join(activeDir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
		hashes[name] = sha256.Sum256(data)
	}
	candidate := candidateDatabaseBytes(t, "legacy")
	archive := gzipBytes(t, candidate)
	tmp, err := vaultfs.NewPrivateTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tmp.Cleanup() }()
	if _, err := StreamCandidate(t.Context(), streamReadCloser{bytes.NewReader(archive)}, tmp, StreamLimits{
		MaxCompressedBytes: int64(len(archive) + 1), MaxDatabaseBytes: int64(len(candidate) + 1),
		MaxTempBytes: int64(len(archive) + len(candidate) + 1), ReadIdleTimeout: time.Second,
	}); err != nil {
		t.Fatal(err)
	}
	for name, before := range hashes {
		data, err := os.ReadFile(filepath.Join(activeDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if after := sha256.Sum256(data); after != before {
			t.Fatalf("active artifact %s changed", name)
		}
	}
}
