package stage

import (
	"fmt"
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
	Guest *guest.Guest
	// Patience is how long to wait for the domain to boot and answer ssh.
	Patience time.Duration
	// Omarchy is the version the guest reported, filled by Setup and written
	// beside the clip so a take can be reproduced rather than only re-shot.
	Omarchy string
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
func (v *VM) Setup(layout scene.Layout, _ *scene.Project) (*scene.Manifest, error) {
	if err := v.Guest.Start(v.Patience); err != nil {
		return nil, err
	}
	version, err := v.Guest.AssertOmarchy()
	if err != nil {
		return nil, err
	}
	v.Omarchy = version
	if err := v.Guest.Provision(); err != nil {
		return nil, err
	}
	if err := v.Guest.Prepare(); err != nil {
		return nil, err
	}
	if err := v.Guest.OpenTerminal(); err != nil {
		return nil, err
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
	_, _ = v.Guest.InSession("systemctl --user stop backstage-terminal")
	_, _ = v.Guest.Root("pkill -u " + v.Guest.User + " -x foot")
	return nil
}

// Facts writes the provenance sidecar for a clip recorded on this stage.
func (v *VM) Facts(clip string) error {
	if v.Omarchy == "" {
		return fmt.Errorf("the stage never read the guest's Omarchy version")
	}
	return v.Guest.WriteFacts(clip, v.Omarchy)
}
