package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestWithBusyRetryRetriesBusyErrors(t *testing.T) {
	t.Parallel()

	attempts := 0
	value, err := withBusyRetry(context.Background(), func() (int, error) {
		attempts++
		if attempts < 3 {
			return 0, fmt.Errorf("insert failed: database is locked (5) (SQLITE_BUSY)")
		}
		return 42, nil
	})
	if err != nil {
		t.Fatalf("withBusyRetry: %v", err)
	}
	if value != 42 {
		t.Fatalf("expected value 42, got %d", value)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestWithBusyRetryDoesNotRetryNonBusyErrors(t *testing.T) {
	t.Parallel()

	attempts := 0
	wantErr := errors.New("boom")
	_, err := withBusyRetry(context.Background(), func() (int, error) {
		attempts++
		return 0, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts)
	}
}

func TestIsBusyErrorRecognizesBusyAndLockedErrors(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"database is locked (5) (SQLITE_BUSY)",
		"database table is locked: items",
		"constraint failed: SQLITE_LOCKED",
	} {
		if !isBusyError(errors.New(value)) {
			t.Fatalf("expected busy error for %q", value)
		}
	}
	if isBusyError(errors.New("some other failure")) {
		t.Fatal("did not expect non-busy error to match")
	}
}
