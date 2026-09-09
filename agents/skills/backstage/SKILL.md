---
name: backstage
description: >
  Use when recording, re-recording, or composing video tutorials and product
  demonstrations with Backstage: writing scene files, staging a layout of
  terminals, driving an Omarchy virtual machine as the stage, and stitching
  clips into a production or composing existing media with HTML/SVG templates,
  captions and audio. Also use when a recorded walkthrough has gone stale
  and has to be regenerated from its scene. Excludes development of the
  Backstage source code itself.
---

# Backstage Skill

Turn a real workflow into a video that can be re-recorded when the product
changes. Recording scenes produce takes; visual scenes and presentations arrange
existing media without recording again.

Use this skill for recording workflows and composing presentations. Not for contributing to
Backstage itself.

## Topic Guides

Read the matching guide before writing or changing anything:

- [`scenes.md`](scenes.md) - the scene file, its actions, and what each one costs
- [`vm-stage.md`](vm-stage.md) - recording inside an Omarchy guest, and every trap in it
- [`producing.md`](producing.md) - recording sequential productions and framing
- [`presentations.md`](presentations.md) - arranging existing clips, HTML/SVG, captions and audio

## The Shape Of A Project

```
backstage.json     recording settings, vms, templates, presentations
scenes/*.json      recording or visual scenes; narration and audio assets
presentations/     JSON timelines that reuse media
templates/         editable HTML/CSS
assets/            local fonts, images and audio
props/             scripts a scene can call
hooks/             setup and reset, to build a known starting state
recordings/        raw takes
exports/           rendered presentations
```

## The Commands

```bash
backstage list                  # scenes, productions and presentations
backstage rehearse SCENE        # fast, no camera; check the flow first
backstage play SCENE            # the take
backstage produce PRODUCTION    # record scenes again and join them
backstage render NAME --check   # validate existing media and template slots
backstage render NAME           # export a presentation from existing media
backstage preview NAME          # render first, then open playback controls
backstage template init NAME    # create an editable HTML template
backstage kill                  # strike the set
```

`play`, `produce` and `setup` take over the display. Run them on a clean
desktop, or on a vm stage, where they take over the guest instead.

`render` never runs recording steps, hooks or VM commands. Use it when changing
the montage of existing takes. `preview` has an initial rendering wait.

## Rehearse Before Playing

`rehearse` runs the same steps with the delays compressed and no recorder.
A scene that has never been rehearsed is a take that will be thrown away.

## One Scene, One Screen

A recording scene records one screen. Two computers are two takes, composed
afterwards. A visual scene uses HTML/SVG and has no recording steps.
That is not a limitation to work around: it is what keeps a take from having to
hold two recorders in step, and it lets the same footage be laid out more than
one way later without recording anything again.

## When A Recording Goes Stale

Do not re-shoot by hand. Change the scene, or change the product, and play the
scene again. A recording that cannot be regenerated is a screenshot with a
duration.
