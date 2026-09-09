# Scenes

A scene is an ordered list of steps against a staged layout. One file, one
scene, one clip.

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
| `name` | the name of the output file, `<out>/<name>.mp4` |
| `layout` | the layout to stage, from `backstage.json` |
| `vm` | the guest to run inside; empty stages on this machine |
| `vm-start` | `{"mode":"clean"}`, `{"mode":"reuse"}`, or `{"mode":"continue","after":"previous-scene"}` |
| `recorder` | `inside` or `framebuffer`; it overrides the guest |
| `fresh` | `true` runs the `setup` hook in place of `reset` |
| `reset` | `false` keeps the state that the last take left |
| `steps` | the ordered actions |

`clean` restores a named `snapshot`, defaulting to `initial`; `reuse` keeps disk
contents and reorganizes the desktop; `continue` preserves the live session and
skips reset/setup hooks. Clean and continue require managed stages. See
[Manage VM stages](how-to-manage-vm-stages.md#choose-how-each-scene-starts).

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

## Do not narrate what the screen shows

The caption states what the screen cannot show: why the step matters, what it
costs, and what happens next.
