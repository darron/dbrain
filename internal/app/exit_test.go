package app

import (
	"errors"
	"testing"
)

func TestExitErrorCarriesCodeCauseAndSilence(t *testing.T) {
	cause := errors.New("cause")
	err := &ExitError{Code: 2, Err: cause, Silent: true}
	if err.Error() != "cause" || !errors.Is(err, cause) {
		t.Fatalf("exit error = %#v", err)
	}
}
