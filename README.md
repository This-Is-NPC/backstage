# Backstage

> ### *Lights, camera... Automation!*

**Backstage turns real software workflows into reproducible video tutorials.**

Write the flow once. Backstage opens the environment, drives the tools, shows
step-by-step narration on screen, and records the result as a polished `.mp4`.
When the app changes, re-run the same scene and regenerate the tutorial.

No manual screen recording. No forgotten steps. No stale visual documentation.

---

## Why Backstage?

Most product documentation explains what a feature does, but not how it actually
feels to use it. Teams fill that gap with hand-recorded walkthroughs, onboarding
clips, release videos, and customer tutorials.

Those videos are valuable, but painful to maintain:

- a UI changes and the tutorial becomes outdated;
- a CLI flag changes and the old recording lies;
- a release needs a fresh walkthrough, but recording it again takes time;
- one typo or missed click means another take.

Backstage makes video tutorials **declarative, automated, and reproducible**.
Describe a real workflow in a scene file, let Backstage perform it like a user,
and get the same clean video every time.

It is documentation you can replay.

## What It Records

Backstage is tool-agnostic. It can drive terminal apps, TUIs, browsers, desktop
apps, local scripts, Playwright tests, setup commands, and any other process a
scene can call.

Use it for:

- **Feature walkthroughs** that show the exact user journey.
- **Onboarding videos** that can be regenerated when the product changes.
- **Release notes** as short, repeatable clips instead of one-off recordings.
- **CLI and API tutorials** that never go stale.
- **E2E tests as video tutorials** by running headed tests and narrating the flow.
- **Customer education** built from the same workflows your team already trusts.

## The Core Idea

A Backstage project is a scripted production.

| Term | What it means |
| :--- | :--- |
| **Stage** | the environment the workflow runs in: terminals, panes, windows, apps |
| **Scene** | the script: what to type, what to click, what to show, and when |
| **Prompter** | the on-screen narration box, typed out like a human is explaining the step |
| **Prop** | any external script a scene can call: Playwright, shell, Python, RPA, setup |
| **Rehearsal** | a fast dry-run to validate the flow before recording |

You direct the scene. Backstage performs it and records the take.

## How It Works

1. Create a project with a `backstage.json` config.
2. Define a layout for the Stage: one terminal, split panes, or app windows.
3. Write a Scene as ordered steps: narration, commands, key presses, waits, Props.
4. Rehearse the Scene to validate timing and targeting.
5. Play the Scene to record a video tutorial.

The output lands in `<project>/recordings/<scene>.mp4`.

Because the flow is scripted, the tutorial is not a fragile artifact. It is a
repeatable build output.

## Quickstart

Install on Linux, macOS, or WSL:

```bash
curl -fsSL https://raw.githubusercontent.com/This-Is-NPC/backstage/master/install.sh | bash
```

Install on Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/This-Is-NPC/backstage/master/install.ps1 | iex
```

Record a scene:

```bash
backstage play path/to/scene.json
```

New here? Start with [Getting started](.docs/getting-started.md).

## Commands

| Command | What it does |
| :--- | :--- |
| `backstage list` | list the project's scenes and productions |
| `backstage play SCENE` | run the scene and record it to an `.mp4` |
| `backstage rehearse SCENE` | dry-run fast, with no recording, to check the flow |
| `backstage produce PRODUCTION` | stitch several scenes and transitions into one video |
| `backstage setup --stage LAYOUT` | set the stage without recording |
| `backstage kill` | strike the set |

> `play`, `produce`, and `setup` take over your screen. Run them on a clean desktop.

## Turning E2E Tests Into Video Tutorials

Your end-to-end tests already know how to walk through the product. Backstage can
reuse that work.

Point a Prop at a headed Playwright test, add on-screen narration, and the test
run becomes a guided video tutorial. When the feature changes, update the test or
scene and regenerate the clip.

```json
{
  "name": "checkout-tour",
  "layout": "solo",
  "steps": [
    {"action": "dialog", "value": "Let's walk through checkout from cart to confirmation."},
    {"action": "prop", "value": "props/e2e.sh", "args": ["checkout.spec.ts"]}
  ]
}
```

The result is not just proof that the feature works. It is a tutorial your users,
support team, and release notes can reuse.

## Project Content Stays Yours

Backstage itself stays generic. Your scenes, hooks, scripts, demo state, and
recordings live in `projects/`, which is kept out of version control by default.

The tool is shared. The workflows you record are yours.

## Learn More

Full docs live in [`.docs/`](.docs/README.md).

- **[Getting started](.docs/getting-started.md)**: build your first tutorial from scratch.
- **[Concepts](.docs/concepts.md)**: Stage, Scene, Prompter, Prop, Rehearsal.
- **[Writing scenes](.docs/scenes.md)**: the scene file format, step by step.
- **[Configuration](.docs/configuration.md)** and **[CLI](.docs/cli.md)**: references.
- **[How it works](.docs/how-it-works.md)**: the pipeline under the hood.

---

<sub>Built for Hyprland. Needs Go, tmux, ghostty, gpu-screen-recorder, and ffmpeg.</sub>
