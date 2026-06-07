# Concepts

Backstage borrows the language of the theatre. Five terms cover the whole tool.

## Stage

The environment your demo runs in. A **layout** in `backstage.json` describes it:
one or more panes (a working dir, a command, a split size), opened together in a
single fullscreen window. The Stage is what the camera sees.

> Example: a left pane running an app or TUI, a right pane running a shell.

## Scene

The script. A JSON file listing, in order, what happens: instruction boxes to
type, commands to run, keys to press, scripts to call. A scene names the layout
it needs and the steps to perform. One scene produces one video.

See [Writing scenes](scenes.md).

## Prompter

The on-screen narrator. A floating box that types instruction text character by
character, the way a person writes a note mid-demo. It's how the audience knows
what's happening. Driven by the `dialog` action.

## Prop

Any external script a step can call. This is the bridge beyond the terminal:
a Prop can drive a browser, run a Playwright test, click a desktop app, or do
setup work. Backstage records the whole screen, so whatever the Prop puts on it
ends up in the video. Driven by the `prop` action.

> This is what makes Backstage record *any* demo, not just terminal ones.

## Rehearsal

A dry-run. `backstage rehearse` performs the scene fast and **without
recording**, so you can check the flow, timing, and targeting before committing
to a real take. Same code path as `play`, minus the camera.

---

## How they fit together

```
backstage.json  ──►  Stage (layout)        the set
   scene.json   ──►  Scene (steps)         the script
        step    ──►  Prompter / Prop / pane action   the performance
   backstage play  ──►  records the Stage  ──►  scene.mp4
```

You direct (write the scene). Backstage sets the Stage, performs each step
(typing via the Prompter, running commands, calling Props), and records.

Next: [Writing scenes](scenes.md) · [Configuration](configuration.md).
