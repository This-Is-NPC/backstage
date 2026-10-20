package recorder

import (
	"strings"
	"testing"
	"time"
)

func TestFramebufferDefaultsTheURIAndTheRate(t *testing.T) {
	f := NewFramebuffer("omahouse-kid", "", 0)
	if f.URI != "qemu:///system" {
		t.Errorf("uri = %q", f.URI)
	}
	if f.FPS <= 0 {
		t.Errorf("fps = %d, want a rate to aim for", f.FPS)
	}
}

func TestFramebufferStopBeforeStartIsRefusedByName(t *testing.T) {
	_, err := NewFramebuffer("d", "", 4).Stop()
	if err == nil {
		t.Fatal("stopping a recorder that never started was accepted")
	}
	// By name: without the guard this falls through to assembling an empty
	// directory and fails there instead, so a case asking only for an error
	// would pass while the guard was gone.
	if !strings.Contains(err.Error(), "never started") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
}

// The rate a take is assembled at has to be the rate the grabs achieved, not
// the rate that was asked for. A grab over libvirt costs more than a sleep, so
// a loop aiming at four a second lands below it -- and assembling below-rate
// frames at the asked-for rate is a film that plays fast, silently, by exactly
// the fraction it fell short. This is the arithmetic that keeps it real time.
func TestFramebufferRateIsMeasuredAndNotAssumed(t *testing.T) {
	f := NewFramebuffer("d", "", 30)
	f.began = time.Now().Add(-10 * time.Second)
	f.stopped = time.Now()
	f.frames = 25

	got := f.Rate()
	if got < 2.4 || got > 2.6 {
		t.Errorf("rate = %.2f, want the 2.5 the grabs actually managed and not the 30 asked for", got)
	}
	if got >= float64(f.FPS) {
		t.Errorf("rate = %.2f, which is the asked-for rate; the film would play fast", got)
	}
}

func TestFramebufferRateIsNothingBeforeATakeEnds(t *testing.T) {
	f := NewFramebuffer("d", "", 4)
	if f.Rate() != 0 {
		t.Errorf("a recorder that never ran reports a rate of %v", f.Rate())
	}
}
