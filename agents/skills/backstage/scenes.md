# Writing Scenes

A scene is an ordered list of steps against a staged layout.

```json
{
  "name": "01-installing",
  "layout": "solo",
  "vm": "study",
  "fresh": true,
  "steps": [
    {"action": "dialog", "value": "One command, and the machine is under rules."},
    {"action": "run", "target": "shell", "value": "sudo pacman -S omahouse", "delay-after": 22}
  ]
}
```

| Field | Meaning |
|-------|---------|
| `name` | the output file, `<out>/<name>.mp4` |
| `layout` | which layout from `backstage.json` to stage |
| `vm` | which guest to run inside; empty stages on this machine |
| `fresh` | run the `setup` hook instead of `reset` |
| `reset` | default true; false leaves the state as the last take left it |

## Actions

| `action` | uses | does |
|----------|------|------|
| `dialog` | `value` | the narration box, typed out and held |
| `run` | `target`, `value` | type the text and press Enter |
| `type` | `target`, `value` | type the text and press nothing |
| `keys` | `target`, `commands` | named keys and literals in order |
| `prop` | `value`, `args` | run a project script and wait for it |
| `wait` | - | pause |

`delay-before`, `delay-after`, `key-delay` and `hold` tune any step.

## Delays Are The Script, Not Padding

`delay-after` is how long the viewer has to read what just happened. A command
whose output takes twenty seconds needs a delay that covers it; a scene that
moves on early records the next command being typed over the last one's output.

Measure the real thing once and write that number down.

## Do Not Type Shell Metacharacters

Typing goes through a virtual keyboard. A trailing `&`, a redirect, or a pipe
can arrive incomplete and leave half a command on screen for the rest of the
take. Anything that needs a background process belongs in a `prop`, or in what
the stage opens for you.

## What A Scene Should Not Do

- **Do not narrate what the screen already says.** The caption is for what the
  screen cannot show: why this matters, what it costs, what happens next.
- **Do not type a command the documentation does not teach.** A film outlives
  the release it was made from, and a demonstration of a verb nobody wrote down
  goes on being watched after that verb is gone. Consider a check that compares
  the commands a scene types against the page that teaches them.
