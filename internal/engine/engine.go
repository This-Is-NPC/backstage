package engine

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/This-Is-NPC/backstage/internal/guest"
	"github.com/This-Is-NPC/backstage/internal/pane"
	"github.com/This-Is-NPC/backstage/internal/prompter"
	"github.com/This-Is-NPC/backstage/internal/recorder"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/stage"
)

// Options tune a run. Record off + Speed < 1 is a rehearsal (dry-run).
type Options struct {
	Record bool
	Speed  float64 // delay multiplier; 1 = real time, smaller = faster rehearsal

	// OutPath overrides where the recording is written. Empty uses the default
	// <project>/<record.out>/<scene>.mp4. Used by the production pipeline to
	// record each scene to its own clip.
	OutPath string
	// ShowStaging starts recording before the stage is built, so the stage
	// montage appears in the video. Default (false) starts after the stage is
	// ready, hiding the setup.
	ShowStaging bool
	// OnInterrupt runs after recorder/popup/stage cleanup but before the interrupt
	// handler exits. Production uses this to remove its segment work directory.
	OnInterrupt func()
}

// Engine runs a scene over the stage/recorder/prompter/pane drivers.
type Engine struct {
	Project *scene.Project
	Stager  stage.Stager
	Rec     recorder.Recorder
	Prompt  prompter.Prompter

	Speed float64
	// PaneDriver overrides the default tmux driver. A vm stage sets it,
	// because on that stage a target names a computer and not a pane.
	PaneDriver pane.Driver
	pane       pane.Driver
	rehearsing bool
	cmdGuard   *CommandGuard
}

// New builds an Engine with the default Hyprland/gpu drivers for a project.
func New(p *scene.Project) *Engine {
	return &Engine{
		Project: p,
		Stager:  &stage.Hypr{},
		Rec:     recorder.NewGPU(p.Record.Monitor, p.Record.FPS),
		Prompt:  &prompter.Hypr{},
		Speed:   1,
	}
}

// NewForScene builds an Engine with the drivers the scene asks for.
//
// A scene with no `vm` gets the ordinary stage: a tmux layout in a window on
// this machine, recorded off a monitor. A scene that names a vm gets all four
// drivers swapped at once -- stage, recorder, panes, and in time the prompter
// -- because the machine being filmed changes every one of them together. That
// is why they are chosen here and not each in its own place: a run with a guest
// stage and a host recorder would record this desktop while typing on another.
func NewForScene(p *scene.Project, s *scene.Scene) (*Engine, error) {
	e := New(p)
	if s == nil || s.VM == "" {
		return e, nil
	}
	cfg, ok := p.VMs[s.VM]
	if !ok {
		return nil, fmt.Errorf("scene %q runs on vm %q, which backstage.json does not declare",
			s.Name, s.VM)
	}
	if cfg.Domain == "" {
		return nil, fmt.Errorf("vm %q names no libvirt domain", s.VM)
	}
	if cfg.User == "" {
		return nil, fmt.Errorf("vm %q names no user; the stage films that account's session "+
			"and connects as it", s.VM)
	}
	box := guest.New(cfg.Domain, cfg.User, cfg.Admin, cfg.Key, cfg.URI)
	box.Open = cfg.Open
	box.Language = cfg.Language
	e.Stager = stage.NewVM(box)

	// The scene wins over the vm, because the scene is what knows whether it is
	// about to kill the session it is being recorded from.
	which := cfg.Recorder
	if s.Recorder != "" {
		which = s.Recorder
	}
	switch which {
	case "", "inside":
		e.Rec = recorder.NewWF(box, p.Record.FPS)
	case "framebuffer":
		e.Rec = recorder.NewFramebuffer(cfg.Domain, cfg.URI, p.Record.FPS)
	default:
		return nil, fmt.Errorf("scene %q asks for recorder %q; say `inside` or `framebuffer`",
			s.Name, which)
	}
	e.PaneDriver = pane.NewGuest(box)
	return e, nil
}

// Run stages the scene's layout, optionally records, executes every step, then
// stops. The layout is assumed validated against the project (scene.Validate).
func (e *Engine) Run(s *scene.Scene, opts Options) (runErr error) {
	e.Speed = opts.Speed
	if e.Speed <= 0 {
		e.Speed = 1
	}
	prevRehearsing := e.rehearsing
	e.rehearsing = !opts.Record
	defer func() { e.rehearsing = prevRehearsing }()

	layout, ok := e.Project.Layouts[s.LayoutName()]
	if !ok {
		return fmt.Errorf("layout %q not in config", s.LayoutName())
	}

	// Preflight the prompter/host before any recording. Any run that WILL record a
	// Hypr-driven overlay must fail fast, not finalize an overlay-less video. The
	// readiness check matches what the scene actually needs:
	//   - a dialog step opens the prompter terminal → needs hyprctl AND the terminal
	//     (full Preflight);
	//   - a live transition step records a prop over the compositor overlay but never
	//     opens the terminal → needs hyprctl only (PreflightHypr), so it must not
	//     fail on a host missing the terminal.
	// Only fail-fast when actually recording: a rehearse/dry-run (Record==false)
	// produces no video, so it must not require hyprctl/terminal on a non-Hypr host.
	if opts.Record && e.Prompt != nil {
		switch {
		case e.hasDialogStep(s):
			if err := e.Prompt.Preflight(e.Project.Term); err != nil {
				return err
			}
		case e.recordsHyprOverlay(s):
			if err := e.Prompt.PreflightHypr(); err != nil {
				return err
			}
		}
	}

	var recMu sync.Mutex
	recArmed := false
	recStopped := false
	stopRec := func() error {
		recMu.Lock()
		if !opts.Record || !recArmed || recStopped {
			recMu.Unlock()
			return nil
		}
		recStopped = true
		recMu.Unlock()
		fmt.Println(">> stop recording")
		_, err := e.Rec.Stop()
		return err
	}
	// Cancel active command starts, stop the recorder, and tear down overlays on
	// interrupt, then exit.
	// Shared with production.recordLiveTransition via InterruptGuard so the
	// command-cancel + recorder-stop-once + signal-exit pattern can't drift between
	// the two sites.
	// (recArmed/recStopped stay under recMu here because startRec sets them
	// concurrently; the guard provides the signal handling and exit.)
	//
	// The guard owns command cancellation and recorder Stop before running this
	// onInterrupt, so the closure must not touch the recorder or the guard itself --
	// it only does overlay and caller cleanup. Not referencing the outer guard also
	// removes the construction-window nil-deref the closure used to risk.
	prevCmdGuard := e.cmdGuard
	cmdGuard := &CommandGuard{}
	e.cmdGuard = cmdGuard
	defer func() { e.cmdGuard = prevCmdGuard }()

	guard := newInterruptGuard(
		func() error {
			cmdGuard.Interrupt()
			return stopRec()
		},
		func() { e.cleanupOnInterrupt(opts.OnInterrupt) },
	)
	defer func() {
		if err := guard.Stop(); err != nil {
			runErr = errors.Join(runErr, err)
		}
		guard.Release()
	}()

	if err := e.runHooks(s); err != nil {
		return err
	}

	out := opts.OutPath
	if out == "" {
		if err := scene.ValidateName("scene", s.Name); err != nil {
			return err
		}
		var err error
		out, err = e.Project.SafePath(e.Project.Record.Out, s.Name+".mp4")
		if err != nil {
			return err
		}
	}
	startRec := func() error {
		if !opts.Record {
			return nil
		}
		recMu.Lock()
		recArmed = true
		recStopped = false
		recMu.Unlock()
		fmt.Println(">> start recording")
		if err := e.Rec.Start(out); err != nil {
			return err
		}
		return nil
	}

	usesStage := len(layout.Panes) > 0

	// ShowStaging: capture the stage montage too (record before staging).
	if opts.ShowStaging {
		if err := startRec(); err != nil {
			return err
		}
	}

	if usesStage {
		fmt.Printf(">> stage layout: %s\n", s.LayoutName())
		m, err := e.Stager.Setup(layout, e.Project)
		if err != nil {
			return err
		}
		if e.PaneDriver != nil {
			e.pane = e.PaneDriver
		} else {
			e.pane = pane.NewTmux(m)
		}
		e.sleep(stageWarm)
	} else {
		fmt.Printf(">> stage layout: %s (none)\n", s.LayoutName())
		e.pane = nil
	}

	// Default: start after the stage is ready, hiding the setup.
	if !opts.ShowStaging {
		if err := startRec(); err != nil {
			return err
		}
	}

	// Continue-on-error is intentional: for a live recorder a partial take beats
	// a discarded one, so a failed step is logged and joined into runErr (surfaced
	// to the caller) rather than aborting the remaining steps.
	for i, st := range s.Steps {
		if err := e.runStep(i, st); err != nil {
			fmt.Fprintf(os.Stderr, "   !! step %d: %v\n", i+1, err)
			runErr = errors.Join(runErr, fmt.Errorf("step %d: %w", i+1, err))
		}
	}

	e.sleep(endWait)
	if opts.Record {
		if err := stopRec(); err != nil {
			return err
		}
		fmt.Printf(">> done. %s  (stage open — backstage kill)\n", out)
	} else {
		fmt.Println(">> rehearsal done (no recording).  (stage open — backstage kill)")
	}
	return runErr
}

func (e *Engine) cleanupOnInterrupt(extra func()) {
	// Primary recorder/command cleanup is owned by the guard; repeat the command
	// interrupt here so direct cleanupOnInterrupt tests and future callers stay safe.
	e.interruptActiveCommand()
	if e.Prompt != nil {
		_ = e.Prompt.Close()
	}
	if e.Stager != nil {
		_ = e.Stager.Teardown()
	}
	if extra != nil {
		extra()
	}
}

func (e *Engine) interruptActiveCommand() {
	if e.cmdGuard != nil {
		e.cmdGuard.Interrupt()
	}
}

// hasDialogStep reports whether the scene has a dialog step, which opens the
// prompter terminal and so requires the full hyprctl+terminal preflight. Aliases
// are resolved so an aliased dialog step is still detected.
func (e *Engine) hasDialogStep(s *scene.Scene) bool {
	for _, st := range s.Steps {
		if action, _ := e.resolve(st); action == "dialog" {
			return true
		}
	}
	return false
}

// recordsHyprOverlay reports whether the scene will record any Hypr-driven
// overlay — a dialog step (popup) or a live transition step (recorded prop) —
// so the host-readiness preflight runs before recording for every such path.
// Aliases are resolved so an aliased dialog/transition step is still detected.
func (e *Engine) recordsHyprOverlay(s *scene.Scene) bool {
	for _, st := range s.Steps {
		action, _ := e.resolve(st)
		switch action {
		case "dialog":
			return true
		case "transition":
			// Only a live transition records a Hypr overlay; an offline-cmd
			// transition used as a step is rejected by validation, but guard here.
			if t, ok := e.Project.Transitions[st.Value]; ok && t.RenderMode() == scene.RenderLive {
				return true
			}
		}
	}
	return false
}

// runHooks runs the setup hook for a fresh scene, else the reset hook.
func (e *Engine) runHooks(s *scene.Scene) error {
	h := e.Project.Hooks
	switch {
	case s.Fresh && h.Setup != "":
		fmt.Println(">> fresh: setup hook")
		return e.runScript(h.Setup)
	case s.ResetEnabled() && h.Reset != "":
		fmt.Println(">> reset hook")
		return e.runScript(h.Reset)
	}
	return nil
}

// runScript runs a project-relative hook script with the project env, in the
// project directory.
func (e *Engine) runScript(rel string) error {
	path, err := e.Project.SafePath(rel)
	if err != nil {
		return err
	}
	cmd := exec.Command(path)
	cmd.Dir = e.Project.Dir
	cmd.Env = e.Project.PropEnv()
	scene.SetProcessGroup(cmd)
	var runErr error
	if e.cmdGuard != nil {
		runErr = e.runCommand(cmd)
	} else {
		runErr = runInterruptibleCommand(cmd)
	}
	if runErr != nil {
		return fmt.Errorf("hook %s: %w", rel, runErr)
	}
	return nil
}

func (e *Engine) runCommand(cmd *exec.Cmd) error {
	g := e.cmdGuard
	if g == nil {
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Wait()
	}
	if err := g.Start(cmd); err != nil {
		return err
	}
	defer g.Done(cmd)
	return cmd.Wait()
}

func runInterruptibleCommand(cmd *exec.Cmd) error {
	cmdGuard := &CommandGuard{}
	guard := newInterruptGuard(func() error {
		cmdGuard.Interrupt()
		return nil
	}, nil)
	defer guard.Release()

	if err := cmdGuard.Start(cmd); err != nil {
		return err
	}
	defer cmdGuard.Done(cmd)
	return cmd.Wait()
}
