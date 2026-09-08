package production

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/recorder"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestBadgeIsFaintAndCentred(t *testing.T) {
	drawn := badge("10x", "")
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
	drawn := badge("it's 10x", "")
	if strings.Contains(drawn, "='it's") {
		t.Errorf("badge = %q; an apostrophe ends the quoting and ffmpeg refuses the graph", drawn)
	}
}

// A clip with nothing to change is handed back untouched, and no work is done.
// The take is the evidence, and a re-encode of it for no reason is a second
// generation of the thing being kept.
func TestRetimeReturnsTheClipWhenThereIsNothingToDo(t *testing.T) {
	clip := madeClip(t, 2)
	got, err := Retime(clip, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != clip {
		t.Errorf("Retime returned %q, want the clip itself", got)
	}
}

func TestRetimeShortensTheClipByTheRate(t *testing.T) {
	clip := madeClip(t, 8)
	out, err := Retime(clip, []scene.Segment{
		{Until: "4", Rate: 1},
		{Rate: 4, Badge: "4x"},
	}, "")
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
	_, err := Retime(clip, []scene.Segment{{Until: "9:00", Rate: 1}, {Rate: 10}}, "")
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
