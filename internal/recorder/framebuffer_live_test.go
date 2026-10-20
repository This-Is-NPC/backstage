package recorder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/encode"
)

// The caller owns the domain; this check never changes its lifecycle or disks.
func TestFramebufferWithLibvirt(t *testing.T) {
	domain := os.Getenv("BACKSTAGE_FRAMEBUFFER_DOMAIN")
	if domain == "" {
		t.Skip("set BACKSTAGE_FRAMEBUFFER_DOMAIN to validate a running test guest")
	}
	out := os.Getenv("BACKSTAGE_FRAMEBUFFER_OUTPUT")
	if out == "" {
		out = filepath.Join(t.TempDir(), "capture.mp4")
	}
	f := NewFramebuffer(domain, "", 4)
	f.Encoder = encode.Encoder(os.Getenv("BACKSTAGE_FRAMEBUFFER_ENCODER"))
	began := time.Now()
	if err := f.Start(out); err != nil {
		t.Fatal(err)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			_, _ = f.Stop()
		}
	})
	time.Sleep(6 * time.Second)
	stopping := time.Now()
	_, stopErr := f.Stop()
	stopped = true
	if stopErr != nil {
		t.Fatal(stopErr)
	}
	stopSeconds, totalSeconds := time.Since(stopping).Seconds(), time.Since(began).Seconds()
	duration, err := Duration(out)
	if err != nil {
		t.Fatal(err)
	}
	captureSeconds := f.stopped.Sub(f.began).Seconds()
	if duration < captureSeconds-0.001 || duration > captureSeconds+0.001 {
		t.Fatalf("output duration %.6f differs from capture %.6f", duration, captureSeconds)
	}
	measurement := struct {
		Total, Stop, FPS, Duration float64
		Frames                     int
	}{totalSeconds, stopSeconds, f.Rate(), duration, f.frames}
	b, err := json.Marshal(measurement)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("framebuffer-measurement %s", b)
}
