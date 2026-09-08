package pane

import (
	"time"

	"github.com/This-Is-NPC/backstage/internal/guest"
)

// Guest drives a scene by typing on an Omarchy guest's own keyboard.
//
// There are no panes here and that is the difference from Tmux: the target of
// every step is the guest itself, and the keystrokes land wherever that
// machine's compositor is pointing -- which is the terminal the stage opened,
// because Omarchy focuses what it opens. A scene reads the same either way; it
// is the stage that decides whether `target` names a pane or a computer.
type Guest struct {
	Box      *guest.Guest
	KeyDelay time.Duration
}

// NewGuest returns a driver for one guest.
func NewGuest(g *guest.Guest) *Guest {
	return &Guest{Box: g, KeyDelay: 14 * time.Millisecond}
}

// Run types a command and presses Enter.
func (g *Guest) Run(_, cmd string) error {
	if err := g.Box.Type(cmd, g.KeyDelay); err != nil {
		return err
	}
	return g.Box.Key("enter")
}

// Type types literal text and presses nothing.
func (g *Guest) Type(_, text string) error {
	return g.Box.Type(text, g.KeyDelay)
}

// Keys sends named keys and literals in order.
//
// A name this stage knows is a key; anything else is typed as text, which is
// the same contract the tmux driver offers and the reason a scene can move
// between the two.
func (g *Guest) Keys(_ string, commands []string, keyDelay time.Duration) error {
	for _, command := range commands {
		var err error
		if _, known := guest.Codes[command]; known || len(command) > 1 && containsPlus(command) {
			err = g.Box.Key(command)
		} else {
			err = g.Box.Type(command, g.KeyDelay)
		}
		if err != nil {
			return err
		}
		if keyDelay > 0 {
			time.Sleep(keyDelay)
		}
	}
	return nil
}

func containsPlus(s string) bool {
	for _, r := range s {
		if r == '+' {
			return true
		}
	}
	return false
}
