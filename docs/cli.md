# CLI

```
backstage <command> [args]
```

| Command | What it does |
|---------|--------------|
| `backstage list` | list recording/visual scenes, productions and presentations |
| `backstage status [DIR]` | report which recording takes are stale, blocked or missing |
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
`stage prune-states STAGE --workspace DIR` removes produced snapshots that no
valid scene in that workspace still declares as `vm-end` on the stage.

> ⚠️ `play`, `setup` and `produce` take over the physical display. Run on a clean desktop.

## list

```bash
backstage list [--project DIR]
```

Prints recording scenes with layout and step count, visual scenes with duration,
and declared productions and presentations. Use `play` for recording scenes and
`render` or `preview` for presentations.

## status

```bash
backstage status
backstage status tutorials/pt
backstage status tutorials/pt --json
```

Walks up from DIR (or the current directory) to the workspace root, then
discovers every project whose `extends` chain reaches that root. Visual scenes
are omitted. DIR limits which scenes are printed; producers outside it are
still resolved.

The command is read-only. It does not start, stop or restore a stage, and it
does not create lock files. It reads the published clip and facts through the
shared take reader (generation or legacy pair) and never reads attempts.

Each recording scene is one row: project-relative name, status, short detail.
The first matching status wins: `error`, `blocked:no-producer`,
`blocked:state-missing`, `blocked:rehearsal-state`, `missing`, `stale:inputs`,
`stale:state-mismatch`, `stale:start-state`, `stale:upstream`, `unverifiable`,
`ok`. `error` is a take that cannot be opened, a scene file that does not
load or validate, or a project whose `backstage.json` parses and whose
`extends` chain reaches this workspace but fails the rest of validation (for
example `record.out` escaping the leaf). DIR limits the scene rows, not the
errors: configuration and scene errors from the whole workspace are always
listed in an Errors section (and in `--json` `errors`), identified by
`kind` (`config-error` or `scene-error`) and `project`. Config-error rows
omit `scene`. The command exits non-zero whenever that list is non-empty.
JSON that does not parse, or a chain that does not reach this root, is only a
`warning` and does not change the exit code. A scene in `error` never
aborts the report through the graph (no cycle, no duplicate-producer
conflict). Of its `vm-start` / `vm-end`, only fields that would validate on
their own enter the graph, and it is the producer of a snapshot only when no
valid scene declares that snapshot on the same stage; otherwise the error row
notes that it also declares that `vm-end`. A consumer whose start snapshot
origin points at an error row or an error project is
`stale:upstream`. A snapshot that exists with no
origin, including `initial`, is a valid manual root: consumers of that state
are not `stale:upstream`. `stale:inputs` compares the take's own start-state
digest (`clean:<facts.start-image>`, `continue:<after>`). `stale:start-state`
is a clean take whose `facts.start-image` is not the current snapshot image,
so re-recording a producer leaves each consumer `stale:start-state`.

`--json` adds the project, every matching reason, generation clip and facts
paths, the stable clip and facts paths, stage, start and end snapshots, the
`manual` label of each known state, `warnings`, and `errors`.

Two valid scenes that save the same snapshot on one stage, or a cycle among
valid scenes (including a scene that both starts from and saves the same
snapshot), exit non-zero. Scene and config errors exit non-zero through the
Errors section. Conflicts are written once to the command output.

## play

```bash
backstage play path/to/scene.json
backstage play path/to/scene.json --adopt
backstage play path/to/scene.json --with-deps
backstage play path/to/scene.json --with-deps --jobs 1
backstage play --stale
backstage play --stale tutorials/pt --json
```

Finds the project (`backstage.json` above the scene), runs the `reset`/`setup`
hook, stages the layout, starts recording, performs every step, stops. The video
lands at `<project>/<record.out>/<scene-name>.mp4`. That path is the last
successful take. A take whose steps fail, that comes back materially shorter
than the time the recorder ran, or whose `vm-end` capture fails, is still kept
as an attempt and printed; it does not replace the last valid clip. The stage
stays open afterwards; close it with `backstage kill`.

`--adopt` replaces a snapshot that has no origin. Type the snapshot name to
confirm, the same way `stage delete` asks for the stage name. Produce does
not take this flag.

`--with-deps` prints a plan, then records stale or missing producers (and any
producer beneath one that will run) before the scene. It needs a scene under
`<project>/scenes`. It reserves every stage in that plan first and hands each
lock to a child process for that stage. Takes on different stages run together
when the host budget allows. A take that uses the host display never overlaps
another host take or any VM take. `--jobs N` caps how many takes run at once;
`--jobs 1` is the old serial order. `--json` writes one `job.progress` line
per status change, a `plan.warning` line for each plan warning, and a final
JSON report that repeats those warnings. `--jobs` and `--json` need
`--with-deps` or `--stale`. Each job logs to
`${XDG_STATE_HOME:-~/.local/state}/backstage/jobs/<run>/<job>.log`; only the
20 newest runs are kept. A consumer of a manual snapshot is fine; a producer
that would replace one, or replace a snapshot that belongs to another scene,
stops the plan before the lock. The scene that remakes a snapshot is its
declared `vm-end` producer; the origin is only used to detect errors and
staleness. `--adopt` on this command applies only to the scene you named, and
the typed confirmation happens before the lock. Ctrl-C at that prompt exits
130 without taking a lock or starting a take. A scene or project error on the
chain stops the command before any lock; an error off the chain is a warning.
The first failure starts no new job; running jobs finish and dependents stay
`not-run`. Ctrl-C sends SIGINT to every child, waits, prints that same report
once (the take in progress is `interrupted`) and exits 130. A cancel between
takes lists the next step as `interrupted before` and as not run. A busy
`image-catalog` makes a clean restore wait; it does not fail the take.

`--stale [DIR]` records every scene in the workspace that `--with-deps`
would seed (`missing`, `stale:*`, and a recording whose state is still a
rehearsal), plus the same upstream and continue closure and the consumers
of every planned take (including scenes that are still `ok` and would go
stale after the run). The same refusals apply to that final set before
any lock. `--adopt` applies to each seeded scene, not to a scene that
entered only as a downstream consumer; type every snapshot name when more
than one must be replaced.

## rehearse

```bash
backstage rehearse path/to/scene.json
backstage rehearse path/to/scene.json --replace-state
backstage rehearse path/to/scene.json --with-deps
backstage rehearse --stale
```

Same as `play` but skips recording and compresses delays, so you can validate
flow and targeting quickly before a real take. `--replace-state` lets a
rehearsal overwrite a snapshot a recording made. Without it, that replacement
is refused before the take starts. Produce does not take this flag.

`--with-deps` and `--stale` use the same plan, locks and scheduler as
`play`. It never implies `--replace-state`: the plan stops before a take
that would replace a recording snapshot unless you pass the flag. A
`continue` scene waits for its predecessor even when that predecessor is
on another stage.

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
published files are the raw take, not a retimed copy. A failed take
(`steps-failed`, `short`, or `capture-failed`) is kept as an attempt even
without `--keep-segments`. Productions and transitions
are declared in `backstage.json` (see [Configuration](configuration.md#productions)).

| Flag | Effect |
|------|--------|
| `--scenes a,b,c` | ad-hoc production from a scene-name list (instead of a declared one) |
| `--transition NAME` | transition inserted between every ad-hoc pair |
| `--show-staging` | include the stage montage in the video (default hides it) |
| `--keep-segments` | keep the intermediate clips for debugging |
| `--speed N` | shortens the delays **while** each scene is performed. The machine gets less time, so a budget that runs on the clock is not spent. To publish a take faster, give the production a `speed`. |
| `--out FILE` | output path for the final video |

## stage prune-states

```bash
backstage stage prune-states STAGE --workspace DIR [--dry-run] [--json] [--include-missing-projects]
```

Discovers projects the same way as `status`. A configuration or scene error
anywhere in the workspace, or an unreadable `backstage.json` under it
(a discover warning), stops the command before any lock: that file may
still name the state. `--dry-run` then prints those errors and warnings
and exits, with no plan. A real run evaluates again after it holds the
stage lock; a new error, warning or conflict there
releases the lock and removes nothing. It waits for `image-catalog`
only around each snapshot deletion.

A snapshot is removed only when it has a readable origin inside the workspace
and no valid scene still declares that name as `vm-end` on this stage (the
scene was deleted, renamed, or changed the snapshot). A relative
`origin.project`, or one that is not already clean (`..`, `.`, `//`), is
unreadable. The engine writes a cleaned, resolved path. An origin whose
project directory is gone but still belongs to this workspace is kept as
`missing-project` unless `--include-missing-projects`. `--dry-run` prints
`would remove` instead of `removed`.

Reasons: `undeclared`, `initial`, `manual`, `outside-workspace` (including a
hidden directory or a nested workspace), `missing-project`, `consumed`
(`vm-start` `clean`), `in-use`. Each removal is `snapshot-delete`: stage
lock plus `image-catalog` around that delete, one commit, then `Collect`. A collection warning
leaves the mapping gone. A failure mid-run prints `failed SNAPSHOT: ERROR`
and `not attempted SNAPSHOT` (JSON: `failed` and `not-attempted`). Every
error, including a missing argument or an unknown flag, is printed once to
stderr. Workspace errors, warnings and conflicts are not repeated there.
After flags parse, `--json` writes exactly one JSON document to stdout: the
report (success or mid-run failure, with pending cleanup in
`cleanup-warnings`), a workspace abort (`Result`), a `ConflictError`, or
`{"stage": STAGE, "error": "..."}`.

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
