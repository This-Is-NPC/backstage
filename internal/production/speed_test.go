package production

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/budget"
	"github.com/This-Is-NPC/backstage/internal/engine"
	"github.com/This-Is-NPC/backstage/internal/recorder"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestRetimeBatchPreservesEncodedFramesAndTimestamps(t *testing.T) {
	previousCPU, previousMemory := budget.NumCPU, budget.ReadMemAvailable
	budget.NumCPU = func() int { return 8 }
	budget.ReadMemAvailable = func() (uint64, error) { return 10 << 30, nil }
	t.Cleanup(func() { budget.NumCPU, budget.ReadMemAvailable = previousCPU, previousMemory })
	source := madeClip(t, 8)
	for _, tc := range []struct {
		name     string
		fps      int
		segments []scene.Segment
	}{
		{"mixed", 30, []scene.Segment{{Until: "2", Rate: 1}, {Until: "6", Rate: 2, Badge: "2x"}, {Rate: 1}}},
		{"low-rate", 3, []scene.Segment{{Until: "2", Rate: 1}, {Until: "6", Rate: 2, Badge: "2x"}, {Rate: 1}}},
		{"fractional-and-slow", 30, []scene.Segment{{Until: "1.117", Rate: 0.5}, {Until: "3.953", Rate: 1.25}, {Rate: 4, Badge: "4x"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clip := filepath.Join(t.TempDir(), "source.mp4")
			cmd := exec.Command("ffmpeg", "-v", "error", "-y", "-i", source,
				"-vf", fmt.Sprintf("fps=%d", tc.fps), "-c:v", "libx264", "-preset", "veryfast", clip)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("fixture: %v: %s", err, b)
			}
			original, err := os.ReadFile(clip)
			if err != nil {
				t.Fatal(err)
			}
			var sequential, sequentialPTS []byte
			for _, run := range []struct{ cpu, calls int }{{2, 4}, {4, 3}, {8, 2}} {
				budget.NumCPU = func() int { return run.cpu }
				previousCommand := runProductionCommand
				calls := 0
				runProductionCommand = func(cmd *exec.Cmd, cleanup func()) error {
					calls++
					return previousCommand(cmd, cleanup)
				}
				out, err := retime(context.Background(), clip, tc.segments, scene.RenderCfg{}, nil)
				runProductionCommand = previousCommand
				if err != nil {
					t.Fatal(err)
				}
				if calls != run.calls {
					t.Fatalf("FFmpeg calls: got %d, want %d", calls, run.calls)
				}
				payload, err := exec.Command("ffmpeg", "-v", "error", "-i", out, "-map", "0:v:0", "-c:v", "copy", "-bsf:v", "h264_mp4toannexb", "-f", "h264", "-").Output()
				if err != nil {
					t.Fatal(err)
				}
				timestamps, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_frames", "-show_entries", "frame=pts_time,duration_time:format=duration", "-of", "json", out).Output()
				if err != nil {
					t.Fatal(err)
				}
				if run.cpu == 2 {
					sequential, sequentialPTS = payload, timestamps
				} else {
					if !bytes.Equal(sequential, payload) {
						t.Fatal("batching changed the H.264 payload")
					}
					if !bytes.Equal(sequentialPTS, timestamps) {
						t.Fatal("batching changed frame timestamps or duration")
					}
				}
			}
			after, err := os.ReadFile(clip)
			if err != nil || sha256.Sum256(after) != sha256.Sum256(original) {
				t.Fatalf("source recording changed: %v", err)
			}
		})
	}
}

func TestRetimeBatchRespectsResourceLimits(t *testing.T) {
	previousCPU, previousMemory := budget.NumCPU, budget.ReadMemAvailable
	t.Cleanup(func() { budget.NumCPU, budget.ReadMemAvailable = previousCPU, previousMemory })
	for _, tc := range []struct {
		name   string
		cfg    scene.RenderCfg
		cpu    int
		memory uint64
		err    error
		want   int
	}{
		{name: "automatic", cpu: 8, memory: 10 << 30, want: 4},
		{name: "cpu-limit", cpu: 2, memory: 10 << 30, want: 1},
		{name: "memory-limit", cpu: 16, memory: 6 << 30, want: 2},
		{name: "reserve", cpu: 16, memory: 1 << 30, want: 1},
		{name: "unknown-memory", cpu: 16, err: errors.New("unavailable"), want: 1},
		{name: "encoding-budget", cfg: scene.RenderCfg{Threads: scene.RenderThreads{Encode: 4}}, cpu: 8, memory: 10 << 30, want: 1},
		{name: "filter-budget", cfg: scene.RenderCfg{Threads: scene.RenderThreads{Filter: 2}}, cpu: 8, memory: 10 << 30, want: 1},
		{name: "hardware", cfg: scene.RenderCfg{Encoder: "vaapi"}, cpu: 8, memory: 10 << 30, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget.NumCPU = func() int { return tc.cpu }
			budget.ReadMemAvailable = func() (uint64, error) { return tc.memory, tc.err }
			if got := retimeBatchSize(tc.cfg, 6); got != tc.want {
				t.Fatalf("batch size: got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRetimeBatchFailureDoesNotJoinOrReplaceOutput(t *testing.T) {
	clip := madeClip(t, 2)
	out := strings.TrimSuffix(clip, ".mp4") + ".retimed.mp4"
	if err := os.WriteFile(out, []byte("previous export"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := runProductionCommand
	t.Cleanup(func() { runProductionCommand = previous })
	failure := errors.New("encode interrupted")
	var piece string
	calls := 0
	runProductionCommand = func(cmd *exec.Cmd, _ func()) error {
		calls++
		piece = cmd.Args[len(cmd.Args)-1]
		return failure
	}
	_, err := retime(context.Background(), clip, []scene.Segment{{Until: "1", Rate: 2}, {Rate: 1}}, scene.RenderCfg{}, nil)
	if !errors.Is(err, failure) || calls != 1 {
		t.Fatalf("got %v after %d commands; expected the first encode failure", err, calls)
	}
	if _, err := os.Stat(filepath.Dir(piece)); !os.IsNotExist(err) {
		t.Fatalf("temporary pieces remain: %v", err)
	}
	content, err := os.ReadFile(out)
	if err != nil || string(content) != "previous export" {
		t.Fatalf("previous export changed: %q, %v", content, err)
	}
}

func TestRetimeCancellationStopsBeforeProbing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := retime(ctx, "missing.mp4", nil, scene.RenderCfg{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context cancellation", err)
	}
}

func TestRetimeInterruptCleansPiecesAndPreservesOutput(t *testing.T) {
	for _, interruptAt := range []int{1, 2} {
		t.Run(fmt.Sprintf("command-%d", interruptAt), func(t *testing.T) {
			clip := madeClip(t, 2)
			out := strings.TrimSuffix(clip, ".mp4") + ".retimed.mp4"
			if err := os.WriteFile(out, []byte("previous export"), 0o644); err != nil {
				t.Fatal(err)
			}
			previousCommand, previousGuard := runProductionCommand, newInterruptGuard
			t.Cleanup(func() { runProductionCommand, newInterruptGuard = previousCommand, previousGuard })
			var work string
			commands := 0
			runProductionCommand = func(cmd *exec.Cmd, cleanup func()) error {
				commands++
				if commands == 1 {
					work = filepath.Dir(cmd.Args[len(cmd.Args)-1])
				}
				return previousCommand(cmd, cleanup)
			}
			guards := 0
			newInterruptGuard = func(stop func() error, cleanup func()) interruptGuard {
				guards++
				if guards == interruptAt {
					_ = stop()
					cleanup()
				}
				return &noopProductionGuard{}
			}
			cleaned := 0
			_, err := retime(context.Background(), clip, []scene.Segment{{Rate: 2}}, scene.RenderCfg{}, func() { cleaned++ })
			if !errors.Is(err, engine.ErrCommandInterrupted) || commands != interruptAt || cleaned != 1 {
				t.Fatalf("got %v after %d commands and %d cleanups", err, commands, cleaned)
			}
			if _, err := os.Stat(work); !os.IsNotExist(err) {
				t.Fatalf("temporary pieces remain: %v", err)
			}
			if content, err := os.ReadFile(out); err != nil || string(content) != "previous export" {
				t.Fatalf("previous export changed: %q, %v", content, err)
			}
			leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(out), ".production-*.mp4"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("temporary exports remain: %v, %v", leftovers, err)
			}
		})
	}
}

func TestBadgeIsFaintAndCentred(t *testing.T) {
	drawn := badge("10x")
	for _, want := range []string{"text='10x'", "(w-text_w)/2", "(h-text_h)/2"} {
		if !strings.Contains(drawn, want) {
			t.Errorf("badge = %q, missing %q", drawn, want)
		}
	}
	// Faint, or it competes with the thing it is describing. Opaque white over
	// the middle of a terminal hides the line the viewer is reading.
	if !strings.Contains(drawn, "white@0.1") && !strings.Contains(drawn, "@0.2") {
		t.Errorf("badge = %q, want a low opacity", drawn)
	}
}

func TestBadgeQuotesWhatWouldEndTheFilter(t *testing.T) {
	drawn := badge("it's 10x")
	if strings.Contains(drawn, "='it's") {
		t.Errorf("badge = %q; an apostrophe ends the quoting and ffmpeg refuses the graph", drawn)
	}
}

// A clip with nothing to change is handed back untouched, and no work is done.
// The take is the evidence, and a re-encode of it for no reason is a second
// generation of the thing being kept.
func TestRetimeReturnsTheClipWhenThereIsNothingToDo(t *testing.T) {
	clip := madeClip(t, 2)
	got, err := retime(context.Background(), clip, nil, scene.RenderCfg{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != clip {
		t.Errorf("Retime returned %q, want the clip itself", got)
	}
}

func TestRetimeShortensTheClipByTheRate(t *testing.T) {
	clip := madeClip(t, 8)
	out, err := retime(context.Background(), clip, []scene.Segment{
		{Until: "4", Rate: 1},
		{Rate: 4, Badge: "4x"},
	}, scene.RenderCfg{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == clip {
		t.Fatal("Retime returned the original clip")
	}
	length, err := recorder.Duration(out)
	if err != nil {
		t.Fatal(err)
	}
	// Four seconds at one, then four at four: five seconds, near enough for a
	// container that rounds to whole frames.
	if length < 4.4 || length > 5.6 {
		t.Errorf("the retimed clip is %.2fs; want about 5s", length)
	}
	// And the take is untouched, because it is the evidence.
	was, err := recorder.Duration(clip)
	if err != nil || was < 7.5 {
		t.Errorf("the original clip is now %.2fs", was)
	}
}

func TestRetimeRefusesSegmentsItCannotHonour(t *testing.T) {
	clip := madeClip(t, 4)
	_, err := retime(context.Background(), clip, []scene.Segment{{Until: "9:00", Rate: 1}, {Rate: 10}}, scene.RenderCfg{}, nil)
	if err == nil {
		t.Fatal("a segment past the end of the clip was accepted")
	}
	if !strings.Contains(err.Error(), filepath.Base(clip)) {
		t.Errorf("the refusal does not name the clip: %v", err)
	}
}

// madeClip writes a real clip of the given length, so the arithmetic above is
// checked against ffmpeg rather than against an assumption about it.
func madeClip(t *testing.T, seconds int) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	path := filepath.Join(t.TempDir(), "take.mp4")
	made := exec.Command("ffmpeg", "-y", "-v", "error",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=30:duration="+itoa(seconds),
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", path)
	if said, err := made.CombinedOutput(); err != nil {
		t.Fatalf("could not make a clip: %s", said)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
