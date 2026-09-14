# CLI

```
backstage <command> [args]
```

| Command | What it does |
|---------|--------------|
| `backstage list` | list recording/visual scenes, productions and presentations |
| `backstage play SCENE` | stage the scene, record it, write the `.mp4` |
| `backstage takes prune` | remove abandoned or old recorded takes |
| `backstage rehearse SCENE` | run the scene fast, **without** recording (dry-run) |
| `backstage produce [PRODUCTION]` | record several scenes + transitions into one video |
| `backstage setup --stage LAYOUT` | stage a layout only, no recording |
| `backstage kill` | tear down the stage and dismiss any popup |
| `backstage stage --help` | create and manage shared VM stages, snapshots and clones |
| `backstage render NAME` | compose existing media into an MP4 |
| `backstage preview NAME` | render a temporary preview and open playback controls |
| `backstage template init NAME` | create an editable presentation template |
| `backstage config show` | print the merged configuration and the file each key came from |
| `backstage --version` | print the version |

`--project DIR` is a persistent flag on every command: it sets the project
directory (otherwise Backstage searches up from the current directory).

`stage` commands operate on the user's shared VM registry and do not require a
project. See [Manage VM stages](how-to-manage-vm-stages.md) for their complete
workflow and host requirements. `stage snapshots` prints the name-to-image map;
`--origins` adds each origin (`null` when the snapshot was made by hand).
`stage snapshot-delete STAGE SNAPSHOT` removes a named state except `initial`.

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
lands at `<project>/<record.out>/<scene-name>.mp4`. That path is the last
successful take. A take whose steps fail, or that comes back materially shorter
than the time the recorder ran, is still kept as an attempt and printed; it
does not replace the last valid clip. The stage stays open afterwards; close
it with `backstage kill`.

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
(override with `--out`). A scene recorded at `--speed 1` without
`--show-staging` is published to `record.out` as soon as it succeeds; the
published files are the raw take, not a retimed copy. A failed take is kept
as an attempt even without `--keep-segments`. Productions and transitions
are declared in `backstage.json` (see [Configuration](configuration.md#productions)).

| Flag | Effect |
|------|--------|
| `--scenes a,b,c` | ad-hoc production from a scene-name list (instead of a declared one) |
| `--transition NAME` | transition inserted between every ad-hoc pair |
| `--show-staging` | include the stage montage in the video (default hides it) |
| `--keep-segments` | keep the intermediate clips for debugging |
| `--speed N` | shortens the delays **while** each scene is performed. The machine gets less time, so a budget that runs on the clock is not spent. To publish a take faster, give the production a `speed`. |
| `--out FILE` | output path for the final video |

## takes prune

```bash
backstage takes prune [--project DIR] [--older-than DURATION] [--max-size SIZE] [--dry-run]
```

Without flags, only abandoned in-progress directories are handled: a pending
take with no clip, or an empty clip, is deleted; a pending take with a
non-empty clip is moved to attempts, with or without facts. A `.creating-*`
directory younger than one hour is left alone; an older one with a free lease
is treated like an abandoned pending. `--older-than`
and `--max-size` also remove old unreferenced
generations and attempts. The published generation, a recording in progress,
and a generation a render is reading are never removed. Duration accepts Go
durations and a day count (`7d`). Size accepts `K`, `M`, or `G` (1024).

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

## config show

```bash
backstage config show [--project DIR]
```

Prints the workspace, the leaf project, any `extends` value, the merged
effective configuration, and the file that supplied each key. Keys filled only
by defaults are marked `[default]`. Use it to debug an inheritance chain.

See [Compose a presentation](how-to-compose-presentations.md) for the workflow,
[Presentations](presentations.md) for the JSON, and [Templates](templates.md) for
custom HTML. Rendering requires Chromium, FFmpeg and ffprobe, not a VM.
