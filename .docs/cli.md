# CLI

```
backstage <command> [args]
```

| Command | What it does |
|---------|--------------|
| `backstage list` | list the project's scenes and productions |
| `backstage play SCENE` | stage the scene, record it, write the `.mp4` |
| `backstage rehearse SCENE` | run the scene fast, **without** recording (dry-run) |
| `backstage produce [PRODUCTION]` | record several scenes + transitions into one video |
| `backstage setup --stage LAYOUT` | stage a layout only, no recording |
| `backstage kill` | tear down the stage and dismiss any popup |
| `backstage --version` | print the version |

`--project DIR` is a persistent flag on every command: it sets the project
directory (otherwise Backstage searches up from the current directory).

> ⚠️ `play`, `setup` and `produce` take over the physical display. Run on a clean desktop.

## list

```bash
backstage list [--project DIR]
```

Prints the scenes in `scenes/` (name, layout, step count) and any declared
productions. A handy first command to see what you can `play` or `produce`.

## play

```bash
backstage play path/to/scene.json
```

Finds the project (`backstage.json` above the scene), runs the `reset`/`setup`
hook, stages the layout, starts recording, performs every step, stops. The video
lands at `<project>/<record.out>/<scene-name>.mp4`. The stage stays open
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
| `--speed N` | scene timing multiplier (`1` = real time, smaller = faster) |
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
