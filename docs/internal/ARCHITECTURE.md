# Backstage — Architecture (Go + Wails)

> *Lights, camera... Automation!*

Architecture for **Backstage** (Go), with a desktop app (Wails) and an emulated
display (virtual display) ahead. The original Python runner has been ported to Go
and removed (phases 0–1 are done; see the phase table).

## Principles

1. **Core as a Go library.** The CLI and the desktop app are thin clients of the same core.
2. **Tool-agnostic.** The core only knows generic actions. Tool names
   (git, lazygit, a web app...) live in `projects/`.
3. **OS-agnostic via drivers.** Stage, recorder and prompter are interfaces.
   Today's Hyprland = one driver; a virtual display = another.
4. **One schema.** Go structs generate TS types → editor and core share a single
   source of truth (no duplicated schema).
5. **Deterministic.** Same baseline + same scene → same video.

## Vocabulary (Backstage brand concept)

| Term | Technical | In the core |
|------|-----------|-------------|
| **Stage** | display + windows + layout | `stage/` (driver) |
| **Scene** | JSON script (steps) | `scene/` |
| **Prop** | external script called by a step (RPA included) | `prop` action |
| **Rehearsal** | dry-run: no recording, compressed delays | `rehearse` verb |
| **Prompter** | auto-type of the instruction box | `prompter/` (driver) |

## Module layout

```
backstage/
├── go.mod
├── cmd/backstage/main.go         # CLI (cobra): setup | rehearse | play | kill
├── internal/
│   ├── scene/
│   │   ├── types.go              # Scene, Step, Project (JSON tags) + Manifest
│   │   ├── loader.go            # config find-up + defaults + env expansion
│   │   └── validate.go
│   ├── engine/
│   │   ├── engine.go            # Run(scene, opts): hooks→stage→record→steps→stop
│   │   ├── actions.go           # dispatch dialog/run/type/keys/prop/wait
│   │   └── timing.go            # delays, type duration
│   ├── stage/                   # stage drivers (display + windows)
│   │   ├── stage.go             # Stager interface
│   │   ├── hypr.go              # current: Hyprland live + tmux fullscreen
│   │   └── virtual.go           # future: Xvfb/cage headless @ WxH
│   ├── recorder/                # recording drivers
│   │   ├── recorder.go          # Recorder interface
│   │   ├── gpu.go               # gpu-screen-recorder (current)
│   │   └── ffmpeg.go            # ffmpeg x11grab (virtual display)
│   ├── prompter/                # auto-type of the box
│   │   ├── prompter.go          # Prompter interface
│   │   ├── hypr_popup.go        # current: windowrule float + typewriter
│   │   └── overlay.go           # future: overlay on the virtual display
│   ├── pane/
│   │   ├── tmux.go              # tmux send-keys backend
│   │   └── keymap.go            # keymap, ToToken
│   └── cli/                     # cobra verbs
└── app/                         # Wails desktop (FUTURE)
    ├── wails.json
    ├── main.go                  # Wails bootstrap, binds the Go core
    └── frontend/                # React/TS editor (types generated from the Go schema)
```

The CLI and the app import `internal/engine`. Neither reimplements logic.

## Interfaces (the key: pluggable drivers)

```go
// Stager prepares the stage: display + windows where the scene runs.
type Stager interface {
    Setup(layout scene.Layout, p *scene.Project) (*scene.Manifest, error)
    Teardown() error
}

// Recorder captures the stage to video.
type Recorder interface {
    Start(outPath string) error
    Stop() (path string, err error)
}

// Prompter types the floating instruction box (the "Prompter").
type Prompter interface {
    Show(text string, opts Opts) error
    Close() error
}

// Driver targets a window/pane by name and sends input.
type Driver interface {
    Run(target, cmd string) error            // command + Enter
    Type(target, text string) error          // literal text
    Keys(target string, commands []string, keyDelay time.Duration) error
}
```

The `engine` depends on the interfaces, never the implementation. Scene/project
config picks the driver. Today: `hypr` + `gpu` + `tmux`. Future: `virtual` +
`ffmpeg` + `overlay`.

## Schema (Go structs → generated TS types)

```go
type Scene struct {
    Name   string `json:"name"`
    Layout string `json:"layout"`
    Fresh  bool   `json:"fresh"`
    Reset  *bool  `json:"reset"`  // default true
    Steps  []Step `json:"steps"`
}

type Step struct {
    Action      string   `json:"action"`            // dialog|run|type|keys|prop|wait
    Target      string   `json:"target,omitempty"`
    Value       string   `json:"value,omitempty"`
    Commands    []string `json:"commands,omitempty"`
    Args        []string `json:"args,omitempty"`     // prop
    DelayBefore float64  `json:"delay-before,omitempty"`
    DelayAfter  float64  `json:"delay-after,omitempty"`
    KeyDelay    float64  `json:"key-delay,omitempty"`
    Hold        float64  `json:"hold,omitempty"`
}

type Project struct {
    Record  RecordCfg         `json:"record"`
    Display DisplayCfg        `json:"display"`   // FUTURE: w,h,dpi for the virtual display
    Popup   PopupCfg          `json:"popup"`
    Env     map[string]string `json:"env"`
    Hooks   Hooks             `json:"hooks"`
    Aliases map[string]Alias  `json:"aliases"`
    Layouts map[string]Layout `json:"layouts"`
}
```

`tygo` (or similar) generates `.ts` from these structs → the editor consumes the
same schema. This removes the "duplicated schema" cost of Go+Wails.

## The `prop` action (Prop = external script)

```json
{"action": "prop", "value": "props/click-button.py", "args": ["--btn", "ok"]}
```

```go
func (e *Engine) actProp(st scene.Step) error {
    if st.Value == "" { return nil }
    path := st.Value
    if !filepath.IsAbs(path) { path = filepath.Join(e.Project.Dir, st.Value) }
    cmd := exec.Command(path, st.Args...)
    cmd.Dir = e.Project.Dir
    cmd.Env = e.env()                        // inherits the project env
    if err := cmd.Run(); err != nil {        // blocking; surfaces a non-zero exit
        return fmt.Errorf("prop %s: %w", st.Value, err)
    }
    return nil
}
```

Generic by design: it runs any executable. RPA (PyAutoGUI) is just one use case;
so is a browser, a Playwright run, or a desktop automation.

## CLI (cobra)

```
backstage setup    --stage <layout>      # stage only, no recording (debug layout)
backstage rehearse <scene.json>          # run all steps, NO recording, compressed delays
backstage play     <scene.json>          # run + record → mp4
backstage kill                           # tear the stage down
```

`rehearse` = `engine.Run(opts{Record:false, Speed:fast})`. Same loop, skips the recorder.

## Emulated display (virtual display — the "future")

Today recording depends on the physical monitor (Hyprland + the configured
monitor). Target: a headless display at the scene's resolution.

```
project.display: { "w":1920, "h":1080, "dpi":96 }
      │
  stage/virtual.go starts a headless compositor:
      X11:     Xvfb :99 -screen 0 1920x1080x24
      Wayland: cage / sway --headless / wlroots headless
      │   (exports DISPLAY/WAYLAND_DISPLAY to all children)
  recorder/ffmpeg.go records:
      ffmpeg -f x11grab -video_size 1920x1080 -i :99 ... out.mp4
      │
  preview in the app: x11vnc/wayvnc on the virtual display → VNC widget in the Wails frontend
```

Wins: resolution comes from the scene, not the machine. Runs in CI. RPA targets
the virtual display. Selected by `project.display` or a `--driver virtual` flag.

## Migration phases

| Phase | Delivery | Result |
|-------|----------|--------|
| **0 — scaffold + parity** ✅ | Go module (mise), core ported, hypr+gpu+tmux drivers | done — live parity verified, Python runner removed; Go is the sole implementation |
| **1 — tool-agnostic** ✅ | setup/rehearse/play/kill verbs; aliases→config; no hardcoded targets; `prop` action; `make check` gate | done — no tool name in the core |
| **2 — virtual display** | driver interfaces formalized; `virtual.go` + `ffmpeg.go`; `display` config | headless recording at the scene's resolution |
| **3 — schema → TS** | `tygo` generates types from the Go schema | single source of truth |
| **4 — Wails app** | scene/step editor; runs the core; live VNC preview | desktop app |

## New prerequisites (beyond the current ones)

- **Go** (via [mise](https://mise.jdx.dev), pinned in `.mise.toml`) — build.
- **Wails** v2 + the OS webview (phase 4).
- **Xvfb**/`cage` + `ffmpeg`/`x11vnc` (phase 2, virtual display).
- Props may use any interpreter (e.g. `python3`, `node`) — per project, optional.
