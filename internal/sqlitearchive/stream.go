package sqlitearchive

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	brainstore "github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/vaultfs"
)

var (
	ErrStreamBudget      = errors.New("deep stream budget exhausted")
	ErrStreamInterrupted = errors.New("deep stream interrupted")
	ErrCandidateInvalid  = errors.New("archive candidate is invalid")
)

type StreamLimits struct {
	MaxCompressedBytes int64
	MaxDatabaseBytes   int64
	MaxTempBytes       int64
	ReadIdleTimeout    time.Duration
}

type CandidateValidation struct {
	QuickCheck               string
	ForeignKeyViolationCount int
	SchemaCompatibility      string
	MigrationCompatibility   string
}

type StreamResult struct {
	CompressedBytes          int64
	DecompressedBytes        int64
	QuickCheck               string
	ForeignKeyViolationCount int
	SchemaCompatibility      string
	MigrationCompatibility   string
}

type candidateValidator interface {
	Validate(context.Context, *os.File) (CandidateValidation, error)
}

type storeCandidateValidator struct {
	inspect  func(context.Context, string, bool) (brainstore.DatabaseIntegrity, error)
	validate func(context.Context, string) error
}

func (v storeCandidateValidator) Validate(ctx context.Context, candidate *os.File) (CandidateValidation, error) {
	path, err := descriptorPath(candidate)
	if err != nil {
		return CandidateValidation{}, err
	}
	inspect := v.inspect
	if inspect == nil {
		inspect = brainstore.InspectDatabaseReadOnly
	}
	validate := v.validate
	if validate == nil {
		validate = brainstore.ValidateRestorableDatabase
	}
	inspection, inspectErr := inspect(ctx, path, true)
	// Identity validation is deliberately a separate mandatory call. A
	// completed candidate is never accepted on quick_check/table guesses alone.
	restorableErr := validate(ctx, path)
	result := CandidateValidation{
		QuickCheck: inspection.QuickCheck, ForeignKeyViolationCount: inspection.ForeignKeyViolationCount,
		SchemaCompatibility: inspection.SchemaCompatibility, MigrationCompatibility: inspection.MigrationCompatibility,
	}
	if inspectErr != nil {
		return result, fmt.Errorf("inspect candidate database: %w", inspectErr)
	}
	if restorableErr != nil {
		return result, fmt.Errorf("validate restorable database: %w", restorableErr)
	}
	if inspection.QuickCheck != "ok" || inspection.QuickViolationCount > 0 || inspection.ForeignKeyViolationCount > 0 {
		return result, fmt.Errorf("candidate database integrity violation")
	}
	return result, nil
}

func StreamCandidate(ctx context.Context, body io.ReadCloser, temp *vaultfs.PrivateTemp, limits StreamLimits) (StreamResult, error) {
	return streamCandidate(ctx, body, temp, limits, storeCandidateValidator{})
}

func streamCandidate(ctx context.Context, body io.ReadCloser, temp *vaultfs.PrivateTemp, limits StreamLimits, validator candidateValidator) (StreamResult, error) {
	var result StreamResult
	if body == nil || temp == nil || validator == nil {
		return result, fmt.Errorf("deep stream capability unavailable")
	}
	if limits.MaxCompressedBytes <= 0 || limits.MaxDatabaseBytes <= 0 || limits.MaxTempBytes <= 0 || limits.ReadIdleTimeout <= 0 {
		return result, fmt.Errorf("invalid deep stream limits")
	}
	reader := newProgressReadCloser(ctx, body, limits.ReadIdleTimeout)
	defer func() { _ = reader.Close() }()

	archiveFile, err := temp.Create("candidate.db.gz")
	if err != nil {
		return result, err
	}
	compressedCap := min64(limits.MaxCompressedBytes, limits.MaxTempBytes)
	result.CompressedBytes, err = copyBounded(ctx, archiveFile, reader, compressedCap)
	closeErr := archiveFile.Close()
	if err != nil {
		return result, classifyDownloadError(ctx, err)
	}
	if closeErr != nil {
		return result, fmt.Errorf("close downloaded candidate: %w", closeErr)
	}
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("%w: %v", ErrStreamInterrupted, err)
	}

	archive, err := temp.Open("candidate.db.gz")
	if err != nil {
		return result, fmt.Errorf("open downloaded candidate: %w", err)
	}
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		_ = archive.Close()
		return result, fmt.Errorf("%w: compressed candidate format", ErrCandidateInvalid)
	}
	databaseFile, err := temp.Create("candidate.db")
	if err != nil {
		_ = gzipReader.Close()
		_ = archive.Close()
		return result, err
	}
	remainingTemp := limits.MaxTempBytes - result.CompressedBytes
	if remainingTemp <= 0 {
		_ = databaseFile.Close()
		_ = gzipReader.Close()
		_ = archive.Close()
		return result, ErrStreamBudget
	}
	result.DecompressedBytes, err = copyBounded(ctx, databaseFile, gzipReader, min64(limits.MaxDatabaseBytes, remainingTemp))
	databaseCloseErr := databaseFile.Close()
	gzipCloseErr := gzipReader.Close()
	archiveCloseErr := archive.Close()
	if err != nil {
		if errors.Is(err, ErrStreamBudget) {
			return result, err
		}
		if ctx.Err() != nil {
			return result, fmt.Errorf("%w: %v", ErrStreamInterrupted, ctx.Err())
		}
		return result, fmt.Errorf("%w: decompress candidate", ErrCandidateInvalid)
	}
	if databaseCloseErr != nil || gzipCloseErr != nil || archiveCloseErr != nil {
		return result, fmt.Errorf("%w: finalize candidate decompression", ErrCandidateInvalid)
	}

	candidate, err := temp.Open("candidate.db")
	if err != nil {
		return result, err
	}
	validation, validationErr := validator.Validate(ctx, candidate)
	_ = candidate.Close()
	result.QuickCheck = validation.QuickCheck
	result.ForeignKeyViolationCount = validation.ForeignKeyViolationCount
	result.SchemaCompatibility = validation.SchemaCompatibility
	result.MigrationCompatibility = validation.MigrationCompatibility
	if validationErr != nil {
		if ctx.Err() != nil {
			return result, fmt.Errorf("%w: %v", ErrStreamInterrupted, ctx.Err())
		}
		return result, fmt.Errorf("%w: database validation: %v", ErrCandidateInvalid, validationErr)
	}
	if validation.QuickCheck != "ok" || validation.ForeignKeyViolationCount > 0 ||
		(validation.SchemaCompatibility != "current_compatible" && validation.SchemaCompatibility != "legacy_compatible") ||
		(validation.MigrationCompatibility != "current_compatible" && validation.MigrationCompatibility != "legacy_compatible") {
		return result, fmt.Errorf("%w: database validation", ErrCandidateInvalid)
	}
	return result, nil
}

func copyBounded(ctx context.Context, dst io.Writer, src io.Reader, limit int64) (int64, error) {
	if limit <= 0 {
		return 0, ErrStreamBudget
	}
	reader := contextReader{ctx: ctx, source: src}
	written, err := io.Copy(dst, io.LimitReader(reader, limit))
	if err != nil {
		return written, err
	}
	var probe [1]byte
	for emptyReads := 0; ; emptyReads++ {
		n, probeErr := reader.Read(probe[:])
		if n > 0 {
			return written, ErrStreamBudget
		}
		if errors.Is(probeErr, io.EOF) {
			return written, nil
		}
		if probeErr != nil {
			return written, probeErr
		}
		if emptyReads == 99 {
			return written, io.ErrNoProgress
		}
	}
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}

func classifyDownloadError(ctx context.Context, err error) error {
	if errors.Is(err, ErrStreamBudget) {
		return err
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %v", ErrStreamInterrupted, ctx.Err())
	}
	return fmt.Errorf("%w: download candidate", ErrStreamInterrupted)
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

type progressReadCloser struct {
	mu        sync.Mutex
	body      io.ReadCloser
	timer     *time.Timer
	idle      time.Duration
	done      chan struct{}
	closeOnce sync.Once
}

func newProgressReadCloser(ctx context.Context, body io.ReadCloser, idle time.Duration) *progressReadCloser {
	r := &progressReadCloser{body: body, idle: idle, done: make(chan struct{})}
	r.mu.Lock()
	r.timer = time.AfterFunc(idle, func() { _ = r.Close() })
	r.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
			_ = r.Close()
		case <-r.done:
		}
	}()
	return r
}

func (r *progressReadCloser) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if n > 0 {
		r.mu.Lock()
		if r.timer != nil {
			r.timer.Reset(r.idle)
		}
		r.mu.Unlock()
	}
	return n, err
}

func (r *progressReadCloser) Close() error {
	var err error
	r.closeOnce.Do(func() {
		r.mu.Lock()
		if r.timer != nil {
			r.timer.Stop()
		}
		r.mu.Unlock()
		close(r.done)
		err = r.body.Close()
	})
	return err
}
