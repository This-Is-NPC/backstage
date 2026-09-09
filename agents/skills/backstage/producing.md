# Producing

`play` records one scene. `produce` records its scenes again and joins the
results. To arrange existing takes without recording, use `render`; see
[presentations.md](presentations.md).

```bash
backstage produce tour                        # a declared production
backstage produce --scenes 01-intro,02-deploy  # ad hoc
```

| Flag | Effect |
|------|--------|
| `--scenes a,b,c` | an ad hoc production from a list |
| `--transition NAME` | a transition between every pair |
| `--speed N` | shortens the delays *during* a take, so the machine gets less time |
| `--out FILE` | where the finished video goes |

## Play Part Of A Take At Another Rate

Record the real time. Choose the rate when you publish.

```json
{ "scene": "07-a-real-hour",
  "speed": [ { "until": "1:00",  "rate": 1 },
             { "until": "48:00", "rate": 10, "badge": "10x" },
             { "rate": 1 } ] }
```

Segments, not one rate: the interesting parts of a long take are at its ends.
The first minute at real time says the machine is real. The last minute at real
time is where the result arrives.

**`--speed` is not this.** That flag shortens the delays *during* a take, so the
machine gets less time and a budget that runs on the clock is never spent. A
fifty-minute take has to take fifty minutes.

The source take is not rewritten by retiming. However, invoking `produce`
records its scenes again. Use presentation track segments with `render` to
change the rate of files already on disk.

Each segment starts where the last ended, and the last one runs to the end of
the clip without saying where that is. A production that knew the length of its
own take would break the day the take got a second longer.

Keep the badge on for the whole stretch. Somebody who looks away has to be able
to tell a fast film from a fast machine.

The source rate and the speed multiply: a clip grabbed at 3 frames a second
plays as 30 at ten times. Record a long take low when you plan to speed it up.

## Composition Is Post-Processing

Recording and laying out are separate jobs, and keeping them separate is what
makes footage reusable. Record each computer on its own; decide afterwards
whether the film is one screen after another or two side by side.

Do not try to synchronise two recorders during a take. Two takes and a
composition step will always be easier to fix than one take that has to be
right twice at once.

## Frame The Screen, Do Not Cover It

A label drawn over the recording hides the thing the viewer came to see. Put
the screen inside a matte and let the margin carry the labels:

```
1 · INSTALLING              the parent's computer · nothing on it yet
   ┌──────────────────────────────────────────────────────────┐
   │                     the guest's screen                    │
   └──────────────────────────────────────────────────────────┘
              A computer with nothing watching over it yet.
```

Above: which scene, and whose screen. Below: what is being said. Nothing is
drawn on the screen itself.

## Say Which Machine, In Words A Stranger Knows

A viewer meeting the film for the first time does not know your machine names.
`the study` reads as a room. `the parent's computer` reads as a computer.

Name the role too, and keep it truthful for that scene: a machine that has just
been installed does not yet manage anything, and a label claiming it does is a
caption contradicting the screen underneath it.

## Cards Between Scenes Need Room To Breathe

A cut straight from one machine to another reads as a glitch. A card that names
the next machine, held four or five seconds with a fade at each end, is what
tells the viewer the computer changed.
