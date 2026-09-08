# Design

Backstage records a workflow that a person can perform. The recording is a
build output, and not an artifact that a person maintains by hand.

---

## Terms

Use one term for each meaning. These are the terms.

| Term | Meaning |
|---|---|
| **stage** | the environment that a scene runs in |
| **layout** | a named set of panes, from `backstage.json` |
| **pane** | one target that a step types into |
| **scene** | the ordered steps, in one file |
| **step** | one action in a scene |
| **prompter** | the narration box on the screen |
| **prop** | a script of the project that a step calls |
| **hook** | `setup` or `reset`; it builds the starting state |
| **take** | one run of `play` |
| **clip** | the `.mp4` that one take produces |
| **production** | several clips in one video |
| **guest** | a libvirt machine that a scene runs on |

---

## Four parts, and each one can be replaced

An engine performs a scene with four drivers.

| Driver | Job |
|---|---|
| `Stager` | prepare the stage, and take it down |
| `Recorder` | start the camera, and stop it |
| `Prompter` | show the narration, and dismiss it |
| `pane.Driver` | type at a target |

Each driver is an interface. A scene reads the same for each set of drivers.

The scene selects the set. A scene with no `vm` gets a tmux layout on this
machine, recorded from a monitor. A scene with a `vm` gets a guest, recorded
from inside it.

**The engine selects the four together.** A stage on a guest and a recorder on
the host would record this desktop while it types on another.

---

## One scene records one screen

Two computers are two scenes. Compose the clips afterwards.

This rule has two effects. A take does not have to hold two recorders in step.
And the same footage supports more than one layout later, without a new
recording.

---

## Speed belongs to the presentation

A take of fifty real minutes is the evidence. The rate to publish it at is a
different question, and somebody answers it more than one time.

A production therefore carries the rate, and the recording does not. Backstage
writes a retimed copy and leaves the take as it is. A clip written fast has
thrown the evidence away.

The `--speed` flag is a different thing, and it cannot serve here. It shortens
the delays while a scene is performed. The machine then gets less time, and a
budget that runs on the clock is never spent.

---

## Recording, and where the camera stands

| Recorder | Where | Use it for |
|---|---|---|
| `gpu-screen-recorder` | this machine | a stage on this machine |
| `wf-recorder` | inside the guest | almost every guest scene |
| framebuffer | outside the guest | a scene that ends the session |

A guest with no render node cannot use hardware encoding. `wf-recorder` records
through the compositor and encodes in software.

A recording from the host records a *window* that shows the guest. The scale of
the host compositor, the frame of the viewer, and the cursor of the host are
then in the film. A recording from inside gives the framebuffer of the guest at
its own size.

A recorder inside the session dies with that session. Use the framebuffer
recorder for a scene that ends a session.

---

## The framebuffer rate is measured

A grab through libvirt costs more time than a sleep. A loop that aims at four
frames each second gets fewer.

Backstage counts the frames and divides by the elapsed time. It assembles the
clip at that rate. The film is then real time.

A clip assembled at the rate that you asked for plays fast. It plays fast by
the same fraction that the loop missed, and it gives no error.

---

## The guest is Omarchy

This is a requirement. Backstage refuses another guest before it installs a
package or types a key.

Four things depend on it: the package repository, the compositor that records
without a GPU, the one seat that receives a virtual keyboard, and the verbs of
the shell for idle, notifications and restart.

Each failure of a guest stage looks like a different failure. A locked session
looks like a dead keyboard. A blank screen looks like a stopped compositor. To
find out later that the machine was never Omarchy is the most expensive of
them. Backstage says it first.

---

## Provenance

Backstage writes a `.facts.json` file beside each guest clip. The file records
the domain, the accounts, the address, and the Omarchy version.

A film is evidence. That file is the difference between a take that you can
produce again and a take that you can only record again.
