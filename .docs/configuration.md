# Configuration — `backstage.json`

A project is a folder containing `backstage.json` plus `scenes/` and (optionally)
`hooks/`. Backstage finds the config by walking up from a scene to the nearest
`backstage.json`; that folder is the **project root**, and `${PROJECT}` /
`$PROJECT` in the config expand to it.

```json
{
  "record":  { "monitor": "eDP-1", "fps": 30, "out": "recordings" },
  "popup":   { "size": [1200, 560], "cps": 32 },
  "term":    "ghostty",
  "env":     { "APP_HOME": "${PROJECT}/.state" },
  "hooks":   { "setup": "hooks/setup.sh", "reset": "hooks/reset.sh" },
  "aliases": { "app": { "action": "keys", "target": "app" } },
  "layouts": {
    "solo": { "fullscreen": true, "panes": [
      { "name": "term", "cwd": "work", "cmd": "bash" }
    ] }
  }
}
```

## Keys

| Key | What it does | Default |
|-----|--------------|---------|
| `record.monitor` | monitor to capture | `eDP-1` |
| `record.fps` | frames per second | `30` |
| `record.out` | output dir, relative to the project | `recordings` |
| `popup.size` | `[width, height]` of the instruction box | `[1200, 560]` |
| `popup.cps` | typing speed of the box (chars/sec) | `32` |
| `term` | terminal command used for the stage and popup | `ghostty` |
| `env` | map exported to panes, hooks, recorder, popup | — |
| `hooks.setup` | script run when a scene is `"fresh"` | — |
| `hooks.reset` | script run before every other take | — |
| `aliases` | custom action names → `{action, target}` | — |
| `layouts` | named stage layouts (see below) | — |

The video is written to `<project>/<record.out>/<scene-name>.mp4`.

## Trust boundary

`backstage.json` and `scenes/*.json` are executable project configuration: pane
commands, hooks, props, and transitions run local processes as the current user.
Only run projects you trust. To keep shared configs from escaping the project by
accident, project-relative paths reject absolute paths, `..` escapes, and known
symlink escapes. Scene names are limited to letters, numbers, `.`, `_`, and `-`,
and `env` keys must be valid shell identifiers.

## Layouts

A layout is a named set of panes opened in one fullscreen window.

```json
"board": { "fullscreen": true, "panes": [
  { "name": "app",   "cwd": "work", "cmd": "lazygit" },
  { "name": "shell", "cwd": "work", "cmd": "bash", "size": "38%" }
] }
```

| Field | Meaning |
|-------|---------|
| `fullscreen` | take the whole screen (default `true`) |
| `panes[].name` | the name steps target (`"target": "app"`) |
| `panes[].cwd` | working dir, relative to the project (default `.`) |
| `panes[].cmd` | command the pane runs (default `bash`) |
| `panes[].size` | split width when not the first pane, e.g. `"38%"` |

The first pane fills the window; each next pane splits to its right. A step with
no (or an unknown) `target` falls back to the first pane.

## Aliases

Aliases keep tool-specific names out of the core: a scene can use a friendly
action name that the config maps to a canonical action + default target.

```json
"aliases": {
  "app-cmd":   { "action": "keys", "target": "app" },
  "shell-cmd": { "action": "run",  "target": "shell" }
}
```

Now `{"action": "shell-cmd", "value": "git status"}` runs in the `shell` pane.
An explicit `target` on the step overrides the alias default.

## Hooks

Hooks are **your** scripts; Backstage only calls them, with the project `env`
and the project root as the working dir. Use them to build a fixed, reproducible
starting state.

- `reset` runs before each take (restore state).
- `setup` runs instead when the scene sets `"fresh": true` (build from scratch).

Keeping each take's state frozen and restored is what makes the same scene
produce the same video.

## Productions (multi-scene videos)

A **production** records several scenes in order and stitches transition clips
between them into one video. Three pieces in `backstage.json`:

```jsonc
{
  "render": { "w": 1920, "h": 1080, "fps": 30 },

  "transitions": {
    "to-deploy": { "cmd": "node slide.js --title Deploy --out {{out}} --size {{w}}x{{h}}" },
    "to-guards": { "cmd": "node slide.js --title Guards --out {{out}} --size {{w}}x{{h}}" }
  },

  "productions": {
    "tour": {
      "scenes": ["01-intro", "02-deploy", "03-guards"],
      "transitions": [
        { "after": "01-intro",  "use": "to-deploy" },
        { "after": "02-deploy", "use": "to-guards" }
      ]
    }
  }
}
```

Run it with `backstage produce tour` (or ad-hoc with `--scenes`).

### render

The target geometry every clip is normalized to before concatenation.

| Key | Meaning | Default |
|-----|---------|---------|
| `render.w` / `render.h` | output size | `0` = the first scene clip's size (monitor native) |
| `render.fps` | output frame rate | falls back to `record.fps` |

### transitions

A transition is **a full command you write** — any tool, any language. Backstage
substitutes placeholders and then expects a clip:

| Placeholder | Becomes |
|-------------|---------|
| `{{out}}` | **(required)** path the command must write the `.mp4` to |
| `{{w}}` `{{h}}` `{{fps}}` | the render geometry / fps |
| `{{from}}` `{{to}}` | the scene names before and after the transition |

Reuse one script across transitions by varying its arguments (e.g. a `slide`
script called with different `--title`). The command runs with the project `env`
and the project root as its working dir, and must leave a non-empty mp4 at
`{{out}}` (Backstage normalizes it to the render geometry/fps).

### productions

| Field | Meaning |
|-------|---------|
| `scenes` | ordered scene names (files in `scenes/`) |
| `transitions[].after` | the scene this transition follows |
| `transitions[].use` | the transition name (key in `transitions`) |

A transition is placed between its `after` scene and the next one; a transition
after the last scene is ignored.

See also: [Writing scenes](scenes.md) · [CLI](cli.md) · [How it works](how-it-works.md).
