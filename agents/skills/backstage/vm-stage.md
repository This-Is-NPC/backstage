# The VM Stage

## Managed stages

Backstage can create local Omarchy VMs through libvirt/QEMU:

```bash
backstage stage doctor
backstage stage create demo --omarchy latest
backstage stage snapshot demo product-installed
backstage stage clone demo tutorial --snapshot product-installed
```

Reference a shared stage with `"vms": {"laptop": {"stage": "demo"}}`.
Keep `"vm": "laptop"` in the scene. Do not combine `stage` with explicit
connection fields. `open`, `language` and `recorder` still work.

Choose the scene's starting state explicitly when it matters:

- `"vm-start": {"mode": "clean", "snapshot": "product-installed"}` restores
  a cold snapshot; omitting `snapshot` uses the stage's `initial` snapshot.
- `"vm-start": {"mode": "reuse"}` keeps disk contents and reorganizes the
  desktop, like the existing behavior.
- `"vm-start": {"mode": "continue", "after": "01-install"}` preserves the
  entire live session. The predecessor must have succeeded in the same project,
  boot, session and execution type (rehearsal or recording). This mode skips
  reset/setup hooks and rejects explicit `fresh: true` or `reset: true`.

Rehearse a whole continuation chain before recording that chain. Never continue
a recording from a rehearsal. Restore/reboot/SSH access invalidates continuity.
Snapshots save disk and UEFI state, not running processes. Clones have separate
machine identities but inherit application data, including saved login sessions.

Managed stages have generated passwords; use `stage credentials NAME` only
when the filmed workflow needs that password. Do not assume username=password.
Managed hooks run on the host after the stage is reachable, before desktop
preparation. They receive `BACKSTAGE_VM_ADDRESS`, `BACKSTAGE_VM_ADMIN`,
`BACKSTAGE_VM_USER`, `BACKSTAGE_VM_KEY`, `BACKSTAGE_VM_KNOWN_HOSTS`,
`BACKSTAGE_VM_DOMAIN` and `BACKSTAGE_STAGE`.

`stage list`, `inspect`, `start`, `stop`, `ssh`, `snapshots`, `restore` and
`delete` manage the machines without a project. Creation/snapshots/restoration
leave a stage stopped. Recordings leave it running. `latest` is resolved at
creation; existing stages keep their installed version.

## Existing external VMs

A scene that names a `vm` runs inside a virtual machine: the steps are typed on
that computer's own keyboard and its screen is recorded from inside it. The
scene file says nothing about either.

```json
"vms": {
  "study": {
    "domain": "omahouse-parent",
    "user": "parent",
    "admin": "parent",
    "key": "~/.ssh/id_vms",
    "open": "omahouse-studio",
    "language": "C.UTF-8"
  }
}
```

| Field | Meaning |
|-------|---------|
| `domain` | the libvirt domain |
| `user` | the account whose session is filmed |
| `admin` | the account ssh connects as, with the key and sudo |
| `key` | the ssh private key, `~` expanded |
| `open` | what to leave on the desktop; empty opens a terminal |
| `language` | the locale that program runs under |

## The Guest Is Always Omarchy

This is a requirement, not a default, and the stage refuses anything else
before it installs or types a single thing. Everything here leans on it: the
repository the tools come from, the compositor that makes recording possible
without a GPU, the single seat a virtual keyboard appears on, and the shell's
own verbs for idle, notifications and restart.

The refusal is the point. Every failure in a vm stage looks like a different
failure, and finding out three steps later that the machine was never Omarchy
is the most expensive version of all of them.

## The Filmed Account Is Not The Administrator

`user` is who you are filming. `admin` is who ssh connects as. They are two
fields because they are two people: a household is worth filming precisely
because whoever is at the keyboard has no privilege, and handing the filmed
account a key and passwordless sudo so a recorder could reach it would be
filming a machine nobody described.

## What Staging Costs

Measured on a laptop, one guest:

| | stage | with a short scene |
|---|---|---|
| cold, the guest was off | ~66s | ~114s |
| warm, already running | ~14s | ~55s |

The difference is the boot. Teardown deliberately leaves the guest running,
because a take is usually one of several.

## Traps, All Of Them Measured

Every one of these looks like a different problem than it is.

**A locked session reads as a dead keyboard.** Omarchy locks at
`idle.lock`, 300 seconds out of the box. A take that idles past it comes back
locked, and every keystroke after that goes into a password field. Worse, an
account made with `useradd -m` has no password, so the lock cannot be answered
at all, and the shell refuses to restart while the session is locked.

Set a password, then disable the idle timers, then clear any lock that is
already up. In that order: each step is the reason the next one can work.

**A key does not wake a blanked screen; a mouse move does.** With `dpms: Off`,
a keystroke reaches the seat and changes nothing.

**A keybinding is not a script's tool.** The same `SUPER+RETURN` opened a
terminal on one guest and nothing on another, with the same bindings loaded and
no config errors on either. Launch through `systemd-run --user`, which also
keeps the program alive after the ssh that started it has gone.

**`hyprctl` needs `HYPRLAND_INSTANCE_SIGNATURE`.** Without it, it prints
nothing and exits cleanly, so a caller that parses the output gets an empty
answer rather than an error. That is how a stage meaning to close every window
quietly closed none of them.

**`sudo a && b` puts only `a` under sudo.** Use one command that does the whole
job.

**A take begins on an empty desktop.** Ask the compositor what is open and
close all of it. A window left from an earlier take was opened by something the
stage never knew about, so no list of program names will catch it.

**One language on screen.** A film whose captions are in one language and whose
package manager is in another reads as two recordings spliced together.

## Recording

`wf-recorder` inside the guest, through wlr-screencopy, encoding in software.
Not `gpu-screen-recorder`: a guest with `card0` and no render node cannot find
a driver and writes no file at all.

And not from the host. Recording a guest from the host records a *window*
showing the guest, with the host compositor's scaling, the viewer's chrome and
the host's own cursor in the film.

## Provenance

Each clip gets a `.facts.json` beside it: the domain, the accounts, the address
and the guest's Omarchy version. A film is evidence, and evidence has a
provenance. That file is the difference between a take that can be reproduced
and one that can only be re-shot.
