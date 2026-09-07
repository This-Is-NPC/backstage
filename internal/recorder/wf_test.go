package recorder

import (
	"strings"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/guest"
)

func TestWFArgs(t *testing.T) {
	g := guest.New("omahouse-parent", "parent", "", "")
	got := strings.Join(NewWF(g, 30).args("/tmp/take.mp4"), " ")
	for _, want := range []string{"-c libx264", "-f /tmp/take.mp4", "-r 30"} {
		if !strings.Contains(got, want) {
			t.Errorf("args = %q, missing %q", got, want)
		}
	}
	// Software encoding is not a preference here: these guests have no render
	// node, so a hardware codec is a recorder that writes nothing.
	if strings.Contains(got, "vaapi") || strings.Contains(got, "nvenc") {
		t.Errorf("args = %q, and a guest with no render node cannot encode in hardware", got)
	}
}

func TestWFLeavesTheRateToTheCompositorWhenUnset(t *testing.T) {
	g := guest.New("d", "u", "", "")
	got := strings.Join(NewWF(g, 0).args("/tmp/take.mp4"), " ")
	if strings.Contains(got, "-r") {
		t.Errorf("args = %q, want no frame rate forced when none was configured", got)
	}
}

func TestWFStopBeforeStartIsRefusedByName(t *testing.T) {
	g := guest.New("d", "u", "", "")
	_, err := NewWF(g, 30).Stop()
	if err == nil {
		t.Fatal("stopping a recorder that never started was accepted")
	}
	// By name, and not merely "some error". Without the guard this call falls
	// through to real ssh against an empty address and fails there instead --
	// so a case that only asked for an error would pass while the guard was
	// gone, and would reach the network from a unit test to do it.
	if !strings.Contains(err.Error(), "never started") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
}
