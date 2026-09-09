# CLI

```
backstage <command> [args]
```

| Command | What it does |
|---------|--------------|
| `backstage list` | list recording/visual scenes, productions and presentations |
| `backstage play SCENE` | stage the scene, record it, write the `.mp4` |
| `backstage rehearse SCENE` | run the scene fast, **without** recording (dry-run) |
| `backstage produce [PRODUCTION]` | record several scenes + transitions into one video |
| `backstage setup --stage LAYOUT` | stage a layout only, no recording |
| `backstage kill` | tear down the stage and dismiss any popup |
| `backstage stage --help` | create and manage shared VM stages, snapshots and clones |
| `backstage render NAME` | compose existing media into an MP4 |
| `backstage preview NAME` | render a temporary preview and open playback controls |
| `backstage template init NAME` | create an editable presentation template |
| `backstage --version` | print the version |

`--project DIR` is a persistent flag on every command: it sets the project
directory (otherwise Backstage searches up from the current directory).

`stage` commands operate on the user's shared VM registry and do not require a
project. See [Manage VM stages](how-to-manage-vm-stages.md) for their complete
workflow and host requirements.

> ⚠️ `play`, `setup` and `produce` take over the physical display. Run on a clean desktop.

## list

```bash
backstage list [--project DIR]
```

Prints recording scenes with layout and step count, visual scenes with duration,
and declared productions and presentations. Use `play` for recording scenes and
`render` or `preview` for presentations.

## play

```bash
backstage play path/to/scene.json
```

Finds the project (`backstage.json` above the scene), runs the `reset`/`setup`
hook, stages the layout, starts recording, performs every step, stops. The video
lands at `<project>/<record.out>/<scene-name>.mp4`. A take that comes back
materially shorter than the time the recorder ran is reported as an error, with
both lengths; the clip is written either way. The stage stays open
afterwards; close it with `backstage kill`.

## rehearse

```bash
backstage rehearse path/to/scene.json
```

Same as `play` but skips recording and compresses delays, so you can validate
flow and targeting quickly before a real take.

## produce

```bash
backstage produce tour                          # a declared production
backstage produce --scenes 01-intro,02-deploy   # ad-hoc, no transitions
backstage produce --scenes 01-intro,02-deploy --transition slide
```

Records each scene to a clip, renders the transitions between them, and
concatenates everything into one video at `<project>/<record.out>/production.mp4`
(override with `--out`). Productions and transitions are declared in
`backstage.json` (see [Configuration](configuration.md#productions)).

| Flag | Effect |
|------|--------|
| `--scenes a,b,c` | ad-hoc production from a scene-name list (instead of a declared one) |
| `--transition NAME` | transition inserted between every ad-hoc pair |
| `--show-staging` | include the stage montage in the video (default hides it) |
| `--keep-segments` | keep the intermediate clips for debugging |
| `--speed N` | shortens the delays **while** each scene is performed. The machine gets less time, so a budget that runs on the clock is not spent. To publish a take faster, give the production a `speed`. |
| `--out FILE` | output path for the final video |

## setup

```bash
backstage setup --stage LAYOUT [--project DIR]
```

Stages a layout and stops, for debugging the layout itself. The project is found
by searching up from the current directory, or set it with `--project`.

## kill

```bash
backstage kill
```

Strikes the set: kills the tmux session and closes the stage and popup windows.

See also: [Writing scenes](scenes.md) · [Configuration](configuration.md).

## render

```bash
backstage render NAME [--check] [--out FILE] [--project DIR]
```

Composes existing media using the named project presentation. It never records
scenes, runs hooks or starts VMs. Missing input files are errors.

| Flag | Effect |
| --- | --- |
| `--check` | validate configuration, media, timing and template slots without export |
| `--out FILE` | override the project-relative MP4 output path |

Output defaults to the presentation's `out`, then `exports/NAME.mp4`. A companion
`.facts.json` records configuration, resource hashes and tool versions. Custom
HTML can still fail at a later frame after `--check` succeeds. Failed exports do
not replace an existing MP4. Ctrl-C cancels work and removes intermediates.

## preview

```bash
backstage preview NAME [--project DIR]
```

Renders a temporary MP4 first, then serves that exact video locally with play,
pause, seek and frame-step controls. The command prints the URL and opens it
with `xdg-open` when available. It waits for Ctrl-C to close the server and
remove temporary files. This is not an incremental editor.

## template init

```bash
backstage template init NAME [--project DIR]
```

Creates `templates/NAME/template.html` and a sample `visual.html`. NAME must be a
single directory name; an existing directory is refused. Register the template
in `backstage.json` and reference visual HTML from a visual scene.

See [Compose a presentation](how-to-compose-presentations.md) for the workflow,
[Presentations](presentations.md) for the JSON, and [Templates](templates.md) for
custom HTML. Rendering requires Chromium, FFmpeg and ffprobe, not a VM.
