package pane

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestPointerStreamPreservesTimingGapsClicksAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "sh", "-c", `printf '%s\n' '{"elapsed":0.1,"monitor":{"width":1920,"height":1080,"scale":1},"pointer":{"x":960,"y":540},"click":true}' '{"elapsed":0.15}' '{"elapsed":0.2,"monitor":{"width":1920,"height":1080,"scale":1},"pointer":{"x":480,"y":270}}'`)
	stop := tracePointer(cmd, cancel, time.Now().Add(-time.Second), false)
	// The producer finishes itself; cancellation must also remain idempotent.
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("stream did not finish")
	}
	samples := stop()
	if len(samples) != 3 || samples[0].At < .8 || samples[0].Pointer == nil || !samples[0].Pointer.Click || samples[1].Pointer != nil || samples[2].Pointer.X != .25 || samples[2].At-samples[0].At < .099 {
		t.Fatalf("samples=%+v", samples)
	}
	if len(stop()) != 3 {
		t.Fatal("stop lost observations")
	}
	ctx, cancel = context.WithCancel(context.Background())
	stop = tracePointer(exec.CommandContext(ctx, "sleep", "30"), cancel, time.Now(), false)
	started := time.Now()
	stop()
	if time.Since(started) > time.Second {
		t.Fatal("cancellation blocked")
	}
}

func TestPointerEventProtocol(t *testing.T) {
	if out, err := exec.Command("python3", "-B", "observe_pointer_test.py").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
