# How to compose clips into one video

**The task:** put several takes together, and change the layout later without a
new recording.

---

## Before you start

- Record each scene. Confirm that each `.mp4` is in `recordings/`.
- Install `ffmpeg`.

---

## 1. Declare the production

```json
"productions": {
  "tour": {
    "scenes": ["01-installing", "02-linking"],
    "transitions": [ { "after": "01-installing", "use": "chapter" } ]
  }
}
```

---

## 2. Produce it

```bash
backstage produce tour
```

The video is at `recordings/production.mp4`.

Use `--scenes` for a list without a declaration:

```bash
backstage produce --scenes 01-installing,02-linking --transition chapter
```

| Flag | Effect |
|---|---|
| `--scenes a,b,c` | a production from a list |
| `--transition NAME` | a transition between every pair |
| `--speed N` | it shortens the delays while a scene is performed |
| `--out FILE` | the path of the finished video |

---

## Play part of a take at another rate

Record the real time. Choose the rate when you publish.

A production names a scene as a bare string, or as an object that also says how
to present it:

```json
"productions": {
  "tour": {
    "scenes": [
      "06-the-panel",
      { "scene": "07-a-real-hour",
        "speed": [
          { "until": "1:00",  "rate": 1 },
          { "until": "48:00", "rate": 10, "badge": "10x" },
          { "rate": 1 }
        ] }
    ]
  }
}
```

| Field | Meaning |
|---|---|
| `until` | where the stretch ends, in the source clip |
| `rate` | `10` plays ten times faster; `1` is real time |
| `badge` | text drawn over the stretch while it plays |

Write `until` as seconds, `mm:ss` or `hh:mm:ss`.

**Do not use `--speed` for this.** That flag shortens the delays while the scene
is performed. The machine then gets less time, and a budget that runs on the
clock is never spent. A 50-minute take must take 50 minutes.

### Keep the ends at real time

The interesting parts of a long take are at its ends. Play the first minute at
real time, so the viewer sees that the machine is real. Play the last minute at
real time, because that is where the result arrives.

### The take is never changed

Backstage writes a second file and leaves the recording as it is. Ask for
another rate tomorrow, and no new recording is necessary.

### Each stretch starts where the last one ended

A production states no start. The last stretch runs to the end of the clip, and
must not say where the end is. A production that knew the length of its own
take would fail when the take became one second longer.

### The badge stays on

A viewer who looks away must be able to tell a fast film from a fast machine. A
mark that appears one time at the start does not tell them that later.

### The source rate and the speed multiply

A clip recorded at 3 frames each second plays as 30 at ten times. Record a long
take at a low rate when you plan to speed it up. This saves disk and time.

---

## Record each computer on its own

One scene records one screen. Record the second computer as a second scene.
Compose the clips afterwards.

Do not try to hold two recorders in step during one take. Two takes and a
composition step are easier to correct than one take that must be correct
twice at the same time.

---

## Frame the screen; do not cover it

A label on top of the recording hides the thing that the viewer must see. Put
the screen in a matte, and put the labels in the margin.

```
1 · INSTALLING              the parent's computer · nothing on it yet
   ┌────────────────────────────────────────────────────────┐
   │                  the screen of the guest                │
   └────────────────────────────────────────────────────────┘
            A computer with nothing watching over it yet.
```

The top margin says which scene, and whose screen. The bottom margin says what
happens. Nothing covers the screen.

---

## Name the machine in words that a stranger knows

A first viewer does not know your machine names. `the study` reads as a room.
`the parent's computer` reads as a computer.

State the role of the machine. Keep the role true for that scene. A machine
that you installed one minute ago does not manage anything yet.

---

## Give a chapter card enough time

A cut from one machine to another reads as a fault. Show a card that names the
next machine. Hold the card for four seconds. Fade it in and out.

---

## What this does not do

- **It does not know when each caption belongs.** Backstage does not yet write
  the time of each step beside the clip. Write those times yourself, or keep
  the narration inside the take.
- **It does not lay two clips side by side.** Use `ffmpeg` with `hstack` for
  that layout today.
