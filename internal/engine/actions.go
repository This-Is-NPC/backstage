package engine

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/This-Is-NPC/backstage/internal/prompter"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

// runStep resolves aliases, honors delay-before/after, and dispatches the action.
func (e *Engine) runStep(i int, st scene.Step) error {
	action, target := e.resolve(st)
	fmt.Printf("   step %d: %s -> %s\n", i+1, action, target)

	if st.DelayBefore > 0 {
		e.sleep(st.DelayBefore)
	}

	var err error
	switch action {
	case "dialog":
		err = e.actDialog(st)
	case "run":
		err = e.pane.Run(target, st.Value)
	case "type":
		err = e.pane.Type(target, st.Value)
	case "keys":
		err = e.actKeys(target, st)
	case "prop":
		err = e.actProp(st)
	case "wait":
		// pause only
	default:
		err = fmt.Errorf("unknown action %q", action)
	}

	after := st.DelayAfter
	if after == 0 {
		after = defDelayAfter
	}
	e.sleep(after)
	return err
}

// resolve expands a configured alias into a canonical action + default target.
func (e *Engine) resolve(st scene.Step) (action, target string) {
	action, target = st.Action, st.Target
	if al, ok := e.Project.Aliases[action]; ok {
		action = al.Action
		if target == "" {
			target = al.Target
		}
	}
	return action, target
}

// actDialog shows the floating instruction box, holds while it types, then closes.
func (e *Engine) actDialog(st scene.Step) error {
	cps := e.Project.Popup.CPS
	opts := prompter.Opts{CPS: cps, Term: e.Project.Term}
	if len(e.Project.Popup.Size) == 2 {
		opts.Width, opts.Height = e.Project.Popup.Size[0], e.Project.Popup.Size[1]
	}
	if err := e.Prompt.Show(st.Value, opts); err != nil {
		return err
	}
	hold := st.Hold
	if hold == 0 {
		hold = defHold
	}
	typeSecs := prompter.TypeDuration(st.Value, float64(cps)).Seconds()
	e.sleep(typeSecs + hold)
	if err := e.Prompt.Close(); err != nil {
		return err
	}
	e.sleep(dialogPost)
	return nil
}

func (e *Engine) actKeys(target string, st scene.Step) error {
	kd := st.KeyDelay
	if kd == 0 {
		kd = defKeyDelay
	}
	return e.pane.Keys(target, st.Commands, time.Duration(kd*float64(time.Second)))
}

// actProp runs an external script (RPA or any executable). It resolves a
// project-relative path, inherits the project env, blocks until exit,
// and reports a non-zero exit to the engine.
func (e *Engine) actProp(st scene.Step) error {
	if st.Value == "" {
		return nil
	}
	path, err := e.Project.SafePath(st.Value)
	if err != nil {
		return err
	}
	cmd := exec.Command(path, st.Args...)
	cmd.Dir = e.Project.Dir
	cmd.Env = e.env()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("prop %s: %w", st.Value, err)
	}
	return nil
}
