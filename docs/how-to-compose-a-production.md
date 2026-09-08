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
| `--speed N` | a timing multiplier; a smaller number is faster |
| `--out FILE` | the path of the finished video |

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
