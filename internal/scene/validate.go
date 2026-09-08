package scene

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/This-Is-NPC/backstage/internal/prompter"
)

// knownActions are the canonical step actions the engine understands. Aliases
// (resolved from project config) must expand to one of these.
var knownActions = map[string]bool{
	"dialog":     true,
	"run":        true,
	"type":       true,
	"keys":       true,
	"prop":       true,
	"transition": true,
	"wait":       true,
}

var popupClassRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ValidateConfig checks project-level settings after defaults are applied.
func (p *Project) ValidateConfig() error {
	style := p.Popup.Style
	if style.FontSize < 0 {
		return fmt.Errorf("popup.style.fontSize must not be negative")
	}
	switch style.Chrome {
	case "default", "minimal", "none":
		// ok
	default:
		return fmt.Errorf("popup.style.chrome must be one of default, minimal, none")
	}
	if style.Class == "" || !popupClassRE.MatchString(style.Class) {
		return fmt.Errorf("popup.style.class must contain only letters, numbers, dot, underscore, and dash")
	}
	if err := prompter.ValidateTitle(style.Title); err != nil {
		return err
	}
	return nil
}

// Validate checks a scene against its project: the layout must exist and every
// step must carry an action that is either canonical or a configured alias.
func (s *Scene) Validate(p *Project) error {
	if s.Name != "" {
		if err := ValidateName("scene", s.Name); err != nil {
			return err
		}
	}
	layout := s.LayoutName()
	if layout == "" {
		return fmt.Errorf("scene %q: no layout", s.Name)
	}
	layoutCfg, ok := p.Layouts[layout]
	if !ok {
		return fmt.Errorf("scene %q: layout %q not in config", s.Name, layout)
	}
	if len(s.Steps) == 0 {
		return fmt.Errorf("scene %q: no steps", s.Name)
	}
	for i, st := range s.Steps {
		if st.Action == "" {
			return fmt.Errorf("scene %q: step %d has no action", s.Name, i+1)
		}
		action := st.Action
		if !knownActions[action] {
			al, ok := p.Aliases[action]
			if !ok {
				return fmt.Errorf("scene %q: step %d unknown action %q", s.Name, i+1, st.Action)
			}
			action = al.Action
			if !knownActions[action] {
				return fmt.Errorf("scene %q: step %d alias %q expands to unknown action %q", s.Name, i+1, st.Action, action)
			}
		}
		if action == "transition" {
			if st.Value == "" {
				return fmt.Errorf("scene %q: step %d transition action needs value", s.Name, i+1)
			}
			// In-scene transition steps must run as a live overlay; require live
			// mode in addition to the shared transition checks (a valid offline
			// cmd is still validated when present so the rules can't diverge).
			if err := p.ValidateTransition(st.Value); err != nil {
				return fmt.Errorf("scene %q: step %d: %w", s.Name, i+1, err)
			}
			if t := p.Transitions[st.Value]; t.RenderMode() != RenderLive {
				return fmt.Errorf("scene %q: step %d: transition %q has no live.prop", s.Name, i+1, st.Value)
			}
		}
		if len(layoutCfg.Panes) == 0 && (action == "run" || action == "type" || action == "keys") {
			return fmt.Errorf("scene %q: step %d action %q needs a layout with panes", s.Name, i+1, action)
		}
	}
	return nil
}

// Production looks up a declared production by name.
func (p *Project) Production(name string) (Production, error) {
	prod, ok := p.Productions[name]
	if !ok {
		return Production{}, fmt.Errorf("production %q not in config", name)
	}
	return prod, nil
}

// ValidateProduction checks a production: every scene file exists, every
// referenced transition is defined and writes to {{out}}, and each transition's
// "after" names a scene in the sequence.
func (p *Project) ValidateProduction(prod Production) error {
	if len(prod.Scenes) == 0 {
		return fmt.Errorf("production has no scenes")
	}
	inSeq := map[string]bool{}
	for _, ref := range prod.Scenes {
		sc := ref.Scene
		path, err := p.ScenePathSafe(sc)
		if err != nil {
			return err
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("scene %q not found at %s", sc, path)
		}
		inSeq[sc] = true
	}
	seenAfter := map[string]bool{}
	for _, tu := range prod.Transitions {
		if seenAfter[tu.After] {
			return fmt.Errorf("duplicate transition after %q", tu.After)
		}
		seenAfter[tu.After] = true
		// after "" is the intro (plays before the first scene); any other value
		// must name a scene in the sequence.
		if tu.After != "" && !inSeq[tu.After] {
			return fmt.Errorf("transition after %q: not a scene in this production", tu.After)
		}
		if err := p.ValidateTransition(tu.Use); err != nil {
			return err
		}
	}
	return nil
}

// ValidateTransition checks a transition is defined and has at least one render
// path: an offline cmd that writes to {{out}}, or a live prop. Both blocks are
// validated whenever present, regardless of render-mode precedence, so a
// malformed offline cmd is never masked by an accompanying live block.
func (p *Project) ValidateTransition(name string) error {
	t, ok := p.Transitions[name]
	if !ok {
		return fmt.Errorf("transition %q not in config", name)
	}
	if t.RenderMode() == RenderNone {
		return fmt.Errorf("transition %q: define cmd or live.prop", name)
	}
	if t.HasOffline() && !strings.Contains(t.Cmd, "{{out}}") {
		return fmt.Errorf("transition %q: cmd must write to {{out}}", name)
	}
	if t.HasLive() {
		if _, err := p.SafePath(t.Live.Prop); err != nil {
			return fmt.Errorf("transition %q live.prop: %w", name, err)
		}
	}
	return nil
}
