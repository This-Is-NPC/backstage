package engine

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

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
}

// Engine runs a scene over the stage/recorder/prompter/pane drivers.
type Engine struct {
	Project *scene.Project
	Stager  stage.Stager
	Rec     recorder.Recorder
	Prompt  prompter.Prompter

	Speed float64
	pane  pane.Driver
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

// Run stages the scene's layout, optionally records, executes every step, then
// stops. The layout is assumed validated against the project (scene.Validate).
func (e *Engine) Run(s *scene.Scene, opts Options) (runErr error) {
	e.Speed = opts.Speed
	if e.Speed <= 0 {
		e.Speed = 1
	}

	layout, ok := e.Project.Layouts[s.LayoutName()]
	if !ok {
		return fmt.Errorf("layout %q not in config", s.LayoutName())
	}

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
	var recMu sync.Mutex
	recStarted := false
	recStopped := false
	stopRec := func() error {
		recMu.Lock()
		defer recMu.Unlock()
		if !opts.Record || !recStarted || recStopped {
			return nil
		}
		recStopped = true
		fmt.Println(">> stop recording")
		_, err := e.Rec.Stop()
		return err
	}
	sigCh := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer func() {
		signal.Stop(sigCh)
		close(done)
		if err := stopRec(); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()
	go func() {
		select {
		case <-sigCh:
			_ = stopRec()
			if e.Prompt != nil {
				_ = e.Prompt.Close()
			}
			if e.Stager != nil {
				_ = e.Stager.Teardown()
			}
			os.Exit(130)
		case <-done:
		}
	}()
	startRec := func() error {
		if !opts.Record {
			return nil
		}
		fmt.Println(">> start recording")
		if err := e.Rec.Start(out); err != nil {
			return err
		}
		recMu.Lock()
		recStarted = true
		recStopped = false
		recMu.Unlock()
		return nil
	}

	// ShowStaging: capture the stage montage too (record before staging).
	if opts.ShowStaging {
		if err := startRec(); err != nil {
			return err
		}
	}

	fmt.Printf(">> stage layout: %s\n", s.LayoutName())
	m, err := e.Stager.Setup(layout, e.Project)
	if err != nil {
		return err
	}
	e.pane = pane.NewTmux(m)
	e.sleep(stageWarm)

	// Default: start after the stage is ready, hiding the setup.
	if !opts.ShowStaging {
		if err := startRec(); err != nil {
			return err
		}
	}

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
	cmd.Env = e.env()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hook %s: %w", rel, err)
	}
	return nil
}

// env is the process environment plus the project's exported env block.
func (e *Engine) env() []string {
	env := os.Environ()
	for k, v := range e.Project.Env {
		env = append(env, k+"="+v)
	}
	return env
}
