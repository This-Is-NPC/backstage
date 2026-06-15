package engine

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const interruptExitCode = 130

var (
	interruptStopTimeout = 10 * time.Second
	exitProcess          = os.Exit
)

type interruptGuard interface {
	Stop() error
	Release()
}

var newInterruptGuard = func(stop func() error, onInterrupt func()) interruptGuard {
	return NewInterruptGuard(stop, onInterrupt)
}

// InterruptGuard centralizes the "run primary stop, then teardown and exit on
// SIGINT/SIGTERM" pattern shared by engine.Run and production.recordLiveTransition.
// Both paths must (a) start stop exactly once with no data race between the signal
// goroutine and the main return path, and (b) run their teardown before os.Exit so
// nothing leaks on interrupt. Keeping one implementation means the two sites can't
// drift.
//
// The interrupt-handler body is supplied by the caller (onInterrupt) so each site
// keeps its own secondary teardown. The guard owns stop and starts it before
// invoking onInterrupt; on the signal path it waits only a bounded time so
// secondary teardown still runs if primary shutdown hangs. onInterrupt must not
// call Stop itself and need not reference the guard at all. The handler runs once,
// then the process exits 130.
type InterruptGuard struct {
	mu          sync.Mutex
	stopped     bool
	stop        func() error // primary teardown; invoked at most once
	stopDone    chan struct{}
	stopErr     error
	sigCh       chan os.Signal
	done        chan struct{}
	releaseOnce sync.Once
}

// NewInterruptGuard installs a SIGINT/SIGTERM handler. On interrupt it first
// starts stop(), waits up to a bounded timeout, then runs onInterrupt (the
// caller's extra teardown), then exits with code 130. stop may be nil. Call
// Release on the normal path to deregister the handler.
func NewInterruptGuard(stop func() error, onInterrupt func()) *InterruptGuard {
	g := &InterruptGuard{
		stop:  stop,
		sigCh: make(chan os.Signal, 1),
		done:  make(chan struct{}),
	}
	signal.Notify(g.sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-g.sigCh:
			g.handleInterrupt(onInterrupt)
		case <-g.done:
		}
	}()
	return g
}

func (g *InterruptGuard) handleInterrupt(onInterrupt func()) {
	// Finalize the recorder before any caller teardown so the file is closed
	// regardless of what onInterrupt does. Bound the interrupt path so secondary
	// cleanup (popup/stage/temp files) still runs if recorder shutdown hangs.
	_ = g.stopWithin(interruptStopTimeout)
	if onInterrupt != nil {
		onInterrupt()
	}
	exitProcess(interruptExitCode)
}

// Stop runs the recorder teardown at most once, even across the signal goroutine
// and the main return path. Safe to call from both.
func (g *InterruptGuard) Stop() error {
	done := g.startStop()
	<-done
	return g.stopError()
}

func (g *InterruptGuard) stopWithin(timeout time.Duration) error {
	done := g.startStop()
	if timeout <= 0 {
		<-done
		return g.stopError()
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-done:
		return g.stopError()
	case <-t.C:
		return fmt.Errorf("interrupt recorder stop timed out after %s", timeout)
	}
}

func (g *InterruptGuard) startStop() <-chan struct{} {
	g.mu.Lock()
	if g.stopDone != nil {
		done := g.stopDone
		g.mu.Unlock()
		return done
	}
	done := make(chan struct{})
	g.stopDone = done
	stop := g.stop
	if g.stopped || stop == nil {
		g.stopped = true
		g.mu.Unlock()
		close(done)
		return done
	}
	g.stopped = true
	g.mu.Unlock()

	go func() {
		err := stop()
		g.mu.Lock()
		g.stopErr = err
		close(done)
		g.mu.Unlock()
	}()
	return done
}

func (g *InterruptGuard) stopError() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stopErr
}

// Release deregisters the signal handler and unblocks the goroutine. Call on the
// normal (non-interrupt) return path. Idempotent.
func (g *InterruptGuard) Release() {
	g.releaseOnce.Do(func() {
		signal.Stop(g.sigCh)
		close(g.done)
	})
}
