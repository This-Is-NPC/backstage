package scene

import "path/filepath"

// Step is one ordered action in a scene.
//
// JSON keys mirror the legacy runner: action, target, value, commands, args,
// delay-before, delay-after, key-delay, hold, step.
type Step struct {
	Action      string   `json:"action"`
	Target      string   `json:"target,omitempty"`
	Value       string   `json:"value,omitempty"`
	Commands    []string `json:"commands,omitempty"`
	Args        []string `json:"args,omitempty"` // prop: extra argv for the script
	DelayBefore float64  `json:"delay-before,omitempty"`
	DelayAfter  float64  `json:"delay-after,omitempty"`
	KeyDelay    float64  `json:"key-delay,omitempty"`
	Hold        float64  `json:"hold,omitempty"`
	StepID      any      `json:"step,omitempty"` // free-form label, used only in logs
}

// Scene is a recordable script: a layout to stage plus ordered steps.
//
// Reset is a pointer so an omitted value defaults to true (ResetEnabled).
type Scene struct {
	Name   string `json:"name,omitempty"`
	Layout string `json:"layout"`
	Fresh  bool   `json:"fresh,omitempty"`
	Reset  *bool  `json:"reset,omitempty"`
	Steps  []Step `json:"steps"`
}

// LayoutName returns the layout to stage.
func (s *Scene) LayoutName() string {
	return s.Layout
}

// ResetEnabled reports whether the reset hook runs before recording. Default true.
func (s *Scene) ResetEnabled() bool {
	return s.Reset == nil || *s.Reset
}

// Project is a backstage.json: record/popup config, env, hooks, aliases and the
// named layouts scenes can stage. Loaded by LoadProject, which fills defaults.
type Project struct {
	Record  RecordCfg         `json:"record"`
	Popup   PopupCfg          `json:"popup"`
	Term    string            `json:"term,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Hooks   Hooks             `json:"hooks,omitempty"`
	Aliases map[string]Alias  `json:"aliases,omitempty"`
	Layouts map[string]Layout `json:"layouts"`

	// Render targets the final video when stitching a production (concat needs a
	// consistent size/fps across clips).
	Render RenderCfg `json:"render,omitempty"`
	// Transitions are reusable, fully user-defined render commands, keyed by name.
	Transitions map[string]Transition `json:"transitions,omitempty"`
	// Productions are named ordered sequences of scenes with transitions between.
	Productions map[string]Production `json:"productions,omitempty"`

	// Dir is the project root (directory holding the config). Set by LoadProject.
	Dir string `json:"-"`
}

// RenderCfg is the target geometry for a stitched production. Zero w/h means the
// monitor's native resolution; zero fps falls back to record.fps.
type RenderCfg struct {
	W   int `json:"w,omitempty"`
	H   int `json:"h,omitempty"`
	FPS int `json:"fps,omitempty"`
}

// Transition is a clip rendered between two scenes by a full user command. The
// command must write an mp4 to {{out}}; Backstage also substitutes {{w}} {{h}}
// {{fps}} {{from}} {{to}}. Everything else is the user's (any tool, any params).
type Transition struct {
	Cmd string `json:"cmd"`
}

// TransitionUse places a transition after a named scene in a production.
type TransitionUse struct {
	After string `json:"after"` // scene name this transition follows
	Use   string `json:"use"`   // transition name (key in Project.Transitions)
}

// Production is an ordered list of scene names plus the transitions between them.
type Production struct {
	Scenes      []string        `json:"scenes"`
	Transitions []TransitionUse `json:"transitions,omitempty"`
}

// ScenePath returns the file path for a scene referenced by name.
func (p *Project) ScenePath(name string) string {
	return filepath.Join(p.Dir, "scenes", name+".json")
}

// RecordCfg targets the recorder: which monitor, fps, and output subdir.
type RecordCfg struct {
	Monitor string `json:"monitor,omitempty"`
	FPS     int    `json:"fps,omitempty"`
	Out     string `json:"out,omitempty"`
}

// PopupCfg sizes the instruction popup and sets its typing speed.
type PopupCfg struct {
	Size []int `json:"size,omitempty"` // [w, h]
	CPS  int   `json:"cps,omitempty"`  // characters per second
}

// Hooks are user scripts (project-relative) the runner calls but never inspects.
type Hooks struct {
	Setup string `json:"setup,omitempty"`
	Reset string `json:"reset,omitempty"`
}

// Alias maps a custom step action onto a canonical action + default target,
// keeping tool-specific names out of the core (e.g. mytool-pane -> keys@editor).
type Alias struct {
	Action string `json:"action"`
	Target string `json:"target,omitempty"`
}

// Layout is a named tmux arrangement: an ordered list of panes, fullscreen or not.
type Layout struct {
	Fullscreen *bool  `json:"fullscreen,omitempty"` // default true
	Panes      []Pane `json:"panes"`
}

// FullscreenEnabled reports whether the layout takes the whole screen. Default true.
func (l *Layout) FullscreenEnabled() bool {
	return l.Fullscreen == nil || *l.Fullscreen
}

// Pane is one tmux pane: a name to target, a working dir, a command, and an
// optional split size (e.g. "38%"); the first pane ignores Size.
type Pane struct {
	Name string `json:"name"`
	Cwd  string `json:"cwd,omitempty"`
	Cmd  string `json:"cmd,omitempty"`
	Size string `json:"size,omitempty"`
}

// Manifest maps a layout pane name to its live tmux pane id (e.g. "%3").
// It lives here (not in the stage driver) so pane targeting and staging share
// one type without importing each other.
type Manifest struct {
	Panes map[string]string `json:"panes"`
	Order []string          `json:"order"` // pane names in layout order
}

// Pane resolves a target name to a tmux pane id, trying each candidate in turn
// and falling back to the first pane when none match (empty target included).
func (m *Manifest) Pane(candidates ...string) string {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if id, ok := m.Panes[c]; ok {
			return id
		}
	}
	if len(m.Order) > 0 {
		return m.Panes[m.Order[0]]
	}
	return ""
}
