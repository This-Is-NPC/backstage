# The VM Stage

## Managed stages

Backstage can create local Omarchy VMs through libvirt/QEMU:

```bash
backstage stage doctor
backstage stage create demo --omarchy latest
backstage stage snapshot demo product-installed
backstage stage snapshots demo --origins
backstage stage snapshot-delete demo product-installed
backstage stage prune-states demo --workspace .
backstage stage clone demo tutorial --snapshot product-installed
```

The pool filesystem must support POSIX ACLs. Capture keeps catalog disks
`0440` and adds a named read ACL for your user so a later snapshot can
open them after libvirt has taken ownership. `stage doctor` probes that
ACL and prints `sudo setfacl` commands for older unreadable images; it
never runs `sudo`. Do not `chmod` a captured image after the ACL.

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
  A predecessor that declares `vm-end` is refused.
- `"vm-end": {"snapshot": "theme-installed"}` saves that disk state after a
  successful take. Missing snapshots are created. A manual snapshot needs
  `play --adopt` and typing the snapshot name. A rehearsal will not replace a
  recording without `rehearse --replace-state`. A recording will not start
  `clean` from a rehearsal. Produce has neither flag.   `play --with-deps`
  and `rehearse --with-deps` walk that chain from declared producers, keep
  a `continue` pair adjacent on the stage, refuse a replacement that
  belongs to another scene before the lock, and do not treat `--adopt` or
  `--replace-state` as implied for a producer. Different managed stages
  may record at once; a host take never overlaps a VM. `--stale` uses the
  same scheduler and also records downstream consumers. A clean restore waits for `image-catalog`. Create and
  Clone still hold the catalog for the whole verb.

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

`stage list`, `inspect`, `start`, `stop`, `ssh`, `snapshots`, `restore`,
`snapshot-delete`, `prune-states` and `delete` manage the machines without a
project. `prune-states` still needs `--workspace` so it can see which
scenes still declare a state.
Creation/snapshots/restoration leave a stage stopped. Recordings leave it
running. `latest` is resolved at creation; existing stages keep their
installed version.

`stage snapshots` is the name-to-image map. `--origins` adds `{ image, origin }`
(`origin` is `null` for `initial` and any hand-made snapshot). A produced
state records the leaf project, scene, inputs digest, images, take kind,
time and Backstage version. `inspect --json` includes `snapshot-origins`.
`snapshot-delete` refuses `initial` and removes the mapping and origin
together. `prune-states STAGE --workspace DIR` runs that deletion for
produced snapshots no valid scene in the workspace still declares as
`vm-end` on the stage. A broken config or scene, or an unreadable
`backstage.json` under the workspace, stops it before any lock — also
on `--dry-run`, which then lists those errors and warnings and prints
no plan. A real run evaluates again under the stage lock and aborts
without removing if that look fails. `image-catalog` is taken only
around each deletion. `--dry-run` otherwise lists the same set
without locks or new files, wording removals as `would remove`. It
keeps `initial`, manual or relative origins, an origin path that is not
already clean (`..`, `.`, `//`), origins outside the workspace (a hidden
directory or a nested workspace is outside), `missing-project` unless
`--include-missing-projects`, snapshots a `vm-start clean` consumer still
uses, and the image the stage is on. Reasons: `undeclared`, `initial`,
`manual`, `outside-workspace`, `missing-project`, `consumed`, `in-use`.
A failure mid-run lists `failed` and `not attempted`. Every error,
including a missing argument or an unknown flag, is printed once to
stderr; workspace errors, warnings and conflicts are not repeated.
After flags parse, `--json` writes one document: the report (`cleanup-warnings`
for pending cleanup), a workspace abort, a conflict, or
`{"stage": STAGE, "error": "..."}`.

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

Every recorded clip gets a `.facts.json` beside it, on the host and on a VM:
the Backstage version, when it was made, and a `result` (`ok`, `steps-failed`,
or `short`, or `capture-failed` when `vm-end` does not commit). A guest clip
also records the machine; a clean start records `start-image` and
`start-state.snapshot`. A successful `vm-end` adds `end-state`. Completed
VM phases go in `timings` (seconds, capture bytes, `capture-mode`,
`image-depth`, `catalog-wait-seconds`, and `capture-fallback` when a
delta check flattened); they are not an input. `boot-seconds` only when Begin booted a stopped
domain. `vm-end` and `stage snapshot` stop the guest, then hold
`image-catalog` only around the decision/marker and the catalog
commit (`qemu-img convert` is unlocked). A marker in
`machines/pending/` protects the parent until commit; `Collect` reads
markers first and drops a marker whose stage record is gone, only
when the listed paths match managed storage. Unreadable markers or
paths outside the store stop Collect; Recover and Delete skip them
with a warning and doctor lists them. A leftover marker after a
committed `stage snapshot` is a warning. `capture-seconds` excludes catalog
waits (`catalog-wait-seconds`). `Create` and `Clone` still hold the
catalog for the whole verb. `vm-end` and `stage snapshot` write a qcow2 delta against the
activation image when the backing chain matches the catalog, up to the
host `max-image-depth` (`BACKSTAGE_IMAGE_DEPTH`, then
`machines/settings.json`, then 8). That default was measured; lower
it with `BACKSTAGE_IMAGE_DEPTH` or `machines/settings.json` if a
cold chain is slow. `0` disables deltas. A chain that
includes a cached OS base stays complete (`cached-base`) so the base
is never promoted. An unreadable `bases/*.json` flattens
(`base-cache-unreadable`) instead of failing the capture. An unknown settings key is an error; doctor
reports it. `Create` and `Clone` keep `initial` complete. An older
`Collect` may delete unused schema 1 images and then stop on schema 2;
`vm-end`, `snapshot-delete` and `prune-states` on that binary warn
`pending cleanup` every time. Catalog disks are `0440` plus the
named read ACL. The stage
`provision.log` records `timing <field> <value>` for
the same measures, including a manual `stage snapshot` and
`stage restore`. A film is
evidence, and evidence has a provenance. That file is the difference between
a take that can be reproduced and one that can only be re-shot.
