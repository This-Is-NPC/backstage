package pane

import (
	"os/exec"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

// Driver targets a named pane and sends input to it.
type Driver interface {
	// Run types a command into the target pane and presses Enter.
	Run(target, cmd string) error
	// Type types literal text into the target pane without pressing Enter.
	Type(target, text string) error
	// Keys sends a sequence of named keys / literals, pausing keyDelay between each.
	Keys(target string, commands []string, keyDelay time.Duration) error
}

// Tmux drives panes via tmux send-keys, resolving target names through the
// manifest (empty/unknown target falls back to the first pane).
type Tmux struct {
	Manifest *scene.Manifest
	// runEnterGap is the pause between typing a command and pressing Enter.
	runEnterGap time.Duration
}

// NewTmux returns a Tmux driver for the given staged manifest.
func NewTmux(m *scene.Manifest) *Tmux {
	return &Tmux{Manifest: m, runEnterGap: 300 * time.Millisecond}
}

func (t *Tmux) pane(target string) string {
	return t.Manifest.Pane(target)
}

func sendKeys(args ...string) error {
	return exec.Command("tmux", append([]string{"send-keys"}, args...)...).Run()
}

// Run types cmd literally then presses Enter.
func (t *Tmux) Run(target, cmd string) error {
	if cmd == "" {
		return nil
	}
	p := t.pane(target)
	if err := sendKeys("-t", p, "-l", cmd); err != nil {
		return err
	}
	time.Sleep(t.runEnterGap)
	return sendKeys("-t", p, "Enter")
}

// Type types text literally, no Enter.
func (t *Tmux) Type(target, text string) error {
	if text == "" {
		return nil
	}
	return sendKeys("-t", t.pane(target), "-l", text)
}

// Keys sends each command as a named key or literal, pausing keyDelay between.
func (t *Tmux) Keys(target string, commands []string, keyDelay time.Duration) error {
	p := t.pane(target)
	for _, cmd := range commands {
		kind, tok := ToToken(cmd)
		var err error
		if kind == KindLit {
			err = sendKeys("-t", p, "-l", tok)
		} else {
			err = sendKeys("-t", p, tok)
		}
		if err != nil {
			return err
		}
		time.Sleep(keyDelay)
	}
	return nil
}
