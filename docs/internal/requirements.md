# Requirements

What Backstage must do, the constraints it works under, and what is deliberately
out of scope. Internal doc: written for contributors, not end users.

## Vision

Turn any software demo into a flawless, reproducible video by describing it once
in a scene file. The author directs; Backstage performs (drives the tools,
narrates on screen, records) and produces the same take every time.

## Functional requirements

| # | Requirement |
|---|-------------|
| F1 | Parse a **scene** (JSON): an ordered list of steps plus the layout to stage. |
| F2 | Resolve a **project** by walking up from the scene to the nearest `backstage.json`. |
| F3 | Stage a **layout**: open the configured panes (cwd/cmd/size) in a single fullscreen window. |
| F4 | Run **hooks** before a take: `setup` for a fresh scene, otherwise `reset`. |
| F5 | Drive panes by **name**: type commands, type literal text, send named keys. |
| F6 | Show a **Prompter**: a floating box that types instruction text on screen, char by char. |
| F7 | Run **Props**: call any external script (with args, project env), blocking, surfacing a non-zero exit. |
| F8 | **Record** the screen to an `.mp4` at `<project>/<out>/<scene>.mp4`. |
| F9 | Verbs: `play` (record), `rehearse` (dry-run, no record, compressed delays), `setup` (stage only), `kill` (teardown). |
| F10 | **Aliases**: map custom step actions to a canonical action + default target, defined in project config. |

## Non-functional requirements

| # | Requirement | Rationale |
|---|-------------|-----------|
| N1 | **Deterministic** — same scene + same baseline ⇒ same video. | A doc you can regenerate, not re-shoot. |
| N2 | **Tool-agnostic core** — no tool name (`okt`, app names) in the engine. | Records anything; specifics live in `projects/`. |
| N3 | **Single binary** — one Go artifact, no runtime interpreter at play time. | Easy install (`go install`) and future desktop reuse. |
| N4 | **Drivers behind interfaces** — stage / recorder / prompter / pane. | Swap Hyprland for a virtual display later without touching the engine. |
| N5 | **User content stays local** — `projects/` is gitignored. | The tool is shared; recordings and scenes are the user's. |

## Constraints (current)

- **Hyprland only** for staging + the floating popup (omarchy windowrule mechanism).
- **gpu-screen-recorder** as the capture backend (whole monitor, hardware-encoded).
- Requires `tmux`, `ghostty`, `ffmpeg` present.
- Recording takes over the physical display; run on a clean desktop.

## Out of scope (this iteration)

- Virtual / headless display (Xvfb/cage) and scene-defined resolution.
- The Wails desktop scene editor + live preview.
- X11 / macOS / non-Hyprland portability.

These are planned; see [ARCHITECTURE](ARCHITECTURE.md) for the phased path.
