package engine

import (
	"fmt"
	"os"
	"time"

	"github.com/This-Is-NPC/backstage/internal/recorder"
)

// clipLength reads how long a finished take is. A package var so a test can
// weigh a take that was never filmed.
var clipLength = recorder.Duration

// How much shorter than the window it was filmed in a take may be before
// backstage calls it short.
//
// Not zero, because even a clean take loses a fraction of a second: the encoder
// stops counting at the last whole frame it wrote, and the window is measured
// from a poll that noticed the file a moment after the first frame went into
// it. But the takes worth catching are not a fraction short. Measured, over
// nine takes of one guest: the one take that was held to its end came back a
// quarter of a second under its window, and every take that lost its ending
// came back between five and twenty seconds under -- a twentieth of itself
// where a terminal was on screen, and better than a fifth of itself where a
// windowed program was. Two seconds sits well above the first and well below
// the rest.
//
// A var and not a const only so a test can weigh a one-second take without
// first sleeping for a real scene's worth of seconds.
var takeSlack = 2.0 // seconds

// Of the window, for takes long enough that two seconds is noise -- but capped,
// because a slack that grows without limit stops guarding the takes that most
// need guarding. Two percent of a fifty-one minute session is a whole minute:
// a take that lost a minute of its ending would pass in silence, and that is
// the take nobody can casually shoot again. The cap is the smallest loss ever
// measured, so the slack never grows past a gap that has really happened.
const (
	takeSlackPct = 0.02
	takeSlackCap = 5.0 // seconds
)

// checkTake reports a take that is materially shorter than the window it was
// recorded in.
//
// **This is the only thing that notices a lost ending.** A take can come back
// short with every check each recorder makes for itself passing: the process
// exited cleanly, the container is valid and whole, the file is not empty, and
// the film inside plays at real speed with no gap in it. It simply stops
// early -- and what it stops before is the tail, which is where a scene puts
// the thing it was made to show, the app that opens, the panel that adds up,
// the session that ends. Backstage is the only party holding both numbers: it
// started the clock and it can read the clip.
//
// The window is wall-clock and not the sum of the scene's delays on purpose. A
// prop, a run step or a slow guest takes as long as it takes, and a scene run
// at a rehearsal speed asks for delays it will not sleep; the clock covers all
// of it, and the recorder was running for every second the clock counted.
func checkTake(path string, window time.Duration) error {
	length, err := clipLength(path)
	if err != nil {
		// A take that cannot be measured is not a take that is wrong, and a
		// host without ffprobe must still be able to film. Say so and pass:
		// silence here would be the same silence this check exists to end.
		fmt.Fprintf(os.Stderr, "   !! the take could not be measured, so its length went unchecked: %v\n", err)
		return nil
	}
	want := window.Seconds()
	slack := takeSlack
	if p := want * takeSlackPct; p > slack {
		slack = p
	}
	if slack > takeSlackCap {
		slack = takeSlackCap
	}
	if length >= want-slack {
		return nil
	}
	return fmt.Errorf("the take is %.1fs but the recorder ran for %.1fs: %.1fs is missing, "+
		"and what a scene loses when a recorder falls behind is its ending -- "+
		"check %s for the last thing the scene did",
		length, want, want-length, path)
}
