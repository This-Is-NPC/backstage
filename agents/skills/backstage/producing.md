# Producing

`play` records one scene. `play --with-deps` records that scene's stale or
missing producers first, after printing the plan and reserving every stage
in it. Different stages run together under the host budget; a host-display
take never overlaps a VM take. A group consumer occupies every member
stage, so no other take on those stages runs with it. Remaking one
group producer also remakes its `group-sibling` producers in the same
project so the run keeps one generation, and remakes a stale or missing
producer on a sibling's chain. A blocked sibling stops the
plan before the lock. `--with-deps` and `--stale` mint the generation;
they refuse `--internal-state-generation` and `--internal-reserved-*`. `--jobs 1` is serial. `--stale` is a switch and the workspace is an optional
argument (`--stale [DIR]`, default `.`). It records every seeded scene in
the workspace, plus consumers that would go stale after those takes, with
the same rules. Each take is a
child process with its own log under `~/.local/state/backstage/jobs`.
`--jobs` and `--json` need `--with-deps` or `--stale`. `--json` emits
`job.progress` lines, a `plan.warning` line for each plan warning, and a
final report that repeats those warnings. Ctrl-C signals every
child, prints the final report once (the take in progress is interrupted)
and exits 130. `produce` stays serial: it records its scenes again and joins the
results. A scene at `--speed 1` without `--show-staging` is published to
`record.out` as soon as it succeeds; the published take is the raw clip, not
a retimed copy. A later failure does not undo that publication. A failed
take (`steps-failed`, `short`, or `capture-failed`) is kept as an attempt
even without `--keep-segments`. A scene with `vm-end` rewrites facts in the
work directory before that import. If one scene produces a snapshot another
scene in the same production consumes, the producer comes first.
A `state-groups` consumer waits for the producer of every member. The
production mints one generation and reserves every member stage. Silent
members are restored and stay off; only the scene `vm` boots.
`continue` cannot follow a scene that ends the guest. Other speeds and
`--show-staging` stay in the work directory. To arrange existing takes
without recording, use `render`; see [presentations.md](presentations.md).

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
