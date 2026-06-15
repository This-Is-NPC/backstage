package engine

import (
	"errors"
	"os/exec"
	"sync"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

var ErrCommandInterrupted = errors.New("command start canceled by interrupt")

// CommandGuard gates command starts against interrupt cleanup. If Interrupt races
// with Start, Interrupt waits until Start has either failed or produced a process
// handle before killing the command's process group.
type CommandGuard struct {
	mu          sync.Mutex
	interrupted bool
	active      *exec.Cmd
}

func (g *CommandGuard) Start(cmd *exec.Cmd) error {
	if g == nil {
		return cmd.Start()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.interrupted {
		return ErrCommandInterrupted
	}
	g.active = cmd
	if err := cmd.Start(); err != nil {
		if g.active == cmd {
			g.active = nil
		}
		return err
	}
	return nil
}

func (g *CommandGuard) Done(cmd *exec.Cmd) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active == cmd {
		g.active = nil
	}
}

func (g *CommandGuard) Interrupt() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.interrupted = true
	_ = scene.KillProcessGroup(g.active)
}
