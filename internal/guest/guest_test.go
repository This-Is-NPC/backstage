package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewExpandsTheKeyAndDefaultsTheURI(t *testing.T) {
	t.Setenv("HOME", "/home/somebody")
	g := New("omahouse-parent", "parent", "~/.ssh/id_vms", "")
	if want := filepath.Join("/home/somebody", ".ssh/id_vms"); g.KeyFile != want {
		t.Errorf("key = %q, want %q", g.KeyFile, want)
	}
	if g.URI != "qemu:///system" {
		t.Errorf("uri = %q, want the system connection", g.URI)
	}
	// A key that is already absolute is left exactly as written: a path with a
	// tilde in the middle is somebody's real directory.
	if g := New("d", "u", "/keys/~odd/id", "qemu:///session"); g.KeyFile != "/keys/~odd/id" {
		t.Errorf("key = %q, want it untouched", g.KeyFile)
	}
}

// The chords a scene writes have to become the numbers the seat expects, and a
// wrong number is a keystroke that silently does something else -- which is the
// most expensive kind of failure this package can have, because the film looks
// like the tool did nothing.
func TestKeyRefusesANameItDoesNotKnow(t *testing.T) {
	g := New("d", "u", "", "")
	err := g.Key("super+frobnicate")
	if err == nil {
		t.Fatal("a key nobody named was accepted, so a scene's typo would be silence")
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Errorf("the refusal does not name the key: %v", err)
	}
}

func TestEveryNamedKeyHasACode(t *testing.T) {
	if len(Codes) == 0 {
		t.Fatal("no key names at all, so nothing below is checked")
	}
	for name, code := range Codes {
		if name == "" || code == "" {
			t.Errorf("%q -> %q", name, code)
		}
	}
	// The two a stage cannot do without: enter ends every `run`, and super is
	// half of every Omarchy binding.
	for _, needed := range []string{"enter", "super"} {
		if _, ok := Codes[needed]; !ok {
			t.Errorf("no code for %q", needed)
		}
	}
}

func TestSessionNamesThisAccountsOwnRuntime(t *testing.T) {
	g := New("d", "u", "", "")
	g.uid = 1003
	session := g.Session()
	for _, want := range []string{"/run/user/1003", "wayland-1", "/run/user/1003/bus"} {
		if !strings.Contains(session, want) {
			t.Errorf("session %q is missing %q", session, want)
		}
	}
}

func TestWriteFactsLandsBesideTheClip(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "01-take.mp4")
	g := New("omahouse-kid", "kid", "", "")
	g.Address = "192.168.122.230"
	if err := g.WriteFacts(clip, "4.0.2-1"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "01-take.facts.json"))
	if err != nil {
		t.Fatalf("no sidecar beside the clip: %v", err)
	}
	for _, want := range []string{"omahouse-kid", "4.0.2-1", "192.168.122.230"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the sidecar does not record %q:\n%s", want, body)
		}
	}
	// The instant is what makes two takes comparable rather than merely
	// different, so a sidecar without one is not evidence of anything.
	if !strings.Contains(string(body), time.Now().Format("2006")) {
		t.Errorf("the sidecar records no time:\n%s", body)
	}
}
