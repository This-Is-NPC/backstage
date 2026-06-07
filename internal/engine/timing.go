package engine

import "time"

// Step timing defaults, ported from the legacy runner.
const (
	defDelayAfter = 0.5 // seconds after a step when none is given
	defKeyDelay   = 0.35
	defHold       = 1.5 // how long a dialog stays after typing
	stageWarm     = 3.5 // pause after staging before recording
	endWait       = 3.0 // tail pause before stopping the recorder
	dialogPost    = 0.6 // pause after closing a dialog
)

// sleep pauses for seconds, scaled by the engine speed factor (rehearse < 1).
func (e *Engine) sleep(seconds float64) {
	f := e.Speed
	if f <= 0 {
		f = 1
	}
	time.Sleep(time.Duration(seconds * f * float64(time.Second)))
}
