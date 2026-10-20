package pane

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"github.com/This-Is-NPC/backstage/internal/facts"
)

//go:embed observe_pointer.py
var pointerScript string

// PointerObserver is optional and runs only while observations are requested.
type PointerObserver interface {
	TracePointer(context.Context, time.Time) func() []facts.PointerSample
}

func (t *Tmux) TracePointer(ctx context.Context, started time.Time) func() []facts.PointerSample {
	ctx, cancel := context.WithCancel(ctx)
	return tracePointer(exec.CommandContext(ctx, "python3", "-u", "-c", pointerScript, t.Monitor), cancel, started, false)
}

func (g *Guest) TracePointer(ctx context.Context, started time.Time) func() []facts.PointerSample {
	ctx, cancel := context.WithCancel(ctx)
	quoted := "'" + strings.ReplaceAll(pointerScript, "'", `'\''`) + "'"
	return tracePointer(g.Box.SessionCommand(ctx, "exec python3 -u -c "+quoted+" ''"), cancel, started, g.NativePixels)
}

func tracePointer(cmd *exec.Cmd, cancel context.CancelFunc, started time.Time, native bool) func() []facts.PointerSample {
	done := make(chan struct{})
	var samples []facts.PointerSample
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return func() []facts.PointerSample { return nil }
	}
	if err := cmd.Start(); err != nil {
		_ = pipe.Close()
		cancel()
		return func() []facts.PointerSample { return nil }
	}
	go func() {
		defer close(done)
		defer cancel()
		defer func() { _ = cmd.Wait() }()
		scanner := bufio.NewScanner(pipe)
		epoch := -1.0
		for scanner.Scan() {
			var message struct {
				Elapsed float64
				Monitor desktopMonitor
				Pointer struct{ X, Y *float64 }
				Click   bool
			}
			if json.Unmarshal(scanner.Bytes(), &message) != nil || message.Elapsed < 0 {
				continue
			}
			if epoch < 0 {
				epoch = time.Since(started).Seconds() - message.Elapsed
			}
			sample := facts.PointerSample{At: max(0, epoch+message.Elapsed)}
			if message.Pointer.X != nil && message.Pointer.Y != nil {
				sample.Pointer = message.Monitor.pointer(*message.Pointer.X, *message.Pointer.Y, native)
				if sample.Pointer != nil {
					sample.Pointer.Click = message.Click
				}
			}
			if n := len(samples); n == 0 || sample.At > samples[n-1].At {
				samples = append(samples, sample)
			}
		}
	}()
	return func() []facts.PointerSample {
		cancel()
		<-done
		return samples
	}
}
