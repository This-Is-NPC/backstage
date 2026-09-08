# What does not work yet

**No other page in this documentation names a defect that is not here.** Each
item states what happens, and what to do until it is corrected.

---

## 1. A caption cannot be added after the take

**What happens.** Backstage does not record the time of each step beside the
clip. A production cannot know when to show each line of narration.

**What to do in the meantime.** Keep the narration inside the take with
`dialog` steps. To burn a caption band under a composed video, read the
`delay-after` values of the scene and write the times by hand.

---

## 2. A production cannot lay two clips side by side

**What happens.** `produce` joins clips one after the other. It has no layout
that puts two screens in one frame.

**What to do in the meantime.** Use `ffmpeg` with the `hstack` filter. Pad the
shorter clip with `tpad` so that both clips end together.

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
scenes that the guest cannot record, and those scenes are short.

**What to do in the meantime.** Use `inside` for every scene that does not end
its own session.

---

## 5. A scene can type a command that the product no longer has

**What happens.** Nothing compares the commands of a scene against the
documentation of the product. A film outlives the release that it shows.

**What to do in the meantime.** Write a check that reads the commands of your
scenes and confirms that your documentation teaches each one. Run it before
each take.

---

## What is not a defect, and is easy to mistake for one

**A guest that stays on after a take.** `Teardown` leaves the domain running on
purpose. A boot costs minutes, and a take is usually one of several.

**A refusal of a guest that is not Omarchy.** The stage installs from the
Omarchy repository and uses the verbs of its shell. The refusal is first
because every later failure looks like something else.

**A scene that records one screen.** Two computers are two scenes. Compose the
clips afterwards.
