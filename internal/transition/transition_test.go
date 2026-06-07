package transition

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubstitute(t *testing.T) {
	got := substitute("r --out {{out}} --size {{w}}x{{h}} --fps {{fps}} {{from}}->{{to}}",
		Vars{Out: "/tmp/c.mp4", W: 1920, H: 1080, FPS: 30, From: "a", To: "b"})
	want := "r --out /tmp/c.mp4 --size 1920x1080 --fps 30 a->b"
	if got != want {
		t.Errorf("substitute =\n  %s\nwant\n  %s", got, want)
	}
}

func TestRenderWritesClip(t *testing.T) {
	out := filepath.Join(t.TempDir(), "clip.mp4")
	// fake "renderer" just writes some bytes to {{out}}
	err := Render(`printf 'fakevideo' > {{out}}`, Vars{Out: out}, os.Environ(), t.TempDir())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if b, _ := os.ReadFile(out); string(b) != "fakevideo" {
		t.Errorf("clip not written, got %q", b)
	}
}

func TestRenderErrors(t *testing.T) {
	out := filepath.Join(t.TempDir(), "clip.mp4")

	// command fails
	if err := Render("exit 7", Vars{Out: out}, os.Environ(), t.TempDir()); err == nil {
		t.Error("expected error on failing command")
	}
	// command succeeds but writes nothing to out
	if err := Render("true", Vars{Out: out}, os.Environ(), t.TempDir()); err == nil {
		t.Error("expected error when no output produced")
	}
	// command writes an empty file
	empty := filepath.Join(t.TempDir(), "empty.mp4")
	if err := Render(":> {{out}}", Vars{Out: empty}, os.Environ(), t.TempDir()); err == nil {
		t.Error("expected error on empty output")
	}
	if !strings.HasSuffix(out, ".mp4") { // guard against accidental edits
		t.Fatal("test setup")
	}
}
