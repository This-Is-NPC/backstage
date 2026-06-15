# Writing scenes

A scene is a JSON file in a project's `scenes/`. It names the layout to stage and
lists the steps to perform. One scene produces one video.

For the project config the scene runs under, see [Configuration](configuration.md).

## The scene file

```json
{
  "name": "demo",
  "layout": "solo",
  "reset": true,
  "steps": [
    {"action": "dialog", "value": "Let's make our first commit."},
    {"action": "run",  "target": "term", "value": "git status", "delay-after": 2},
    {"action": "keys", "target": "term", "commands": ["ctrl+l"]},
    {"action": "prop", "value": "props/open-browser.sh", "args": ["http://localhost:3000"]},
    {"action": "wait", "delay-after": 2}
  ]
}
```

| Field | Meaning |
|-------|---------|
| `name` | name of the output file (`<out>/<name>.mp4`); defaults to the filename |
| `layout` | which layout from `backstage.json` to stage |
| `fresh` | `true` runs the `setup` hook (build state from scratch) |
| `reset` | `true` (default) runs the `reset` hook before recording |
| `steps` | the ordered list of actions |

## Actions

| `action` | uses | does |
|----------|------|------|
| `dialog` | `value` | open the floating Prompter box, type `value`, hold, close |
| `run` | `target`, `value` | type `value` + Enter in the target pane |
| `type` | `target`, `value` | type `value` literally (no Enter) in the target pane |
| `keys` | `target`, `commands` | send named keys / literals to the target pane |
| `prop` | `value`, `args` | run an external script (any tool: browser, RPA, setup) |
| `transition` | `value`, `args` | run a configured live transition as an in-scene overlay |
| `wait` | — | just pause |

Optional on any step: `delay-before`, `delay-after` (seconds), `key-delay`
(between keys), `hold` (how long a dialog stays before closing).

`target` is a pane `name` from the layout. With no target, the step falls back to
the first pane.

### prop: call any script

```json
{"action": "prop", "value": "props/click.py", "args": ["--btn", "ok"]}
```

The path must be relative to the project root; absolute paths and project escapes
are rejected. The script runs with the project `env` and the project root as its
working dir,
blocks until it exits, and a non-zero exit is reported. This is how a scene
reaches beyond the terminal: drive a browser, run an e2e suite, automate a
desktop app. Whatever it puts on screen is recorded.

### transition: reuse a live transition inside a scene

```json
{"action": "transition", "value": "chapter-browser", "args": ["--subtitle", "Anything visible can be recorded"]}
```

`value` names a transition from `backstage.json`. The transition must define
`live.prop`; offline-only `cmd` transitions are valid for productions, but cannot
run inside a scene. Backstage runs the live prop from the project root, appends
the step `args` after the transition's configured `live.args`, substitutes the
in-scene placeholders `{{w}}`/`{{h}}`/`{{fps}}`, blocks until the prop exits, and
records whatever it showed on screen as part of the current scene. Use this for
HTML/CSS chapter cards, animated overlays, or other visuals that need more
control than the short built-in Prompter.

`{{from}}`, `{{to}}`, and `{{out}}` are **not** available in-scene. `{{from}}`/
`{{to}}` are production-only: they name the surrounding scenes of a transition
segment, and an in-scene step has no neighbouring scenes, so they would only ever
substitute to empty here. `{{out}}` is omitted because the recorder owns the clip
file. (As in a production segment, `{{fps}}` falls back to `record.fps`, and when
`render.w`/`render.h` are unset `{{w}}`/`{{h}}` substitute to `0`, meaning "monitor
native" — the prop must treat `0` as native.)

### Aliases

A scene can use friendly action names that the project config maps to a canonical
action + default target. Define them under `aliases` in `backstage.json` (see
[Configuration](configuration.md#aliases)).

## Keys (in `keys`)

| You write | Becomes |
|-----------|---------|
| `"right arrow"` / `"left arrow"` / `"up"` / `"down"` | arrows |
| `"esc"` `"enter"` `"tab"` `"space"` `"backspace"` | named keys |
| `"pgup"` / `"pgdn"` `"home"` / `"end"` `"delete"` `"f1".."f6"` | navigation |
| `"ctrl+p"` `"alt+x"` | chords |
| `"m"`, `"1"`, any other string | typed literally |

## Tips

- Keep dialog boxes short. Long text drags and tires.
- Use `delay-after` to let a TUI repaint or a notification appear.
- For deterministic takes, drive a `bash` pane with `run` rather than relying on a
  live agent.
- Validate with `backstage rehearse SCENE` before a real `play`.

Next: [How it works](how-it-works.md).
