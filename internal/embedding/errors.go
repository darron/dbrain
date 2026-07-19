package embedding

import (
	"errors"
	"fmt"
)

type FailureKind string

const (
	FailureRetryable   FailureKind = "retryable"
	FailureBlocked     FailureKind = "blocked"
	FailureFatalConfig FailureKind = "fatal_config"
)

type Failure struct {
	Kind FailureKind
	Err  error
}

func (e *Failure) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("embedding %s failure: %v", e.Kind, e.Err)
}

func (e *Failure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func RetryableError(err error) error {
	return failure(FailureRetryable, err)
}

func BlockedError(err error) error {
	return failure(FailureBlocked, err)
}

func FatalConfigError(err error) error {
	return failure(FailureFatalConfig, err)
}

func IsRetryable(err error) bool {
	return isFailureKind(err, FailureRetryable)
}

func IsBlocked(err error) bool {
	return isFailureKind(err, FailureBlocked)
}

func IsFatalConfig(err error) bool {
	return isFailureKind(err, FailureFatalConfig)
}

func failure(kind FailureKind, err error) error {
	if err == nil {
		err = errors.New("unspecified error")
	}
	return &Failure{Kind: kind, Err: err}
}

func isFailureKind(err error, kind FailureKind) bool {
	var failure *Failure
	return errors.As(err, &failure) && failure.Kind == kind
}
