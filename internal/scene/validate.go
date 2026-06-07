package scene

import (
	"fmt"
	"os"
	"strings"
)

// knownActions are the canonical step actions the engine understands. Aliases
// (resolved from project config) must expand to one of these.
var knownActions = map[string]bool{
	"dialog": true,
	"run":    true,
	"type":   true,
	"keys":   true,
	"prop":   true,
	"wait":   true,
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
	if _, ok := p.Layouts[layout]; !ok {
		return fmt.Errorf("scene %q: layout %q not in config", s.Name, layout)
	}
	if len(s.Steps) == 0 {
		return fmt.Errorf("scene %q: no steps", s.Name)
	}
	for i, st := range s.Steps {
		if st.Action == "" {
			return fmt.Errorf("scene %q: step %d has no action", s.Name, i+1)
		}
		if !knownActions[st.Action] {
			if _, ok := p.Aliases[st.Action]; !ok {
				return fmt.Errorf("scene %q: step %d unknown action %q", s.Name, i+1, st.Action)
			}
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
	for _, sc := range prod.Scenes {
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

// ValidateTransition checks a transition is defined and writes to {{out}}.
func (p *Project) ValidateTransition(name string) error {
	t, ok := p.Transitions[name]
	if !ok {
		return fmt.Errorf("transition %q not in config", name)
	}
	if !strings.Contains(t.Cmd, "{{out}}") {
		return fmt.Errorf("transition %q: cmd must write to {{out}}", name)
	}
	return nil
}
