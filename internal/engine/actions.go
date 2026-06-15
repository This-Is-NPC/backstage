package engine

import (
	"fmt"
	"time"

	"github.com/This-Is-NPC/backstage/internal/prompter"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/transition"
)

// runStep resolves aliases, honors delay-before/after, and dispatches the action.
func (e *Engine) runStep(i int, st scene.Step) error {
	action, target := e.resolve(st)
	fmt.Printf("   step %d: %s -> %s\n", i+1, action, target)

	if st.DelayBefore > 0 {
		e.sleep(st.DelayBefore)
	}

	var err error
	if (action == "run" || action == "type" || action == "keys") && e.pane == nil {
		return fmt.Errorf("action %q needs a staged pane", action)
	}
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
	case "transition":
		err = e.actTransition(st)
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
	if e.rehearsing {
		// Rehearsals are host-independent dry runs; keep timing without opening Hypr.
		if err := e.sleepDialog(st, cps); err != nil {
			return err
		}
		e.sleep(dialogPost)
		return nil
	}
	style := e.Project.Popup.Style
	opts := prompter.Opts{
		CPS: cps, Term: e.Project.Term,
		FontSize: style.FontSize, Title: style.Title, Header: style.Header,
		Chrome: style.Chrome, Class: style.Class,
	}
	if len(e.Project.Popup.Size) == 2 {
		opts.Width, opts.Height = e.Project.Popup.Size[0], e.Project.Popup.Size[1]
	}
	if err := e.Prompt.Show(st.Value, opts); err != nil {
		return err
	}
	if err := e.sleepDialog(st, cps); err != nil {
		return err
	}
	if err := e.Prompt.Close(); err != nil {
		return err
	}
	e.sleep(dialogPost)
	return nil
}

func (e *Engine) sleepDialog(st scene.Step, cps int) error {
	hold := st.Hold
	if hold == 0 {
		hold = defHold
	}
	typeSecs := prompter.TypeDuration(st.Value, float64(cps)).Seconds()
	e.sleep(typeSecs + hold)
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
	return e.runProp("prop "+st.Value, st.Value, st.Args)
}

func (e *Engine) actTransition(st scene.Step) error {
	if st.Value == "" {
		return fmt.Errorf("transition action needs value")
	}
	t, ok := e.Project.Transitions[st.Value]
	if !ok {
		return fmt.Errorf("transition %q not in config", st.Value)
	}
	if t.RenderMode() != scene.RenderLive {
		return fmt.Errorf("transition %q has no live.prop", st.Value)
	}
	// Substitute placeholders via the shared resolver so an in-scene step and a
	// production segment apply the SAME config fallbacks. {{out}} is intentionally
	// omitted for live props (the recorder owns the clip). {{fps}} falls back to
	// record.fps. {{w}}/{{h}} are the configured render dims; when render.w/h are
	// unset they substitute to "0" ("monitor native") rather than concrete pixels:
	// unlike production, an in-scene step does not record a probe clip, so the true
	// native size is not knowable here. The prop must treat 0 as "native". (This is
	// the only deliberate divergence from production, which probes for real pixels.)
	fps, w, h := e.Project.ResolveRenderDims()
	v := transition.Vars{W: w, H: h, FPS: fps}
	args := transition.SubstituteLiveArgs(t.Live.Args, v)
	args = append(args, transition.SubstituteLiveArgs(st.Args, v)...)
	return e.runProp("transition "+st.Value, t.Live.Prop, args)
}

func (e *Engine) runProp(label, rel string, args []string) error {
	cmd, err := e.Project.PropCommand(rel, args)
	if err != nil {
		return err
	}
	if err := e.runCommand(cmd); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}
