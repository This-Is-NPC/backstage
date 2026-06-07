package recorder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if _, err := g.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
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
