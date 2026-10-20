package production

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestNormalizeCopiesCompatibleVideoWithoutChangingFrames(t *testing.T) {
	clip := madeClip(t, 1)
	out, err := normalizeClips(context.Background(), []string{clip, clip}, t.TempDir(), scene.RenderCfg{Workers: 2}, 320, 240, 30, nil)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := func(path string) string {
		t.Helper()
		b, err := exec.Command("ffmpeg", "-v", "error", "-i", path, "-map", "0:v:0", "-c:v", "copy", "-bsf:v", "h264_mp4toannexb", "-f", "h264", "-").Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	want := fingerprint(clip)
	for _, path := range out {
		if got := fingerprint(path); got != want {
			t.Fatal("compatible H.264 was re-encoded")
		}
	}
}

func TestNormalizeResizesMixedClipsAndPreservesOrder(t *testing.T) {
	clip := madeClip(t, 1)
	other := filepath.Join(t.TempDir(), "other.mp4")
	cmd := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=blue:s=160x90:r=25:d=1", "-c:v", "libx264", other)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, b)
	}
	out, err := normalizeClips(context.Background(), []string{clip, other}, t.TempDir(), scene.RenderCfg{Workers: 2, Threads: scene.RenderThreads{Encode: 2}}, 320, 240, 30, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, path := range out {
		s, err := probeVideo(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		if s.Width != 320 || s.Height != 240 || s.Rate != "30/1" || s.TimeBase != "1/15360" {
			t.Fatalf("segment %d: %+v", i, s)
		}
	}
	if filepath.Base(out[0]) != "n000.mp4" || filepath.Base(out[1]) != "n001.mp4" {
		t.Fatal(out)
	}
}

func TestCopyCompatibilityRejectsDifferentH264Configuration(t *testing.T) {
	s := videoStream{Codec: "h264", Width: 320, Height: 240, PixelFormat: "yuv420p", Rate: "30/1", AverageRate: "30/1", TimeBase: "1/15360", Aspect: "1:1", ExtraData: "sha256:a"}
	for _, field := range []string{"sps", "timebase", "fps", "vfr", "sar", "color"} {
		other := s
		switch field {
		case "sps":
			other.ExtraData = "sha256:b"
		case "timebase":
			other.TimeBase = "1/30"
		case "fps":
			other.Rate = "25/1"
		case "vfr":
			other.AverageRate = "29/1"
		case "sar":
			other.Aspect = "2:1"
		case "color":
			other.ColorSpace = "bt709"
		}
		if copyCompatible([]videoStream{s, other}, 320, 240, 30) {
			t.Errorf("copied mismatched %s", field)
		}
	}
}

func TestNormalizeCancellationStopsBeforeEncoding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := normalizeClips(ctx, []string{"unused.mp4"}, t.TempDir(), scene.RenderCfg{}, 320, 240, 30, nil); err == nil {
		t.Fatal("accepted cancelled context")
	}
}
