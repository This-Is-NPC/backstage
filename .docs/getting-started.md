# Getting started

We'll build a project from scratch and record a first `.mp4`. The example records
a tiny **git** tutorial (any tool works). At the end you'll have
`my-tutorial/recordings/01-commit.mp4`.

New to the vocabulary (Stage, Scene, Prop)? See [Concepts](concepts.md).

---

## 1. Folder

A project is just a folder with `backstage.json`, `scenes/`, and (optionally)
`hooks/`.

```bash
mkdir -p my-tutorial/scenes my-tutorial/hooks
cd my-tutorial
```

## 2. `backstage.json`

The config says where to record, what the popup looks like, and which **layouts**
scenes can stage. For a CLI tutorial, one fullscreen terminal is enough.

```json
{
  "record":  { "monitor": "eDP-1", "fps": 30, "out": "recordings" },
  "popup":   { "size": [1200, 520], "cps": 32 },
  "hooks":   { "setup": "hooks/setup.sh", "reset": "hooks/reset.sh" },
  "layouts": {
    "solo": { "fullscreen": true, "panes": [
      { "name": "term", "cwd": "work", "cmd": "bash" }
    ] }
  }
}
```

- `out` is relative to the project → `my-tutorial/recordings/`.
- `work` is where the terminal opens (the stage for git). The hooks create it.
- Set `monitor` to yours (`hyprctl monitors`).

Full reference: [Configuration](configuration.md).

## 3. Hooks (reproducible state)

Hooks are **yours**; Backstage just calls them. `reset` runs before every take;
`setup` runs when a scene is `"fresh"`. Here both leave a clean git repo in
`work/`.

`hooks/reset.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
PROJECT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$PROJECT/work"
rm -rf "$WORK"; mkdir -p "$WORK"; cd "$WORK"
git init -q
printf 'hello\n' > hello.txt
```

`hooks/setup.sh` (same as reset here):

```bash
#!/usr/bin/env bash
exec "$(dirname "$0")/reset.sh"
```

```bash
chmod +x hooks/*.sh
```

## 4. The scene

A scene mixes `dialog` boxes (typed Prompter) with pane actions. `run` sends a
command + Enter; `target` is a pane `name`.

`scenes/01-commit.json`:

```json
{
  "name": "01-commit",
  "layout": "solo",
  "reset": true,
  "steps": [
    {"action": "dialog", "value": "Let's make the first commit in a git repo."},
    {"action": "run", "target": "term", "value": "git status",            "delay-after": 2},
    {"action": "dialog", "value": "hello.txt is untracked. Let's add it."},
    {"action": "run", "target": "term", "value": "git add hello.txt",      "delay-after": 1.5},
    {"action": "run", "target": "term", "value": "git commit -m 'first commit'", "delay-after": 3},
    {"action": "dialog", "value": "Done — first commit made."},
    {"action": "run", "target": "term", "value": "git log --oneline",      "delay-after": 3}
  ]
}
```

## 5. Record

```bash
backstage play scenes/01-commit.json
```

Backstage finds `backstage.json`, runs the `reset` hook, opens the fullscreen
terminal, records, performs the steps, and stops. Output:
`recordings/01-commit.mp4`. Close the stage afterwards:

```bash
backstage kill
```

Want to check the flow first, without recording?

```bash
backstage rehearse scenes/01-commit.json
```

## 6. Iterate

- **Wording / timing**: edit the `value`s and `delay-after`s and re-run. Same
  baseline + same scene → same video.
- **Inspect a frame** without rewatching:
  ```bash
  ffmpeg -i recordings/01-commit.mp4 -ss 8 -frames:v 1 /tmp/f.png
  ```
- **Box too fast/slow**: tweak `popup.cps` in `backstage.json`.

---

## Recipes

### Split layout (app + shell)

Add a layout with two panes and target by name:

```json
"split": { "fullscreen": true, "panes": [
  { "name": "app",   "cmd": "lazygit", "cwd": "work" },
  { "name": "shell", "cmd": "bash",    "cwd": "work", "size": "38%" }
] }
```

```json
{"action": "keys", "target": "app", "commands": ["down", "space", "c"]}
```

### Record a web app (Props)

Backstage records the whole screen, so a `prop` step that launches a browser is
captured. This is how you turn an e2e suite into a feature-documentation video:
point a Prop at the run.

```json
{
  "name": "feature-tour",
  "layout": "solo",
  "steps": [
    {"action": "dialog", "value": "Walking through the checkout flow."},
    {"action": "prop", "value": "props/e2e.sh", "args": ["checkout.spec.ts"], "delay-after": 1}
  ]
}
```

`props/e2e.sh` runs your tool headed (so it's visible to the recorder), e.g.:

```bash
#!/usr/bin/env bash
exec npx playwright test "$1" --headed
```

The tests already click through every feature; the run becomes the demo,
regenerated whenever you re-record.

### Stitch scenes into one video (a production)

Record several scenes back to back with a titled slide between them. Declare a
transition (any command that writes an mp4 to `{{out}}`) and a production in
`backstage.json`:

```jsonc
"render": { "fps": 30 },
"transitions": {
  "to-log": {
    "cmd": "ffmpeg -y -f lavfi -i color=c=black:s={{w}}x{{h}}:d=1.2 -vf \"drawtext=text='{{to}}':fontcolor=white:fontsize=72:x=(w-tw)/2:y=(h-th)/2\" {{out}}"
  }
},
"productions": {
  "intro": {
    "scenes": ["01-commit", "02-log"],
    "transitions": [ { "after": "01-commit", "use": "to-log" } ]
  }
}
```

```bash
backstage produce intro          # → recordings/production.mp4
```

Each scene is recorded, the `to-log` slide is rendered between them, and the
clips are concatenated into one video. Staging is hidden by default; add
`--show-staging` to include it. Ad-hoc, without declaring a production:

```bash
backstage produce --scenes 01-commit,02-log --transition to-log
```

---

Next: [Writing scenes](scenes.md) (full action + key reference) ·
[Configuration](configuration.md) (productions + transitions) ·
[How it works](how-it-works.md) (the pipeline inside).
