---
name: backstage
description: >
  Use when recording, re-recording, or composing video tutorials and product
  demonstrations with Backstage: writing scene files, staging a layout of
  terminals, driving an Omarchy virtual machine as the stage, and stitching
  clips into a production. Also use when a recorded walkthrough has gone stale
  and has to be regenerated from its scene. Excludes development of the
  Backstage source code itself.
---

# Backstage Skill

Turn a real workflow into a video that can be re-recorded when the product
changes. A scene is a script; playing it produces an `.mp4`.

Use this skill for making and maintaining recordings. Not for contributing to
Backstage itself.

## Topic Guides

Read the matching guide before writing or changing anything:

- [`scenes.md`](scenes.md) - the scene file, its actions, and what each one costs
- [`vm-stage.md`](vm-stage.md) - recording inside an Omarchy guest, and every trap in it
- [`producing.md`](producing.md) - stitching clips, transitions, and framing

## The Shape Of A Project

```
backstage.json     record, popup, render, layouts, vms, hooks
scenes/*.json      one scene per file
props/             scripts a scene can call
hooks/             setup and reset, to build a known starting state
recordings/        the output
```

## The Commands

```bash
backstage list                  # the scenes and productions in this project
backstage rehearse SCENE        # fast, no camera; check the flow first
backstage play SCENE            # the take
backstage produce PRODUCTION    # several clips into one video
backstage kill                  # strike the set
```

`play`, `produce` and `setup` take over the display. Run them on a clean
desktop, or on a vm stage, where they take over the guest instead.

## Rehearse Before Playing

`rehearse` runs the same steps with the delays compressed and no recorder.
A scene that has never been rehearsed is a take that will be thrown away.

## One Scene, One Screen

A scene records one screen. Two computers are two scenes, composed afterwards.
That is not a limitation to work around: it is what keeps a take from having to
hold two recorders in step, and it lets the same footage be laid out more than
one way later without recording anything again.

## When A Recording Goes Stale

Do not re-shoot by hand. Change the scene, or change the product, and play the
scene again. A recording that cannot be regenerated is a screenshot with a
duration.
