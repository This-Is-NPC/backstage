# What does not work yet

**No other page in this documentation names a defect that is not here.** Each
item states what happens, and what to do until it is corrected.

---

## 1. Caption timing is not inferred from recording steps

**What happens.** Backstage does not write each action's time beside the take.
Narration cues need explicit times. Captions can now be added after recording
through a presentation.

**What to do.** Declare text and times in the scene's `narration.cues`, then
select cue IDs and a clock in [presentation captions](presentations.md#captions).
Do not assume action delays exactly identify the frames of an existing take.

---

## 2. Legacy produce is sequential

**What happens.** `produce` records and joins scenes one after another. It does
not position several clips inside the same frame.

**What to do.** Use [declarative presentations](how-to-compose-presentations.md)
and `render` for simultaneous screens and layout changes from existing media.

---

## 3. The narration box tiles on a compositor with the new parsers

**What happens.** The prompter asks the compositor to float its window with
`hyprctl keyword windowrule`. A Hyprland with the Lua configuration answers
`keyword can't work with non-legacy parsers`. The box then tiles, takes half
the screen, and moves the windows that it narrates. Each `dialog` step also
reports exit status 7.

**What to do in the meantime.** Add a static rule to your Hyprland
configuration:

```lua
o.window("backstage\\.popup", { float = true, size = "1400 560", move = "740 1240" })
```

---

## 4. The framebuffer recorder is coarse

**What happens.** Each frame is a full grab through libvirt. The rate is
several frames each second, and not thirty.

**This is a limit and not work in progress.** The recorder exists for the
scenes that the guest cannot record.

**What it buys, and it is not nothing.** It measures the rate its grabs
actually achieved and assembles at that rate, so a take is real time by
construction and has no ending to lose -- which is the defect in section 6, and
the framebuffer recorder does not have it. A measured take: a scene scripted at
3069 seconds came back 3075.2 seconds long, 8574 frames at 2.8 a second, on a
loaded guest.

**What to do in the meantime.** Use `inside` for a scene whose picture moves,
and where a coarse rate would show. Use `framebuffer` for a scene that ends its
own session, and for a long one whose last beat is the point and whose picture
is a still window -- there, the rate costs little and the certainty is worth
more.

---

## 5. A scene can type a command that the product no longer has

**What happens.** Nothing compares the commands of a scene against the
documentation of the product. A film outlives the release that it shows.

**What to do in the meantime.** Write a check that reads the commands of your
scenes and confirms that your documentation teaches each one. Run it before
each take.

---

## 6. The recorder inside a guest can lose the end of a take

**What happens.** A take from a guest can be shorter than the time it was
filmed over. The clip that comes back is continuous, correct, plays at real
speed and opens in every player -- and stops early. What it stops before is the
ending, which is where a scene puts the thing it was made to show. Nothing in
the run says so: the recorder exited cleanly, the container is whole and the
file is not empty.

One take, measured in full: a fifty-nine second scene, filmed over a sixty-two
second window, came back forty-six seconds long. Its frames run at thirty a
second from the first to the last with no gap anywhere, and its beats fall at
the times the scene asked for, so nothing was dropped and nothing was hurried
-- the last nineteen seconds are simply not there. The file on the guest is
short by the same amount, so nothing was lost in fetching it, and the guest
had five spare gigabytes and no swap in use at the time.

**How much is lost depends on what is on the screen.** Over nine takes of one
guest: a scene driving a terminal lost about a twentieth of itself, and a
scene driving a windowed program lost better than a fifth -- twelve of
fifty-nine seconds, twenty of seventy-one. The loss grows with the length of
the take, so a fixed pause at the end covers a short scene and not a long one.

**Asking for fewer frames a second does not help.** The same scene lost the
same five seconds at twelve, at twenty and at thirty frames a second. Whatever
the recorder is running out of, it is not the rate it was asked to capture at.

**The framebuffer recorder does not do this**, because it assembles at the rate
it measured rather than at a rate it asked for. See section 4 for what that
costs and when it is the better trade.

**What is not established** is what it is running out of. The reading that
fits is that `wf-recorder`, which encodes in software because these guests
have no render node, was that far behind real time when it was stopped and did
not write what it had not yet encoded. It has not been confirmed.

One measurement argues the loss has two parts rather than one. A terminal take
of 51 seconds lost 2.3, and one of 149 seconds lost 6.3 -- but whatever is
merely dropped at the moment of stopping should cost the same either time, and
this grew. A fixed part from closing plus a part proportional to how far behind
the encoder is would explain it. Also unconfirmed, and the terminal takes are
where the two would be the same size and hardest to tell apart.

**Backstage now says so.** A take materially shorter than the window it was
filmed in is reported as an error at the end of the run, naming both lengths.
Materially is two seconds, or two percent of the window, and never more than
five: the percentage covers a middling take, and the cap keeps it from growing
into the length of the losses it exists to catch. Two percent of a fifty-one
minute session would be a whole minute.
The take is kept: a short take is still worth looking at. Each take from a
guest also prints how long its recorder took to close and how much it wrote
while closing, which is what tells a recorder that was merely slow from one
that ran out of time.

**What to do in the meantime.** End the scene with a `wait` step, so the
overrun falls in the padding instead of in the last beat. Size it against the
take and not against a habit: a quarter of the running time for a scene
driving a window, and read the lengths that each run now reports, because a
wait long enough one day is not long enough the next.

---

## What is not a defect, and is easy to mistake for one

**A guest that stays on after a take.** `Teardown` leaves the domain running on
purpose. A boot costs minutes, and a take is usually one of several.

**A refusal of a guest that is not Omarchy.** The stage installs from the
Omarchy repository and uses the verbs of its shell. The refusal is first
because every later failure looks like something else.

**A scene that records one screen.** Two computers are two scenes. Compose the
clips afterwards.


## Presentation limits

- Preview renders the full temporary movie before opening playback controls.
  It does not update incrementally while editing. Render a short presentation
  when adjusting timing or layout.
- Video slots support axis-aligned rectangles, borders, rounded corners and
  shadows. Arbitrary 3D transforms and masks are outside the slot contract.
- Speech generation and automatic transcription are not implemented. Supply
  text in scene cues and, when desired, existing audio files.
- The timeline can end a visual scene before its animation finishes. Reserve
  enough time for the animation and a reading pause; inspect the final frame.
- Long or high-resolution renders can need substantial intermediate disk space.
  Rendering does not require keeping every raw frame in memory.
