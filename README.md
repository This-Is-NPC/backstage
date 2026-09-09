# Backstage

> ### *Lights, camera... Automation!*

**Backstage turns real software workflows into reproducible video tutorials.**

Write the flow one time. Backstage opens the environment, drives the tools,
shows the narration on the screen, and records the result as an `.mp4`. When the
product changes, play the same scene again and get a new tutorial.

No manual screen recording. No forgotten steps. No stale visual documentation.

---

## Why Backstage

Most documentation explains what a feature does. It does not show how the
feature feels to use. Teams fill that gap with hand-made walkthroughs.

Those videos are useful, and they are expensive to keep:

- a screen changes, and the tutorial is wrong;
- a flag changes, and the old recording lies;
- a release needs a new walkthrough, and a person must record it again;
- one wrong keystroke costs a new take.

Backstage makes a video tutorial declarative. Describe the workflow in a scene
file. Backstage performs it and records the take.

It is documentation that you can play again.

## What it records

Backstage is tool-agnostic. It drives terminal programs, browsers, desktop
applications, local scripts, and any other process that a scene calls.

Use it for:

- **feature walkthroughs** that show the true user journey;
- **onboarding videos** that you make again when the product changes;
- **release notes** as short clips;
- **CLI and API tutorials** that stay correct;
- **end-to-end tests as tutorials**, with narration on the screen;
- **customer education** from the workflows that your team already trusts.

## The core idea

A Backstage project is a scripted production.

| Term | Meaning |
| :--- | :--- |
| **Stage** | the environment that the workflow runs in |
| **Scene** | the script: what to type, what to press, and when |
| **Prompter** | the narration box, typed like a person explains a step |
| **Prop** | a script that a scene calls |
| **Rehearsal** | a fast run that validates the flow before a take |

You direct the scene. Backstage performs it and records the take.

## How it works

1. Create a project with a `backstage.json` file.
2. Define a layout: one terminal, several panes, or a virtual machine.
3. Write a scene as ordered steps: narration, commands, keys, waits, props.
4. Rehearse the scene to check the timing and the targets.
5. Play the scene to record the tutorial.

The output is at `<project>/recordings/<scene>.mp4`.

## Quickstart

Install on Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/This-Is-NPC/backstage/master/install.sh | bash
```

Backstage is built for Hyprland and ships Linux-only builds.

Record a scene:

```bash
backstage play path/to/scene.json
```

New here? Start with
[Record your first scene](docs/how-to-record-your-first-scene.md).

## Commands

| Command | Result |
| :--- | :--- |
| `backstage list` | it lists the scenes and productions of the project |
| `backstage play SCENE` | it runs the scene and records an `.mp4` |
| `backstage rehearse SCENE` | it runs the scene fast, with no recorder |
| `backstage produce PRODUCTION` | it joins clips and transitions into one video |
| `backstage setup --stage LAYOUT` | it stages a layout and stops |
| `backstage kill` | it strikes the set |
| `backstage stage create NAME` | it creates a shared Omarchy VM ready to record |
| `backstage stage --help` | it lists VM management, snapshot and clone commands |

See [Create and manage VM stages](docs/how-to-manage-vm-stages.md) for host
requirements, snapshots, cloning and continuity between scenes.

> `play`, `produce` and `setup` take the whole display. Run them on a clean
> desktop, or on a virtual machine stage.

## Record on a virtual machine

A scene can run inside a libvirt guest that runs Omarchy. Backstage starts the
guest, installs its tools, makes the desktop ready, drives the keyboard of the
guest, and records the screen from inside it.

```json
{ "name": "01-installing", "vm": "laptop", "layout": "solo", "steps": [] }
```

See [Record inside a virtual machine](docs/how-to-record-inside-a-vm.md).

## Turn end-to-end tests into tutorials

Your tests already walk through the product. Point a prop at a headed test, add
narration, and the test run becomes a guided tutorial. When the feature
changes, update the test and record the clip again.

```json
{
  "name": "checkout-tour",
  "layout": "solo",
  "steps": [
    {"action": "dialog", "value": "Walk through checkout, from cart to confirmation."},
    {"action": "prop", "value": "props/e2e.sh", "args": ["checkout.spec.ts"]}
  ]
}
```

## Your content stays yours

Backstage stays generic. Your scenes, hooks, scripts and recordings are in your
own project directory.

## Teach your agent

```bash
mise run install:skill
```

This links the Backstage skill into `~/.agents/skills`. Run
`mise run uninstall:skill` to remove it.

## Declarative presentations

Compose existing recordings with `backstage render`: multiple screens, HTML
layouts, animated SVG scenes, captions and selected audio. A presentation changes
the montage without recording another take. `preview` renders first, then opens
playback controls. Rendering needs Chromium, FFmpeg and ffprobe.

[Follow the walkthrough](docs/how-to-compose-presentations.md) or
[run the example](examples/presentation/README.md).

## Learn more

Full documentation is in [`docs/`](docs/README.md).

- **[Record your first scene](docs/how-to-record-your-first-scene.md)**
- **[Record inside a virtual machine](docs/how-to-record-inside-a-vm.md)**
- **[Compose clips into one video](docs/how-to-compose-a-production.md)**
- **[Presentations](docs/presentations.md)** and **[Templates](docs/templates.md)**
- **[Scenes](docs/scenes.md)** and **[Configuration](docs/configuration.md)**
- **[Design](docs/design.md)** and **[What does not work yet](docs/what-does-not-work.md)**

---

<sub>Built for Hyprland. Needs Go, tmux, ghostty, gpu-screen-recorder, and ffmpeg.
A virtual machine stage also needs libvirt and an Omarchy guest.</sub>
