package cli

import (
	"context"
	"errors"
	"time"
)

const interruptExitCode = 130

// parentInterruptGrace is how long finishJob waits for the parent's
// context to cancel after a child exits 130 or dies on SIGINT/SIGTERM.
// A real Ctrl-C hits the whole process group before the parent's
// NotifyContext runs; without this window the child would be a run failure.
const parentInterruptGrace = 500 * time.Millisecond

func waitParentInterrupt(ctx context.Context, grace time.Duration) {
	if ctx == nil || ctx.Err() != nil || grace <= 0 {
		return
	}
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

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
