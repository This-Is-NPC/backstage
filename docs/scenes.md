# Scenes

A scene is either a recording script (`recording`, the default) or an HTML/SVG
visual (`visual`). Both can own narration text and audio. The example below
records ordered steps against a staged layout.

```json
{
  "name": "01-installing",
  "layout": "solo",
  "vm": "laptop",
  "recorder": "inside",
  "fresh": true,
  "steps": [
    {"action": "dialog", "value": "One command, and the machine is under rules."},
    {"action": "run", "target": "shell", "value": "sudo pacman -S omahouse", "delay-after": 22}
  ]
}
```

## Fields

| Field | Meaning |
|---|---|
| `type` | `recording` (default) or `visual` |
| `entry` | leaf-relative HTML entry, required for visual scenes; `..` may reach the workspace |
| `duration` | available duration in seconds, required for visual scenes |
| `narration` | language and named text/audio cues |
| `audio` | named reusable audio assets: `{ "name": { "file": "assets/sound.wav" } }` |
| `name` | the name of the output file, `<out>/<name>.mp4` |
| `layout` | the layout to stage, from `backstage.json` |
| `vm` | the guest to run inside; empty stages on this machine |
| `vm-start` | `{"mode":"clean"}`, `{"mode":"reuse"}`, or `{"mode":"continue","after":"previous-scene"}`. Clean may add `"group":"household"` with a required `snapshot` |
| `vm-end` | `{"snapshot":"theme-installed"}` — save that disk state after a successful take. May add `"group":"household"` |
| `recorder` | `inside` or `framebuffer`; it overrides the guest |
| `fresh` | `true` runs the `setup` hook in place of `reset` |
| `reset` | `false` skips setup/reset hooks; it does not preserve open windows |
| `inputs` | extra files or directories whose contents belong in the take digest |
| `steps` | the ordered actions |

`clean` restores a named `snapshot`, defaulting to `initial`; `reuse` keeps disk
contents and reorganizes the desktop; `continue` preserves the live session and
skips reset/setup hooks. Clean and continue require managed stages. `vm-end`
requires a managed stage, refuses `initial`, and cannot be the predecessor of
`continue`. `group` is only valid on a clean start or a `vm-end`, and then
`snapshot` is required. The scene `vm` must be a member of that group. A scene
cannot start and save the same group snapshot. A production that consumes a
snapshot another scene in the same run produces must list the producer first.
See [Manage VM stages](how-to-manage-vm-stages.md#choose-how-each-scene-starts)
and [state groups](how-to-manage-vm-stages.md#state-groups).

## Actions

| `action` | Fields | Result |
|---|---|---|
| `dialog` | `value` | the narration box types the text and holds it |
| `run` | `target`, `value` | the target receives the text and Enter |
| `type` | `target`, `value` | the target receives the text |
| `keys` | `target`, `commands` | the target receives the named keys in order |
| `prop` | `value`, `args` | a script of the project runs to its end |
| `wait` | — | the scene pauses |

Add `delay-before`, `delay-after`, `key-delay` or `hold` to any step.

## Write the delay that the command needs

`delay-after` is the time that the viewer gets to read the result. Measure the
command one time. Write that number.

A scene that continues too early records the next command over the output of
the last one.

## Do not type shell metacharacters

A virtual keyboard sends each character. A trailing `&`, a redirect or a pipe
can arrive incomplete. The screen then shows half a command for the rest of the
take.

Put a background process in a `prop`. Let the stage open the programs that the
scene drives.

## Rehearse before you play

```bash
backstage rehearse scenes/01-installing.json
```

The steps run with short delays and no recorder. Correct the flow first.
A rehearsal writes no clip and no facts.

## Facts

Every recording writes `<out>/<name>.facts.json` beside the clip. The sidecar
names the Backstage version, `inputs-sha256` (the digest of what changes the
picture), and a `result`: `ok`, `steps-failed` if a step failed, `short`
if the take is materially shorter than the recorder ran, or `capture-failed`
if `vm-end` could not commit the snapshot. A failed or short take is still
kept. A successful `vm-end` rewrites the sidecar with
`end-state: { snapshot, image }`; a failed capture writes
`end-state: { snapshot, status: "failed" }`. A guest take also records the
machine it was filmed on; a clean start records the restored image and
snapshot name. A group start also records `group-members` (stage, snapshot,
image, generation of every member, including the filmed one). Completed VM phases go in `timings` (`shutdown-seconds`,
`capture-seconds`, `capture-bytes`, `capture-apparent-bytes`,
`restore-stop-seconds`, `restore-activate-seconds`, `boot-seconds`
(only when Begin booted a stopped domain),
`session-seconds`, `stage-phases`). They are omitted when the phase
did not run or failed. They are not part of `inputs-sha256`.

## Inputs digest

`inputs-sha256` hashes a canonical encoding of the take's picture inputs:
the scene without `narration` or `audio`; the resolved `vms` entry (stage,
open, language, and the effective recorder); the resolved layout; merged
`popup` and `term`; the effective steps after alias resolution, and the
definitions of aliases those steps use; each used transition (resolved
definition, live prop content, `render.fps` with a fallback to
`record.fps`, and `render.w` / `render.h`); the hook that would run
(`setup` when `fresh`, otherwise `reset` when enabled, none for
`continue`); each prop file a step calls, directly or through an alias;
each path in `inputs`; merged `env`; `record.fps`; the take's `--speed`
and `--show-staging`; and the start state (`clean:<image-id>`,
`continue:<after>`, or empty for reuse and for a scene with no VM).
Unused aliases and transitions do not enter. A directory in `inputs`
hashes every regular file below it, sorted by workspace-relative path.
A symlink to a file is included when the target stays inside the
workspace: the digest keys the link's own path and hashes the target's
content, so changing either one moves the take. A symlink that leaves
the workspace, or a symlink to a directory, fails the digest. Paths are
leaf-relative and must stay inside the workspace. Moving the workspace
does not change the digest: expanded `${PROJECT}` and `${WORKSPACE}`
values fold back to those tokens, and files are keyed by
workspace-relative path plus content hash.

A prop that sources another file, or a hook that copies a directory, hides
that dependency. Name it in `inputs`:

```json
{ "inputs": ["props/browser-lib.sh", "hooks/files"] }
```

A missing input fails the take before recording. A rehearsal does not
compute the digest unless the scene declares `vm-end`. Changing narration
or unused aliases does not force a new take.

## Do not narrate what the screen shows

The caption states what the screen cannot show: why the step matters, what it
costs, and what happens next.


## Visual scenes and editorial content

A visual scene requires `entry` and a positive `duration`. It cannot declare
`layout`, `vm`, `vm-start`, `vm-end`, `steps`, `recorder`, `fresh` or `reset`. Use it in a
presentation with `render` or `preview`; recording commands reject it.

```json
{
  "name": "explain",
  "type": "visual",
  "entry": "slides/explain.html",
  "duration": 4,
  "narration": {
    "language": "pt-BR",
    "cues": [
      {
        "id": "rule",
        "start": 0,
        "end": 3,
        "text": "Cada computador aplica a regra localmente.",
        "audio": "assets/audio/rule.wav"
      }
    ]
  },
  "audio": {
    "ambience": { "file": "assets/audio/room.wav" }
  }
}
```

Its HTML follows the [template contract](templates.md). `duration` is the
available length; the presentation event decides how much is shown. An event
cannot exceed that length, but can cut the scene short. Reserve time for both
animation and a pause to read its ending.

Recording scenes accept the same `narration` and `audio` blocks alongside their
existing fields. These blocks do not trigger speech playback during recording.

| Cue field | Meaning |
| --- | --- |
| `id` | nonempty identifier, unique within the scene |
| `start` / `end` | nonnegative start and later end, in source seconds |
| `text` | nonempty text; the source of truth for captions and future speech |
| `audio` | optional project-relative audio file; no voice generation occurs |

Cue time refers to the original take for recording scenes, or local scene time
for visual scenes. It is authored explicitly, not inferred from `steps` or
`dialog` timing. Instructions and editorial narration are separate.

The presentation selects cue IDs, optional scene audio assets and their timing.
It can follow video cuts and rates or place speech independently in the final
film. See [audio](presentations.md#audio) and [captions](presentations.md#captions).
