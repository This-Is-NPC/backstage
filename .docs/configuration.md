# Configuration — `backstage.json`

A project is a folder containing `backstage.json` plus `scenes/` and (optionally)
`hooks/`. Backstage finds the config by walking up from a scene to the nearest
`backstage.json`; that folder is the **project root**, and `${PROJECT}` /
`$PROJECT` in the config expand to it.

```json
{
  "record":  { "monitor": "eDP-1", "fps": 30, "out": "recordings" },
  "popup":   { "size": [1200, 560], "cps": 32,
    "style": { "fontSize": 20, "chrome": "minimal" }
  },
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
| `popup.style.fontSize` | terminal font size for the built-in Prompter | `18` |
| `popup.style.title` | popup terminal window title | `instruction.md` |
| `popup.style.header` | text shown in the Prompter header | `instruction.md` |
| `popup.style.chrome` | header treatment: `default`, `minimal`, or `none` | `default` |
| `popup.style.class` | Hyprland window class for popup rules/closing | `backstage.popup` |
| `term` | terminal command used for the stage and popup | `ghostty` |
| `env` | map exported to panes, hooks, props, and offline transitions | — |
| `hooks.setup` | script run when a scene is `"fresh"` | — |
| `hooks.reset` | script run before every other take | — |
| `aliases` | custom action names → `{action, target}` | — |
| `layouts` | named stage layouts (see below) | — |

The video is written to `<project>/<record.out>/<scene-name>.mp4`.

## Popup style

The built-in Prompter is intentionally small: a Hyprland floating terminal that
types short narration. Use `popup.style` for basic project branding:

```jsonc
"popup": {
  "size": [1280, 420],
  "cps": 60,
  "style": {
    "fontSize": 22,
    "title": "backstage.prompt",
    "header": "backstage@demo:~$",
    "chrome": "minimal",
    "class": "backstage.demo.popup"
  }
}
```

`chrome` controls only the header:

| Value | Effect |
|-------|--------|
| `default` | dim framed header, matching the original `instruction.md` look |
| `minimal` | plain header text |
| `none` | no header; only typed text |

Complex HTML/CSS animation, multiple boxes, fullscreen chapter cards, or
transparent overlays belong in **live transitions** (below), not in the built-in
Prompter.

The current popup driver targets Hyprland and the configured terminal. macOS,
Windows, and non-Hyprland popup backends are out of scope for this driver.

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
    "to-browser": { "live": { "prop": "transitions/browser-card.sh", "args": ["--title", "Browser"] } },
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

Transitions have two render modes.

An **offline transition** is a full command you write. Backstage substitutes
placeholders and expects the command to write an mp4 to `{{out}}`:

| Placeholder | Becomes |
|-------------|---------|
| `{{out}}` | **(required)** path the command must write the `.mp4` to |
| `{{w}}` `{{h}}` `{{fps}}` | the render geometry / fps |
| `{{from}}` `{{to}}` | the scene names before and after the transition |

Reuse one script across transitions by varying its arguments (e.g. a `slide`
script called with different `--title`). The command runs with the project `env`
and the project root as its working dir, and must leave a non-empty mp4 at
`{{out}}` (Backstage normalizes it to the render geometry/fps).

Placeholder values are inserted verbatim into this trusted project shell command.
Quote placeholders in `backstage.json` when you need shell word boundaries or
literal handling, for example `--title '{{from}}'`.

A **live transition** runs a blocking project-relative prop while Backstage records
the screen. The prop owns its visual lifecycle: open the overlay/window, wait for
animation, close it, then exit.

```jsonc
"transitions": {
  "chapter-browser": {
    "live": {
      "prop": "transitions/chapter.sh",
      "args": ["--title", "Browser", "--duration", "2.2"]
    }
  }
}
```

When a production reaches a live transition, Backstage records that prop as its
own transition segment and stitches it between scene clips. If both `live` and
`cmd` are present, `live` takes precedence (the shared render-mode rule used by
productions, in-scene steps, and validation); `cmd` remains a fallback-compatible
offline definition for projects that choose it.

A live prop's `args` support the same placeholders as an offline `cmd`
(`{{w}}`, `{{h}}`, `{{fps}}`, `{{from}}`, `{{to}}`) **except `{{out}}`**: the
recorder owns the clip file, so `{{out}}` is substituted to an empty string for
live props — never hand a live prop the recording path.

### productions

| Field | Meaning |
|-------|---------|
| `scenes` | ordered scene names (files in `scenes/`) |
| `transitions[].after` | the scene this transition follows |
| `transitions[].use` | the transition name (key in `transitions`) |

A transition is placed between its `after` scene and the next one; a transition
after the last scene is ignored.

See also: [Writing scenes](scenes.md) · [CLI](cli.md) · [How it works](how-it-works.md).
