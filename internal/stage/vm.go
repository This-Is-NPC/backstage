package stage

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/This-Is-NPC/backstage/internal/guest"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

// VM stages one Omarchy guest: it boots the machine, refuses it if it is not
// Omarchy, installs what a stage needs, makes the desktop filmable, and leaves
// a terminal open.
//
// It is a Stager like Hypr is, and the swap is the whole design: a scene that
// names a vm gets its steps typed on that computer's own keyboard and its
// screen recorded from inside, and the scene file says nothing about either.
//
// The layout's panes are read for their *names* and nothing else. A vm stage
// has one screen, so `target` selects which computer a step is typed at when a
// production drives more than one -- and within a single take every step lands
// on the one machine. Two computers are two takes, composed afterwards, which
// is what keeps a take from having to synchronise two recorders.
type VM struct {
	// Continue preserves the entire live session rather than restaging it.
	Continue bool
	Guest    *guest.Guest
	// Patience is how long to wait for the domain to boot and answer ssh.
	Patience time.Duration
	// Omarchy is the version the guest reported, filled by Setup. The engine
	// writes it beside the clip so a take can be reproduced rather than only re-shot.
	Omarchy string
	// Now, if set, is the clock for phase durations. Nil uses time.Now.
	Now func() time.Time
	// Phases is each Setup phase that completed, including "up".
	Phases map[string]float64
	// SessionSeconds is omarchy+tools+desktop+terminal. It excludes "up".
	SessionSeconds *float64
}

// RecordingGuest returns the guest and the Omarchy version recorded at Setup.
func (v *VM) RecordingGuest() (*guest.Guest, string) {
	return v.Guest, v.Omarchy
}

// SessionTimings returns completed Setup phases and the session sum (no "up").
func (v *VM) SessionTimings() (map[string]float64, *float64) {
	return v.Phases, v.SessionSeconds
}

// NewVM returns a stager for one guest.
func NewVM(g *guest.Guest) *VM {
	return &VM{Guest: g, Patience: 5 * time.Minute}
}

// Setup brings the guest up and makes it ready to be filmed.
//
// The order is not arrangeable. Assert before provision, because installing on
// a machine this cannot drive is a change nobody asked for; provision before
// prepare, because preparing needs the keyboard it installs; prepare before the
// terminal, because a locked session swallows the keystroke that would open it.
func (v *VM) now() time.Time {
	if v != nil && v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v *VM) Setup(layout scene.Layout, proj *scene.Project) (*scene.Manifest, error) {
	return v.setup(layout, func(name string) error {
		switch name {
		case "up":
			return v.Guest.Start(v.Patience)
		case "omarchy":
			version, err := v.Guest.AssertOmarchy()
			v.Omarchy = version
			return err
		case "tools":
			return v.Guest.Provision()
		case "desktop":
			return v.Guest.Prepare()
		case "terminal":
			return v.Guest.OpenTerminal()
		default:
			return fmt.Errorf("unknown stage phase %s", name)
		}
	})
}

func (v *VM) setup(layout scene.Layout, do func(string) error) (*scene.Manifest, error) {
	// Each phase is timed and said out loud, because the cost of a vm stage is
	// not one number: a guest that is already up is seconds and a cold boot is
	// minutes, and somebody deciding whether to keep a stage between takes
	// needs to see which of the two they are paying for.
	v.Phases = map[string]float64{}
	v.SessionSeconds = nil
	var session float64
	phase := func(name string, inSession bool) error {
		began := v.now()
		if err := do(name); err != nil {
			return err
		}
		sec := math.Round(v.now().Sub(began).Seconds()*1000) / 1000
		v.Phases[name] = sec
		if inSession {
			session += sec
			sum := math.Round(session*1000) / 1000
			v.SessionSeconds = &sum
		}
		domain := ""
		if v.Guest != nil {
			domain = v.Guest.Domain
		}
		fmt.Printf(">> stage %s: %s (%.1fs)\n", domain, name, sec)
		return nil
	}

	if !v.Continue {
		if err := phase("up", false); err != nil {
			return nil, err
		}
	}
	if err := phase("omarchy", true); err != nil {
		return nil, err
	}
	if !v.Continue {
		if err := phase("tools", true); err != nil {
			return nil, err
		}
		if err := phase("desktop", true); err != nil {
			return nil, err
		}
		if err := phase("terminal", true); err != nil {
			return nil, err
		}
	}

	// Names only. There is nothing to resolve them to -- one guest is one
	// screen -- and the manifest exists so a scene written for a tmux stage
	// reads unchanged here.
	manifest := &scene.Manifest{Panes: map[string]string{}}
	for _, pane := range layout.Panes {
		manifest.Panes[pane.Name] = v.Guest.Domain
		manifest.Order = append(manifest.Order, pane.Name)
	}
	if len(manifest.Order) == 0 {
		manifest.Panes[v.Guest.Domain] = v.Guest.Domain
		manifest.Order = []string{v.Guest.Domain}
	}
	return manifest, nil
}

// Teardown leaves the guest running and takes back only what this stage added
// to the screen.
//
// Deliberately not a shutdown. A take is usually one of several, booting an
// Omarchy guest costs minutes, and a stage that powered the machine down
// between scenes would spend most of a production waiting. What it does undo is
// the terminal it opened, so the next Setup starts from one rather than from
// however many takes have run.
func (v *VM) Teardown() error {
	if v.Guest == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	g := *v.Guest
	g.Context = ctx
	_, _ = g.InSession("systemctl --user stop backstage-open")
	_, _ = g.Root("pkill -u " + g.User + " -x foot")
	return nil
}
