# Configuration — `backstage.json`

A project is a folder containing `backstage.json` plus `scenes/` and (optionally)
`hooks/`. Backstage finds the config by walking up from a scene to the nearest
`backstage.json`; that folder is the **leaf project**, and `${PROJECT}` /
`$PROJECT` in the config expand to it. A file may set `"extends": "../backstage.json"`
to inherit from a `backstage.json` in a strict ancestor directory. The directory
of the topmost file in that chain is the **workspace**. `${WORKSPACE}` /
`$WORKSPACE` expand to it. A project that does not extend another is its own
workspace.

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
| `extends` | path to an ancestor `backstage.json`; not itself inherited | — |
| `env` | map exported to panes, hooks, props, and offline transitions | — |
| `hooks.setup` | script run when a scene is `"fresh"` | — |
| `hooks.reset` | script run before every other take | — |
| `aliases` | custom action names → `{action, target}` | — |
| `layouts` | named stage layouts (see below) | — |
| `templates` | named HTML entries for presentation layouts | built-in when omitted |
| `presentations` | named presentation JSON files, templates and outputs | — |
| `vms` | named external VM connections or shared managed-stage references | — |
| `state-groups` | named lists of `vms` aliases that share one snapshot generation | — |

The video is written to `<project>/<record.out>/<scene-name>.mp4`. That path
is a projection of the last successful take. A failed or short take is kept
under `.takes/` and does not replace it. Internal readers use
`<scene-name>.take.json` when that manifest exists. A project that has never
published a manifest is read from the stable files with no lock; a first
`play` at the same time may replace `SCENE.mp4` during that read.

## Inheritance

`extends` is relative to the file that declares it and must name a
`backstage.json` in a strict ancestor directory. An absolute path is refused.
A chain can have more than one level. `extends` is not inherited.

Backstage merges the raw JSON from the workspace root down to the leaf before
decoding. The nearest file that **has** a key wins:

| Kind | Keys | Rule |
|---|---|---|
| Named maps | `vms`, `layouts`, `aliases`, `templates`, `presentations`, `transitions`, `productions`, `env`, `state-groups` | Union by name. The nearest entry replaces the inherited one whole. `null` removes that entry. |
| Settings | `record`, `popup`, `popup.style`, `render`, `render.threads`, `hooks` | Field by field. `popup.size` is replaced whole. |
| Scalars | `term` | The nearest present value wins. |

In named maps, only `null` removes an inherited entry. An empty string or `0`
is a value: `"env": {"FOO": ""}` still exports `FOO=`. For settings fields and
scalars, `""`, `0`, or `null` clears an inherited value, the same as never
setting the key. Defaults and validation run once, after the merge.

File references (`hooks.setup`, `hooks.reset`, `templates.*.entry`,
`presentations.*.file`, `transitions.*.live.prop`) are relative to the file that
declares them. Inherited relative references are rewritten so they stay correct
from the leaf (`hooks/reset.sh` in the root becomes `../hooks/reset.sh`).
An inherited absolute path is left unchanged and then refused. Pane `cwd` /
`cmd` and `transitions.*.cmd` are not rewritten; they run in the leaf. A parent
command that needs its own files uses `${WORKSPACE}`. In `env` values both
`${PROJECT}` and `${WORKSPACE}` expand, including the `$NAME` form. In pane and
transition commands only `${WORKSPACE}` expands, so an existing `$PROJECT` shell
variable is left alone.

Reads must stay inside the workspace. Writes (`record.out`, clips,
`production.mp4`, presentation `out`, `exports/`, `template init`) must stay
inside the leaf. `backstage config show` prints the merged configuration and
the file each key came from.

Two leaf projects that share a workspace remain two projects. Outputs, continuity
and provenance stay on the leaf.

To use a managed VM, declare `"vms": {"demo": {"stage": "shared-name"}}` and
set `"vm": "demo"` in the scene. `open`, `language` and `recorder` are optional
project-level overrides. `stage` cannot be mixed with explicit connection fields.
See [Manage VM stages](how-to-manage-vm-stages.md).

A state group names two or more of those aliases so their snapshots stay one
generation:

```json
"state-groups": { "household": ["laptop", "server"] }
```

Every member must be a managed `vms` alias. An alias or a stage belongs to at
most one group. The group name follows the snapshot name rule. Validation runs
after the merge; a bad group is a `config-error`. Scenes point at the group
with `vm-start.group` / `vm-end.group`. See
[Manage VM stages](how-to-manage-vm-stages.md#state-groups).

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
Only run projects you trust. Input paths must stay inside the workspace; output
paths must stay inside the leaf. Absolute paths, escapes past that boundary, and
known symlink escapes are refused. Scene names are limited to letters, numbers,
`.`, `_`, and `-`, and `env` keys must be valid shell identifiers.

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
| `render.workers` | parallel presentation render workers (one Chromium each) | `0` = `max(1, min(NumCPU/2, memoryBudget/2GiB, nMiss))` |
| `render.threads.prepare` | FFmpeg/FFV1 threads while preparing each presentation track | `max(1, n/w)` (`n` = CPUs, `w` = parallel tracks) |
| `render.threads.filter` | `-filter_complex_threads` for that prepare | `1` |
| `render.threads.encode` | libx264 threads for each presentation chunk encoder | `n`, then `max(1, encode/workers)` per chunk |

`render.workers` and `render.threads` are read only by presentation `render` /
`preview`. `produce` does not use these keys. `0`, `null` or an omitted field
means the default. A negative value is a configuration error. A very large
value is accepted as written (`workers` still capped at the number of missed chunks).

Each chunk encoder starts before that chunk's frame loop and codes while its
Chromium draws and captures screenshots, so `encode` threads share the CPUs
with the browsers. `encode-seconds` is the longest of those processes from
Start to Wait and overlaps that loop; it is not a bottleneck reading.
Screenshot and draw are the loop cost. The default encode count is still `n`
before dividing across workers; measure screenshot/draw/transfer p95 and
the frame-loop total before raising it.

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

See also: [Writing scenes](scenes.md) · [CLI](cli.md) · [How it works](design.md).

### A scene with a rate

A scene in a production is a name, or an object that says how to present it.

```json
"scenes": [
  "06-the-panel",
  { "scene": "07-a-real-hour",
    "speed": [
      { "until": "1:00",  "rate": 1 },
      { "until": "48:00", "rate": 10, "badge": "10x" },
      { "rate": 1 }
    ] }
]
```

| Field | Meaning | Default |
|---|---|---|
| `scene` | the name of the scene | required |
| `speed[].until` | where the stretch ends in the source clip | the end |
| `speed[].rate` | how much faster the stretch plays | required |
| `speed[].badge` | text drawn over the stretch | none |

The take on disk does not change. See
[Compose clips into one video](how-to-compose-a-production.md).

## Declarative presentations

```json
{
  "templates": {
    "household": { "entry": "templates/household/template.html" }
  },
  "presentations": {
    "complete": {
      "file": "presentations/complete.json",
      "template": "household",
      "out": "exports/complete.mp4"
    }
  }
}
```

| Field | Meaning | Default |
| --- | --- | --- |
| `templates.NAME.entry` | project-relative HTML file | required |
| `presentations.NAME.file` | project-relative presentation JSON | required |
| `presentations.NAME.template` | key in `templates` | built-in template |
| `presentations.NAME.out` | project-relative output MP4 | `exports/NAME.mp4` |

A presentation's `render` fields override the corresponding project `render`
values. Unset dimensions default to 1920×1080 for presentations; fps falls back
to project `render.fps`, then 30. Project `render.fps` itself defaults to
`record.fps`. These defaults do not change legacy production sizing.

JSON resource paths in a scene stay relative to the leaf and may use `..` to
reach a file in the workspace. Inherited template and presentation files are
rewritten relative to the leaf, then served over HTTP as workspace-relative
URLs so CSS, scripts, fonts and images next to an ancestor template keep
working. Paths cannot leave the workspace through traversal or symlinks;
outputs cannot leave the leaf.

`layouts` still configures recording panes. HTML templates define presentation
layouts. Visual HTML is referenced by a `visual` scene; there is no `slides`
registry. See [Presentations](presentations.md), [Templates](templates.md), or the
[composition walkthrough](how-to-compose-presentations.md).
