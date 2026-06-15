package recorder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGPUArgs(t *testing.T) {
	g := NewGPU("eDP-1", 30)
	got := strings.Join(g.args("/tmp/out.mp4"), " ")
	want := "-w eDP-1 -k auto -f 30 -fm cfr -fallback-cpu-encoding yes -o /tmp/out.mp4"
	if got != want {
		t.Errorf("args =\n  %s\nwant\n  %s", got, want)
	}
}

// GPU must satisfy the Recorder interface.
var _ Recorder = (*GPU)(nil)

func TestGPUStartFailsWhenProcessExitsBeforeOutput(t *testing.T) {
	installGPUStub(t, "exit 7\n")
	g := NewGPU("eDP-1", 30)
	if err := g.Start(filepath.Join(t.TempDir(), "out.mp4")); err == nil {
		t.Fatal("Start should fail when recorder exits before creating output")
	}
}

func TestGPUStopBeforeStartCancelsStart(t *testing.T) {
	installGPUStub(t, `printf warm > "$out"
exit 0
`)
	out := filepath.Join(t.TempDir(), "out.mp4")
	g := NewGPU("eDP-1", 30)
	if _, err := g.Stop(); err != nil {
		t.Fatalf("Stop before Start: %v", err)
	}
	if err := g.Start(out); err == nil || !strings.Contains(err.Error(), "start cancelled") {
		t.Fatalf("Start after Stop error = %v, want start cancelled", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("cancelled Start should not spawn the recorder")
	}
}

func TestGPUStopFinalizesNonEmptyOutput(t *testing.T) {
	installGPUStub(t, `printf warm > "$out"
trap 'printf final > "$out"; exit 0' INT
while true; do sleep 1; done
`)
	out := filepath.Join(t.TempDir(), "out.mp4")
	g := NewGPU("eDP-1", 30)
	if err := g.Start(out); err != nil {
		t.Fatalf("Start: %v", err)
	}
	g.mu.Lock()
	setpgid := g.cmd != nil && g.cmd.SysProcAttr != nil && g.cmd.SysProcAttr.Setpgid
	g.mu.Unlock()
	if !setpgid {
		t.Fatal("Start must place recorder in its own process group")
	}
	if _, err := g.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if b, err := os.ReadFile(out); err != nil || string(b) != "final" {
		t.Fatalf("final output = %q, %v", b, err)
	}
}

func TestGPUStopDuringStartFinalizesOutput(t *testing.T) {
	installGPUStub(t, `printf ready > "$out".ready
trap 'printf final > "$out"; exit 0' INT
while true; do sleep 1; done
`)
	out := filepath.Join(t.TempDir(), "out.mp4")
	g := NewGPU("eDP-1", 30)
	startErr := make(chan error, 1)
	go func() { startErr <- g.Start(out) }()

	ready := out + ".ready"
	deadline := time.After(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case err := <-startErr:
			t.Fatalf("Start returned before recorder was stopped: %v", err)
		case <-deadline:
			t.Fatal("recorder stub did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}

	if _, err := g.Stop(); err != nil {
		t.Fatalf("Stop during Start: %v", err)
	}
	select {
	case err := <-startErr:
		if err != nil {
			t.Fatalf("Start after Stop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not unblock after Stop")
	}
	if b, err := os.ReadFile(out); err != nil || string(b) != "final" {
		t.Fatalf("final output = %q, %v", b, err)
	}
}

func TestGPUStopRejectsEmptyOutput(t *testing.T) {
	installGPUStub(t, `: > "$out"
trap 'exit 0' INT
while true; do sleep 1; done
`)
	out := filepath.Join(t.TempDir(), "out.mp4")
	g := NewGPU("eDP-1", 30)
	if err := g.Start(out); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := g.Stop(); err == nil {
		t.Fatal("Stop should reject empty output")
	}
}

func TestGPUStopForceKillsIgnoredSIGINT(t *testing.T) {
	oldStopGrace := recorderStopGrace
	oldKillWait := recorderKillWait
	recorderStopGrace = 50 * time.Millisecond
	recorderKillWait = time.Second
	defer func() {
		recorderStopGrace = oldStopGrace
		recorderKillWait = oldKillWait
	}()
	installGPUStub(t, `printf warm > "$out"
trap '' INT
while true; do sleep 1; done
`)
	out := filepath.Join(t.TempDir(), "out.mp4")
	g := NewGPU("eDP-1", 30)
	if err := g.Start(out); err != nil {
		t.Fatalf("Start: %v", err)
	}
	start := time.Now()
	if _, err := g.Stop(); err == nil || !strings.Contains(err.Error(), "SIGKILL") {
		t.Fatalf("Stop error = %v, want SIGKILL fallback", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Stop waited too long for ignored SIGINT: %s", elapsed)
	}
}

func installGPUStub(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, gpuBinary)
	script := `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    out="$2"
    shift 2
  else
    shift
  fi
done
` + body
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
