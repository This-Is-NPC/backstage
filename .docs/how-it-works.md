# How it works

A scene's journey, from JSON to `.mp4`:

```
scenes/NN.json + backstage.json (project)
      │
      ▼
  backstage ──► find backstage.json by walking up from the scene (= project root)
      │         load config (record / popup / env) and validate the scene
      │         run the reset hook (or setup, if "fresh")
      │         stage the layout: fullscreen tmux + a pane manifest
      │         start recording (gpu-screen-recorder)
      │
      ├─► perform each step by action, targeting a pane by name:
      │     dialog  → Prompter: floating box typed char-by-char
      │     run     → tmux send-keys (command + Enter) in the target pane
      │     type    → tmux send-keys (literal text) in the target pane
      │     keys    → tmux send-keys (named keys) in the target pane
      │     prop    → run an external script (any tool), blocking
      │     wait    → just pause
      │
      └─► stop recording ──► <project>/<out>/NN.mp4
```

## Packages (the generic core)

| Package | Role |
|---------|------|
| `cmd/backstage` + `internal/cli` | cobra verbs: setup / rehearse / play / kill |
| `internal/engine` | orchestrator: reads config + scene, stages, records, runs the steps |
| `internal/scene` | models (Scene / Step / Project), loader, validation |
| `internal/stage` | builds the stage (tmux + Hyprland fullscreen) |
| `internal/recorder` | start/stop the screen recorder |
| `internal/prompter` | floating box + typewriter (char-by-char) |
| `internal/pane` | target a pane by name (tmux send-keys) + keymap |

Nothing in the core names a specific tool — tool specifics live in `projects/`.

## Configuration

The project config (`backstage.json`) declares record/popup settings, an env
block, hooks, aliases, and the named layouts scenes can stage. See
[Configuration](configuration.md) for the full reference.

## Reproducible, isolated state

Hooks build a project's own starting state and freeze it; `reset` restores it
before each take. Pin the state your demo touches (a scratch dir, a throwaday
home, a throwaway DB) so the real environment is never altered. Same baseline +
same scene → the same video.

## The fullscreen stage

Backstage opens **one** terminal (`--class=backstage.layout`) running tmux, builds
the layout's panes (stable ids `%N` in a manifest, so steps can target by name),
and forces real fullscreen via Hyprland (`focuswindow` + `dispatch fullscreen 0`),
covering the bar.

## Screen recording

The recorder driver uses `gpu-screen-recorder` (omarchy's Alt+PrintScreen tool):
hardware-encoded, whole monitor, no region picker.

```
gpu-screen-recorder -w <monitor> -k auto -f <fps> -fm cfr -o <out>.mp4
# stop: SIGINT (finalizes the mp4)
```

## The floating instruction box

The prompter driver opens a floating, centered terminal using **omarchy's
windowrule mechanism** (the one TUI popups like bluetooth use), and types into it
by re-executing the backstage binary in a hidden `__type` mode (so the typewriter
needs no external interpreter):

```
windowrule = float on,  match:class ^(backstage\.popup)$
windowrule = center on, match:class ^(backstage\.popup)$
windowrule = size W H,   match:class ^(backstage\.popup)$
```

## Portability seams (Hyprland-only for now)

Two drivers are pinned to omarchy/Hyprland, behind interfaces so they can be
swapped later:

- **recorder** — `internal/recorder` (could become wf-recorder / OBS / ffmpeg).
- **floating popup** — `internal/prompter` via Hyprland windowrule (could add an X11 fallback).

A virtual/headless display driver is the next planned step; see
[Architecture](internal/ARCHITECTURE.md).
