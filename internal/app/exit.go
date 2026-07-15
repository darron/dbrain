package app

// ExitError carries the process exit code independently from the diagnostic
// error. Silent is used after a command has already emitted its complete
// machine-readable result.
type ExitError struct {
	Code   int
	Err    error
	Silent bool
}

func (e *ExitError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
