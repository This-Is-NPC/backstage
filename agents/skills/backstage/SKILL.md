---
name: backstage
description: >
  Use when creating, converting, recording, or composing video tutorials and
  product demonstrations with Backstage. Covers native declarative JSON scenes
  and animations, existing-media timelines, recording terminal or Omarchy VM
  workflows, and regenerating stale walkthroughs. Excludes development of the
  Backstage source code itself.
---

# Backstage Skill

Turn a real workflow into a video that can be re-recorded when the product
changes. Recording scenes produce takes. Native JSON visual scenes generate
graphics and interface demonstrations; presentations combine them with optional
recordings, captions and audio without recording again.

Use this skill for recording workflows and composing presentations. Not for contributing to
Backstage itself.

## Topic Guides

Read the matching guide before writing or changing anything:

- [`scenes.md`](scenes.md) - the scene file, its actions, and what each one costs
- [`vm-stage.md`](vm-stage.md) - recording inside an Omarchy guest, and every trap in it
- [`producing.md`](producing.md) - recording sequential productions and framing
- [`declarative.md`](declarative.md) - creating native JSON visuals or converting an existing video, including its elements and animations
- [`presentations.md`](presentations.md) - timelines, cards, cameras, recorded actions, templates, captions and audio

## Choose The Authoring Mode

Use recording scenes when the user wants an actual workflow performed on screen.
Use native `composition` scenes for declarative graphics and interface states;
use built-in cards for standard titles and calls to action. Custom HTML entries
remain available when the task calls for a programmable template.

When the user requests a fully declarative conversion, move the elements,
content, visibility and motion into JSON and local static assets. Reusing the
original React composition through a frame-clock adapter does not satisfy that
request. The optional `export-remotion` bridge produces a React project; it is
not an importer or a declarative conversion. Read `declarative.md` for that workflow.

## The Shape Of A Project

```
backstage.json     recording settings, vms, templates, presentations; may extend an ancestor
scenes/*.json      recording or visual scenes; narration and audio assets
presentations/     JSON timelines with optional media
templates/         optional custom HTML/CSS entries
assets/            local stylesheets, fonts, images and audio
props/             scripts a scene can call
hooks/             setup and reset, to build a known starting state
recordings/        raw takes
exports/           rendered presentations
```

## The Commands

```bash
backstage list                  # scenes, productions and presentations
backstage status [DIR]          # which takes are stale, blocked or missing
backstage rehearse SCENE        # fast, no camera; check the flow first
backstage play SCENE            # the take
backstage produce PRODUCTION    # record scenes again and join them
backstage render NAME --check   # validate scenes, resources, timing and slots
backstage render NAME           # export visual scenes and optional media
backstage lint NAME             # check editorial typography and reading time
backstage export-remotion NAME  # export media and resolved timing for React components
backstage preview NAME          # render first, then open playback controls
backstage template init NAME    # create an editable HTML template
backstage config show           # merged configuration and the file each key came from
backstage takes prune           # drop abandoned or old takes; not the last success
backstage kill                  # strike the set
```

`play`, `produce` and `setup` take over the display. Run them on a clean
desktop, or on a vm stage, where they take over the guest instead.

`render` never runs recording steps, hooks or VM commands. Use it for native
visuals and the montage of existing takes. `preview` has an initial rendering wait.

## Rehearse Before Playing

`rehearse` runs the same steps with the delays compressed and no recorder.
A scene that has never been rehearsed is a take that will be thrown away.

## One Scene, One Screen

A recording scene records one screen. Two computers are two takes, composed
afterwards. A visual scene uses declarative JSON elements or HTML/SVG and has no
recording steps.
That is not a limitation to work around: it is what keeps a take from having to
hold two recorders in step, and it lets the same footage be laid out more than
one way later without recording anything again.

## When A Recording Goes Stale

`backstage status` (and `status --json`) reports which recording takes are
missing, stale, blocked or in error, and why. The directory argument limits
scene rows, not errors: scene and config errors from the whole workspace stay
in the Errors section (`kind` `scene-error` or `config-error`) and force a
non-zero exit. A scene in error does not abort the report through the graph.
Graph conflicts among valid scenes still exit non-zero. JSON that does not parse,
or a chain that does not reach this root, is only a warning. Do not re-shoot
by hand. Change the scene, or change the product, and play the scene again.
A recording that cannot be regenerated is a screenshot with a duration.
