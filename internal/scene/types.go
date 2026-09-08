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
	// VM names an entry in the project's `vms` and moves the whole take onto
	// that computer: the steps are typed on its own keyboard and its screen is
	// recorded from inside it. Empty is the ordinary stage, a tmux layout in a
	// window on this machine.
	//
	// One scene, one computer. Two computers are two scenes, composed
	// afterwards -- which is what keeps a take from having to keep two
	// recorders in step, and what lets the same footage be laid out more than
	// one way later.
	VM    string `json:"vm,omitempty"`
	Fresh bool   `json:"fresh,omitempty"`
	Reset *bool  `json:"reset,omitempty"`
	Steps []Step `json:"steps"`
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
	// VMs are the Omarchy guests a scene can be staged on, by name.
	VMs map[string]VMCfg `json:"vms,omitempty"`

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

// VMCfg is one Omarchy guest a scene can run on.
//
// **Omarchy is a requirement and not a default.** The stage installs its tools
// out of Omarchy's repository, records through its Hyprland's wlr-screencopy --
// which is the only way a guest with no render node can capture its own screen
// -- puts a keyboard on its single seat, and drives its shell's own verbs for
// idle, notifications and restart. A guest that is not Omarchy is refused when
// the stage opens rather than failing later as something else.
type VMCfg struct {
	// Domain is the libvirt domain name.
	Domain string `json:"domain"`
	// User is the account whose session is filmed.
	User string `json:"user"`
	// Admin is the account ssh connects as; it needs the key and sudo. Empty
	// means the filmed account is also the administrator.
	//
	// Two fields because they are two people. A household is worth filming
	// precisely because whoever is at the keyboard has no privilege, and
	// handing the filmed account a key and passwordless sudo so a recorder
	// could reach it would be filming a machine nobody described.
	Admin string `json:"admin,omitempty"`
	// Key is the ssh private key, `~` expanded. Empty uses ssh's own default.
	Key string `json:"key,omitempty"`
	// URI is the libvirt connection; empty means qemu:///system.
	URI string `json:"uri,omitempty"`
	// Open is what the stage leaves on the desktop for the scene to drive.
	// Empty opens a terminal, which is what most scenes type into.
	//
	// A showcase of a program with a window should be showing that window, so
	// this names it: `omahouse-studio` puts the product's own face on screen
	// and keeps the terminal for the steps that genuinely have no other way.
	Open string `json:"open,omitempty"`
	// Language is the locale the opened program runs under, like
	// `C.UTF-8` or `en_US.UTF-8`. A film whose captions are in one
	// language and whose package manager is in another reads as two
	// recordings spliced together.
	Language string `json:"language,omitempty"`
}

// RenderCfg is the target geometry for a stitched production. Zero w/h means the
// monitor's native resolution; zero fps falls back to record.fps.
type RenderCfg struct {
	W   int `json:"w,omitempty"`
	H   int `json:"h,omitempty"`
	FPS int `json:"fps,omitempty"`
}

// ResolveRenderDims returns the (fps, w, h) a transition should target, applying
// the same config fallbacks the production pipeline uses so an in-scene live
// transition and a production segment can't drift. fps falls back to record.fps
// when render.fps is unset. w/h are 0 when render.w/h are unset: that means
// "monitor native", whose true pixel size is only knowable by probing a recorded
// clip — which the production pipeline does, but an in-scene step cannot without
// itself recording. Callers that need concrete pixels (production) probe; the
// in-scene path passes 0 through to the prop, which should treat 0 as native.
func (p *Project) ResolveRenderDims() (fps, w, h int) {
	fps = p.Render.FPS
	if fps == 0 {
		fps = p.Record.FPS
	}
	return fps, p.Render.W, p.Render.H
}

// Transition is a reusable production visual. It can be rendered offline by a
// command that writes an mp4 to {{out}}, or recorded live by running a prop while
// the recorder captures the screen.
type Transition struct {
	Cmd  string         `json:"cmd,omitempty"`
	Live LiveTransition `json:"live,omitempty"`
	// Live transitions run a blocking project-relative prop. The prop owns its
	// visual lifecycle: open the overlay/window, wait for animation, close, exit.
}

// HasLive reports whether this transition should be recorded from the screen.
func (t Transition) HasLive() bool { return t.Live.Prop != "" }

// HasOffline reports whether this transition can render an mp4 without staging.
func (t Transition) HasOffline() bool { return t.Cmd != "" }

// RenderMode is how a transition produces its clip. It is the single source of
// truth for the precedence shared by production, in-scene steps, and validation.
type RenderMode int

const (
	// RenderNone means the transition defines neither a live prop nor an offline cmd.
	RenderNone RenderMode = iota
	// RenderLive records the screen while a live prop drives the overlay.
	RenderLive
	// RenderOffline runs a command that writes the clip to {{out}}.
	RenderOffline
)

// RenderMode reports how this transition should be rendered. A live prop takes
// precedence over an offline cmd when both are present, so production and
// in-scene steps agree on which path runs.
func (t Transition) RenderMode() RenderMode {
	switch {
	case t.HasLive():
		return RenderLive
	case t.HasOffline():
		return RenderOffline
	default:
		return RenderNone
	}
}

// LiveTransition configures a transition-as-prop, either as a production segment
// or as an in-scene overlay step.
type LiveTransition struct {
	Prop string   `json:"prop,omitempty"`
	Args []string `json:"args,omitempty"`
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

// PopupCfg sizes the instruction popup, sets its typing speed, and optionally
// styles the current Hyprland terminal Prompter.
type PopupCfg struct {
	Size  []int         `json:"size,omitempty"` // [w, h]
	CPS   int           `json:"cps,omitempty"`  // characters per second
	Style PopupStyleCfg `json:"style,omitempty"`
}

// PopupStyleCfg keeps the built-in Prompter intentionally small. Complex HTML,
// animation, and multi-box overlays belong to live transitions.
type PopupStyleCfg struct {
	FontSize int    `json:"fontSize,omitempty"`
	Title    string `json:"title,omitempty"`
	Header   string `json:"header,omitempty"`
	Chrome   string `json:"chrome,omitempty"` // default|minimal|none
	Class    string `json:"class,omitempty"`
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
