package recorder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func framebufferFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	frame := image.NewRGBA(image.Rect(0, 0, 128, 72))
	for y := range 72 {
		for x := range 128 {
			frame.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 80, A: 255})
		}
	}
	f, err := os.Create(filepath.Join(dir, "fixture.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, frame); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Setenv("BACKSTAGE_FRAMEBUFFER_FIXTURE", f.Name())
	script := "#!/bin/sh\n[ \"$1\" = -q ] && [ \"$4\" = screenshot ] && [ \"$6\" = /dev/stdout ] || exit 1\ncat \"$BACKSTAGE_FRAMEBUFFER_FIXTURE\"\n"
	if err := os.WriteFile(filepath.Join(dir, "virsh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFramebufferStreamsAndPreservesMeasuredClock(t *testing.T) {
	dir := framebufferFixture(t)
	f := NewFramebuffer("test", "", 40)
	out := filepath.Join(dir, "output.mp4")
	if err := f.Start(out); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.Stop() })
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		frames := f.frames
		f.mu.Unlock()
		if frames >= 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	work, err := filepath.Glob(filepath.Join(dir, "backstage-framebuffer-*"))
	if err != nil || len(work) != 1 {
		t.Fatalf("capture workspace=%v: %v", work, err)
	}
	files, err := os.ReadDir(work[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if filepath.Ext(file.Name()) == ".png" {
			t.Fatal("capture wrote a PNG to disk")
		}
	}
	if got, err := f.Stop(); err != nil || got != out {
		t.Fatalf("stop=%s: %v", got, err)
	}
	if _, err := f.Stop(); err != nil {
		t.Fatalf("repeated stop: %v", err)
	}
	if err := f.Start(out); err == nil {
		t.Fatal("restarted a one-shot recorder")
	}
	b, err := exec.Command("ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0", "-show_entries", "stream=nb_read_frames,duration", "-of", "json", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Streams []struct {
			Frames   string `json:"nb_read_frames"`
			Duration string `json:"duration"`
		}
	}
	if err := json.Unmarshal(b, &data); err != nil || len(data.Streams) != 1 {
		t.Fatalf("probe=%s: %v", b, err)
	}
	count, _ := strconv.Atoi(data.Streams[0].Frames)
	if count != f.frames || count < 5 {
		t.Fatalf("encoded %d of %d frames", count, f.frames)
	}
	duration, _ := strconv.ParseFloat(data.Streams[0].Duration, 64)
	measured := f.stopped.Sub(f.began).Seconds()
	if duration < measured-0.001 || duration > measured+0.001 {
		t.Fatalf("duration %f does not preserve the measured clock %f", duration, measured)
	}
	if _, err := os.Stat(work[0]); !os.IsNotExist(err) {
		t.Fatalf("capture workspace remains: %v", err)
	}
}

func TestFramebufferFailurePreservesPreviousExport(t *testing.T) {
	for _, invalidPNG := range []bool{false, true} {
		t.Run(fmt.Sprintf("invalid-image-%t", invalidPNG), func(t *testing.T) {
			dir := framebufferFixture(t)
			out := filepath.Join(dir, "output.mp4")
			if err := os.WriteFile(out, []byte("previous"), 0o644); err != nil {
				t.Fatal(err)
			}
			if invalidPNG {
				if err := os.WriteFile(os.Getenv("BACKSTAGE_FRAMEBUFFER_FIXTURE"), []byte("not a PNG"), 0o644); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			f := NewFramebuffer("test", "", 40)
			_ = f.Start(out)
			if _, err := f.Stop(); err == nil {
				t.Fatal("failed capture was accepted")
			}
			if b, err := os.ReadFile(out); err != nil || string(b) != "previous" {
				t.Fatalf("previous export changed: %q, %v", b, err)
			}
			for _, pattern := range []string{"backstage-framebuffer-*", ".framebuffer-*.mp4"} {
				if paths, err := filepath.Glob(filepath.Join(dir, pattern)); err != nil || len(paths) != 0 {
					t.Fatalf("capture leftovers=%v: %v", paths, err)
				}
			}
		})
	}
}

func TestFramebufferStopCancelsStartup(t *testing.T) {
	dir := framebufferFixture(t)
	entered := filepath.Join(dir, "entered")
	t.Setenv("BACKSTAGE_FRAMEBUFFER_ENTERED", entered)
	if err := os.WriteFile(filepath.Join(dir, "virsh"), []byte("#!/bin/sh\ntouch \"$BACKSTAGE_FRAMEBUFFER_ENTERED\"\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := NewFramebuffer("test", "", 4)
	started := make(chan error, 1)
	go func() { started <- f.Start(filepath.Join(dir, "output.mp4")) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(entered); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := f.Stop(); err == nil {
		t.Fatal("cancelled startup was accepted")
	}
	select {
	case err := <-started:
		if err == nil {
			t.Fatal("startup succeeded after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("startup did not return after cancellation")
	}
}

func TestFramebufferClockMatchesImageSequenceWithBFrames(t *testing.T) {
	dir := framebufferFixture(t)
	run := func(args ...string) []byte {
		t.Helper()
		b, err := exec.Command("ffmpeg", append([]string{"-v", "error", "-y"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg: %v: %s", err, b)
		}
		return b
	}
	run("-f", "lavfi", "-i", "testsrc2=s=640x360:r=4:d=5", "-pix_fmt", "rgb24", filepath.Join(dir, "%03d.png"))
	encode := func(rate, out string) {
		args := []string{"-framerate", rate, "-i", filepath.Join(dir, "%03d.png"), "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p"}
		if filepath.Base(out) == "nominal.mp4" {
			args = append(args, "-video_track_timescale", "1000000000")
		}
		run(append(args, out)...)
	}
	nominal := filepath.Join(dir, "nominal.mp4")
	encode("4", nominal)
	probe := func(path string) []byte {
		t.Helper()
		b, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_frames", "-show_entries", "frame=pts_time,duration_time:format=duration", "-of", "json", path).Output()
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for _, rate := range []float64{0.5, 2.5, 3.1415, 3.3333, 4} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			baseline, candidate := filepath.Join(dir, "baseline.mp4"), filepath.Join(dir, "candidate.mp4")
			encode(strconv.FormatFloat(rate, 'f', 4, 64), baseline)
			clock, err := framebufferClock(4, rate)
			if err != nil {
				t.Fatal(err)
			}
			args := append([]string{"-i", nominal, "-c:v", "copy"}, clock...)
			run(append(args, candidate)...)
			if !bytes.Equal(probe(baseline), probe(candidate)) {
				t.Fatal("frame count, timestamps or duration changed")
			}
			stats := filepath.Join(dir, "psnr.txt")
			run("-i", baseline, "-i", candidate, "-lavfi", "psnr=stats_file="+stats, "-f", "null", "-")
			b, err := os.ReadFile(stats)
			if err != nil {
				t.Fatal(err)
			}
			minimum, compared := math.Inf(1), 0
			for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				for _, field := range strings.Fields(line) {
					if strings.HasPrefix(field, "psnr_y:") {
						value, err := strconv.ParseFloat(strings.TrimPrefix(field, "psnr_y:"), 64)
						minimum = min(minimum, value)
						compared++
						if err != nil || value < 45 {
							t.Fatalf("frame quality below threshold: %s", field)
						}
					}
				}
			}
			if compared != 20 {
				t.Fatalf("compared %d frames, want 20", compared)
			}
			t.Logf("minimum PSNR-Y %.2f dB over %d frames", minimum, compared)
		})
	}
}
