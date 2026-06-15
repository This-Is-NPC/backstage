package engine

import (
	"sync/atomic"
	"testing"
	"time"
)

// On the normal (non-interrupt) return path, Release must deregister the handler
// and the goroutine must exit WITHOUT ever running onInterrupt.
func TestInterruptGuardReleaseDoesNotRunOnInterrupt(t *testing.T) {
	var onInterruptRan atomic.Bool
	g := NewInterruptGuard(
		func() error { return nil },
		func() { onInterruptRan.Store(true) },
	)
	g.Release()
	if err := g.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if onInterruptRan.Load() {
		t.Fatal("onInterrupt ran on the normal path; it must only run on interrupt")
	}
}

// Stop must invoke the recorder teardown at most once, even when called from both
// the normal path and (conceptually) the signal goroutine. Release after Stop, or
// Stop after Release, must not double-call or panic.
func TestInterruptGuardStopRunsOnce(t *testing.T) {
	var stops atomic.Int32
	g := NewInterruptGuard(
		func() error { stops.Add(1); return nil },
		func() {},
	)
	defer g.Release()

	if err := g.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := g.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if got := stops.Load(); got != 1 {
		t.Fatalf("recorder stop called %d times; want exactly 1", got)
	}
}

// A nil stop and nil onInterrupt must be safe to construct and tear down: this
// mirrors callers that arm the guard with no recorder teardown.
func TestInterruptGuardNilFuncs(t *testing.T) {
	g := NewInterruptGuard(nil, nil)
	g.Release()
	if err := g.Stop(); err != nil {
		t.Fatalf("Stop with nil stop: %v", err)
	}
}

func TestInterruptGuardInterruptCleanupIsBoundedWhenStopHangs(t *testing.T) {
	oldTimeout := interruptStopTimeout
	oldExit := exitProcess
	interruptStopTimeout = 20 * time.Millisecond
	var exitCode atomic.Int32
	events := make([]string, 0, 2)
	exitProcess = func(code int) {
		exitCode.Store(int32(code))
		events = append(events, "exit")
	}
	defer func() {
		interruptStopTimeout = oldTimeout
		exitProcess = oldExit
	}()

	releaseStop := make(chan struct{})
	g := &InterruptGuard{stop: func() error {
		<-releaseStop
		return nil
	}}

	start := time.Now()
	g.handleInterrupt(func() { events = append(events, "cleanup") })
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("interrupt cleanup waited too long for a hung stop: %s", elapsed)
	}
	if len(events) != 2 || events[0] != "cleanup" || events[1] != "exit" {
		t.Fatalf("events = %v, want cleanup before exit", events)
	}
	if got := exitCode.Load(); got != interruptExitCode {
		t.Fatalf("exit code = %d, want %d", got, interruptExitCode)
	}

	close(releaseStop)
	if err := g.Stop(); err != nil {
		t.Fatalf("Stop after releasing hung stop: %v", err)
	}
}
