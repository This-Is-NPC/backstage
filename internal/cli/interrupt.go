package cli

import (
	"context"
	"errors"
)

const interruptExitCode = 130

type interruptError struct {
	err error
}

func (e interruptError) Error() string {
	if e.err == nil {
		return "interrupted"
	}
	return e.err.Error()
}

func (e interruptError) Unwrap() error {
	return e.err
}

func (e interruptError) ExitCode() int {
	return interruptExitCode
}

func interrupted(err error) error {
	if err == nil {
		err = context.Canceled
	}
	var ie interruptError
	if errors.As(err, &ie) {
		return err
	}
	return interruptError{err: err}
}

func takeInterrupted(err error, ctx context.Context) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	return ctx != nil && ctx.Err() != nil
}

// ExitStatus is the process exit code for a CLI error. Only the CLI's own
// interrupt is 130. Hook and step errors may wrap *exec.ExitError; those
// stay 1 so a child that exited 3 or died on a signal does not change
// the backstage status.
func ExitStatus(err error) int {
	if err == nil {
		return 0
	}
	var ie interruptError
	if errors.As(err, &ie) {
		return interruptExitCode
	}
	return 1
}
