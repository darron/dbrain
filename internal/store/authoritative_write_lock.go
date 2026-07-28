package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
)

type authoritativeWriteTx = *sql.Tx

type authoritativeWriteContextKey struct {
	_ byte
}

type authoritativeWriteContext struct {
	tx *sql.Tx
}

func withAuthoritativeWriteTx[T any](
	ctx context.Context,
	st *Store,
	metadata string,
	write func(context.Context, authoritativeWriteTx) (T, error),
) (result T, err error) {
	if st == nil || st.db == nil {
		return result, errors.New("authoritative write store is nil")
	}
	if write == nil {
		return result, errors.New("authoritative write callback is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if st.authoritativeWriteContextKey != nil {
		if active, ok := ctx.Value(st.authoritativeWriteContextKey).(*authoritativeWriteContext); ok && active != nil && active.tx != nil {
			return write(ctx, active.tx)
		}
	}

	var lease io.Closer
	if st.authoritativeWriteAcquire != nil {
		lease, err = st.authoritativeWriteAcquire(ctx, metadata)
		if err != nil {
			return result, fmt.Errorf("acquire authoritative semantic write lease: %w", err)
		}
	}

	release := func() error {
		if lease == nil {
			return nil
		}
		return lease.Close()
	}

	tx, beginErr := st.db.BeginTx(ctx, nil)
	if beginErr != nil {
		return result, errors.Join(
			fmt.Errorf("begin authoritative write transaction: %w", beginErr),
			wrapAuthoritativeWriteReleaseError(release()),
		)
	}

	finished := false
	defer func() {
		if !finished {
			_ = tx.Rollback()
			_ = release()
		}
	}()

	writeCtx := context.WithValue(ctx, st.authoritativeWriteContextKey, &authoritativeWriteContext{tx: tx})
	result, writeErr := write(writeCtx, tx)
	if writeErr != nil {
		rollbackErr := tx.Rollback()
		releaseErr := release()
		finished = true
		return result, errors.Join(
			writeErr,
			wrapAuthoritativeWriteRollbackError(rollbackErr),
			wrapAuthoritativeWriteReleaseError(releaseErr),
		)
	}

	commitErr := tx.Commit()
	releaseErr := release()
	finished = true
	return result, errors.Join(
		wrapAuthoritativeWriteCommitError(commitErr),
		wrapAuthoritativeWriteReleaseError(releaseErr),
	)
}

func wrapAuthoritativeWriteCommitError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("commit authoritative write transaction: %w", err)
}

func wrapAuthoritativeWriteRollbackError(err error) error {
	if err == nil || errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return fmt.Errorf("rollback authoritative write transaction: %w", err)
}

func wrapAuthoritativeWriteReleaseError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("release authoritative semantic write lease: %w", err)
}
