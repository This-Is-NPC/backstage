# Producing

`play` makes one clip per scene. `produce` puts clips together.

```bash
backstage produce tour                        # a declared production
backstage produce --scenes 01-intro,02-deploy  # ad hoc
```

| Flag | Effect |
|------|--------|
| `--scenes a,b,c` | an ad hoc production from a list |
| `--transition NAME` | a transition between every pair |
| `--speed N` | timing multiplier, smaller is faster |
| `--out FILE` | where the finished video goes |

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
