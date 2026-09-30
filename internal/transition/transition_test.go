package transition

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSubstitute(t *testing.T) {
	got := Substitute("r --out {{out}} --size {{w}}x{{h}} --fps {{fps}} {{from}}->{{to}}",
		Vars{Out: "/tmp/c.mp4", W: 1920, H: 1080, FPS: 30, From: "a", To: "b"})
	want := "r --out /tmp/c.mp4 --size 1920x1080 --fps 30 a->b"
	if got != want {
		t.Errorf("Substitute =\n  %s\nwant\n  %s", got, want)
	}
}

func TestSubstituteLeavesShellQuotingToTrustedCommand(t *testing.T) {
	got := Substitute("render --title '{{from}}' --out {{out}}",
		Vars{From: "alpha beta", Out: "/tmp/clip one.mp4"})
	want := "render --title 'alpha beta' --out /tmp/clip one.mp4"
	if got != want {
		t.Fatalf("Substitute = %q, want %q", got, want)
	}
}

func TestSubstituteLiveArgsOmitsOut(t *testing.T) {
	// A live prop must not receive the recorder's output path; {{out}} stays empty.
	got := SubstituteLiveArgs(
		[]string{"--out", "{{out}}", "--size", "{{w}}x{{h}}", "--from", "{{from}}"},
		Vars{Out: "/tmp/recording.mp4", W: 1920, H: 1080, FPS: 30, From: "a", To: "b"})
	want := []string{"--out", "", "--size", "1920x1080", "--from", "a"}
	if len(got) != len(want) {
		t.Fatalf("SubstituteLiveArgs len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("SubstituteLiveArgs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBuildCommandWritesClip(t *testing.T) {
	out := filepath.Join(t.TempDir(), "clip.mp4")
	// fake "renderer" just writes some bytes to {{out}}
	if err := BuildCommand(`printf 'fakevideo' > {{out}}`, Vars{Out: out}, os.Environ(), t.TempDir()).Run(); err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	if err := VerifyOutput(out); err != nil {
		t.Fatalf("VerifyOutput: %v", err)
	}
	if b, _ := os.ReadFile(out); string(b) != "fakevideo" {
		t.Errorf("clip not written, got %q", b)
	}
}

func TestVerifyOutputErrors(t *testing.T) {
	dir := t.TempDir()
	if err := VerifyOutput(filepath.Join(dir, "missing.mp4")); err == nil {
		t.Error("expected error when no output produced")
	}
	empty := filepath.Join(dir, "empty.mp4")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyOutput(empty); err == nil {
		t.Error("expected error on empty output")
	}
}
