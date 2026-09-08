package engine

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

// TestMain keeps every other test in this package away from ffprobe. A Run test
// records to a path nothing ever wrote, and weighing that file is neither what
// those tests are about nor something a host without ffprobe could do.
func TestMain(m *testing.M) {
	clipLength = func(string) (float64, error) { return 1e9, nil }
	os.Exit(m.Run())
}

func stubClipLength(t *testing.T, length float64, err error) {
	t.Helper()
	was := clipLength
	clipLength = func(string) (float64, error) { return length, err }
	t.Cleanup(func() { clipLength = was })
}

// Measured takes, and not invented ones. The slack has to sit between the take
// that kept its ending and every take that lost one, and not merely somewhere.
//
// The windows are the scripts plus the three second tail the engine sleeps
// before stopping, which is what checkTake is handed. The tightest cases are
// the two terminal takes: they lose only about a twentieth of themselves, and
// they are still takes that stopped before their last beat.
func TestCheckTakeSeparatesTheTakesThatLostTheirEnding(t *testing.T) {
	for _, c := range []struct {
		name   string
		window float64
		length float64
		short  bool
	}{
		{"a Qt window, three keys", 18, 9.9, true},
		{"a Qt window, a written profile", 46, 32.3, true},
		{"a Qt window, five views", 62, 46.2, true},
		{"a Qt window, five views again", 74, 50.9, true},
		{"a terminal, the least of the losses", 54, 48.7, true},
		{"a terminal, a long one", 152, 142.7, true},
		{"the take that lost the sites view", 44, 39.3, true},
		{"the take that lost more of it", 50, 41.2, true},
		{"the one take that was held to its end", 62, 61.733, false},
		{"a take a frame under, which every clean take is", 44, 43.9, false},
		{"a long take, where two seconds is noise", 3000, 2997, false},
		{"a long take that is genuinely short", 3000, 2800, true},
		// The reason the slack is capped. Two percent of a fifty-one minute
		// session is a whole minute, so without a cap a take that lost a
		// minute of its ending -- the session closing, which is the point of
		// that film, and an hour of clock to shoot again -- would pass.
		{"fifty-one minutes losing a minute of its ending", 3075, 3015, true},
		{"fifty-one minutes losing six seconds of its ending", 3075, 3069, true},
		// The framebuffer recorder assembles at the rate it measured, so its
		// takes run slightly past their window rather than short of it. Over
		// is never a complaint: the recorder was running for all of it.
		{"fifty minutes off the framebuffer recorder", 3072, 3075.2, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			stubClipLength(t, c.length, nil)
			err := checkTake("/tmp/take.mp4", time.Duration(c.window*float64(time.Second)))
			if c.short && err == nil {
				t.Fatalf("a %.1fs take of a %.0fs recording passed as whole", c.length, c.window)
			}
			if !c.short && err != nil {
				t.Fatalf("a %.1fs take of a %.0fs recording was called short: %v", c.length, c.window, err)
			}
		})
	}
}

// The complaint has to carry both numbers. "The take is short" sends nobody
// anywhere; "39.3 of 44" says how much ending to look for and in which file.
func TestCheckTakeSaysBothLengthsAndThePath(t *testing.T) {
	stubClipLength(t, 39.3, nil)
	err := checkTake("/takes/05-the-panel.mp4", 44*time.Second)
	if err == nil {
		t.Fatal("a short take passed")
	}
	for _, want := range []string{"39.3", "44.0", "4.7", "05-the-panel.mp4"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the complaint does not mention %q: %v", want, err)
		}
	}
}

// A host with no ffprobe still has to be able to film.
func TestCheckTakeCannotBeMeasuredIsNotAFailedTake(t *testing.T) {
	stubClipLength(t, 0, errors.New("exec: ffprobe: not found"))
	if err := checkTake("/tmp/take.mp4", time.Hour); err != nil {
		t.Fatalf("an unmeasurable take was called short: %v", err)
	}
}

// The whole point: a recording that finished, wrote a valid file and passed
// every check the recorder makes for itself, and is still missing its ending,
// must not be reported as a good take.
func TestRunReportsAShortTakeInsteadOfFinishingQuietly(t *testing.T) {
	stubClipLength(t, 0.01, nil)
	var ord []string
	rec := &fakeRec{order: &ord}
	e := &Engine{
		Project: &scene.Project{
			Dir:     t.TempDir(),
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    rec,
		Prompt: &fakePrompt{},
		Speed:  0.1, // real sleeps, scaled down, so the window is real but short
	}
	// A tenth of a second of slack, to match a window measured in tenths.
	wasSlack := takeSlack
	takeSlack = 0.1
	t.Cleanup(func() { takeSlack = wasSlack })

	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait", DelayAfter: 2}}}
	err := e.Run(s, Options{Record: true, Speed: 0.1, OutPath: "/tmp/clip.mp4"})
	if err == nil {
		t.Fatal("a take missing its ending was reported as a good take")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("the run does not say what is wrong: %v", err)
	}
	// Stopped, not abandoned: the recorder ran to the end and the file stays.
	if !rec.stopped {
		t.Error("the recorder was not stopped")
	}
}

// A rehearsal writes no file, so there is nothing to weigh and nothing to say.
func TestRehearsalIsNotWeighed(t *testing.T) {
	stubClipLength(t, 0, errors.New("ffprobe should not have been asked"))
	var ord []string
	e := &Engine{
		Project: &scene.Project{
			Dir:     t.TempDir(),
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    &fakeRec{order: &ord},
		Prompt: &fakePrompt{},
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}
	if err := e.Run(s, Options{Record: false, Speed: 0.0001}); err != nil {
		t.Fatalf("rehearsal: %v", err)
	}
}
