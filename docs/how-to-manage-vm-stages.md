# Create and manage VM stages

A managed stage is an Omarchy VM owned by Backstage. Stages belong to your local
user and can be referenced by several projects. Backstage uses the system
libvirt connection (`qemu:///system`), with QEMU/KVM running the machines.

## Prepare the host

```bash
backstage stage doctor
```

The host must be Linux x86_64 with access to `/dev/kvm` and libvirt. Install
QEMU, libvirt, OVMF/edk2 firmware, libguestfs (`guestfish`), xorriso,
OpenSSH, OpenSSL and ffmpeg (`ffprobe`). Enable libvirt's `default` NAT network.
Doctor reports missing dependencies without installing them or changing host
configuration.

Backstage creates a dedicated directory storage pool named `backstage-UID`,
under `/var/lib/libvirt/images/backstage-UID`. Libvirt must permit your account
to create this pool. Its directory must be writable by your account and
traversable by QEMU; disks are labelled through libvirt when a domain starts.
The pool filesystem must support POSIX ACLs. Capture writes a named read ACL
for your user on each catalog disk (`0600`, then `user:<uid>:r` and mask `r`)
so you can still open that image as a backing file after libvirt's DAC has
moved the owner to `libvirt-qemu`. Libvirt restores ownership only on the
writable overlay; it does not restore the backing chain. Doctor probes the
pool with a temporary file (apply the ACL, read it back, delete the file) and
lists any older catalog image you cannot read, printing a `sudo setfacl`
command for each. Doctor never runs `sudo`. Do not `chmod` a captured image
after that ACL: a later `chmod 0600` recomputes the mask from the group bits
and clears the named entry. Do not run the entire Backstage CLI as root to
work around missing permissions.

## Create a stage

```bash
backstage stage create demo --omarchy latest
```

Defaults: 4 virtual CPUs, 8 GiB RAM, 40 GiB virtual disk, 1920×1080 display,
US keyboard, `en_US.UTF-8`, UTC, UEFI without Secure Boot, unencrypted disk.
Video uses VirtIO when supported by the host, otherwise Bochs.
Override these with `--cpus`, `--memory`, `--disk`, `--resolution`, `--keyboard`,
`--locale` and `--timezone`. `--timeout` defaults to `40m` for installation.

The first creation downloads the official Omarchy ISO and verifies its SHA-256,
generates the installer's `cidata` configuration, installs the OS and prepares
the graphical session. Initial provisioning synchronizes package databases and
upgrades the guest before installing recording tools. It tests keyboard delivery and a short recording before
publishing a reusable base. Subsequent compatible creations reuse that base.

`latest` resolves once for this creation. The actual version, URL, checksum and
provisioning recipe are recorded. Existing stages are never updated by creating
another stage. Use `--omarchy 4.0.3`, for example, to select a specific ISO.
An unsupported installer format fails with diagnostics; it does not fall back
to typing through an installation wizard.

Each stage has a filmed account, `omarchy`, with password-protected sudo, and a
separate `backstage-admin` account for SSH automation. Credentials are generated
per stage. To reveal the filmed account's password explicitly:

```bash
backstage stage credentials demo
```

Creation ends with the VM **stopped** and its prepared state saved as `initial`.
To inspect it:

```bash
backstage stage list
backstage stage inspect demo --json
backstage stage start demo
backstage stage ssh demo
backstage stage stop demo
```

`list --json` and `inspect --json` do not disclose credentials. `stop` requests
an orderly shutdown; `stop --force` explicitly forces power off.

## Reference it from a project

```json
"vms": {
  "laptop": {
    "stage": "demo",
    "open": "omahouse-studio",
    "language": "C.UTF-8"
  }
}
```

A scene uses `"vm": "laptop"`. The project alias and shared stage name need not
match. `stage` cannot be combined with `domain`, `user`, `admin`, `key` or `uri`.
The existing explicit-connection format still works for externally managed VMs.

## Choose how each scene starts

Restore a saved state before a scene:

```json
"vm-start": { "mode": "clean", "snapshot": "product-installed" }
```

Omit `snapshot` to restore `initial`. For a clone, `initial` includes the
applications and files inherited from its origin snapshot.

Keep files and installed applications, but close windows and reopen the project's
configured program:

```json
"vm-start": { "mode": "reuse" }
```

Continue exactly where another scene left the running desktop:

```json
"vm-start": { "mode": "continue", "after": "01-install" }
```

`continue` preserves windows, processes and terminal state. It requires the
named scene to have succeeded on this stage, in this project, in the same boot
and graphical session. Rehearsals can continue rehearsals; recordings can
continue recordings. A successful recording cannot continue a rehearsal.
The predecessor name is its scene `name`, or the filename without `.json` when
`name` was omitted.

A failed/interrupted scene, stop, restore or `stage ssh` invalidates continuity.
Changes made by other tools cannot all be detected. A production validates the
order beforehand and reserves all its managed stages for the whole run.

`continue` skips setup/reset hooks and refuses explicit `fresh: true` or
`reset: true`. It never silently reboots or reconstructs a missing session.
A scene that declares `vm-end` cannot be the predecessor of `continue`.
`clean` and `continue` require managed stages. Omitting `vm-start` retains the
existing behavior: preserve disk contents, then prepare the desktop.

Save a disk state at the end of a successful take:

```json
"vm-end": { "snapshot": "theme-installed" }
```

The snapshot is created if it is missing. A snapshot with no origin (made by
`stage snapshot`) is refused unless `play --adopt` and you type the snapshot
name. A snapshot that belongs to another project or scene is always refused.
A rehearsal that would replace a recording is refused unless
`rehearse --replace-state`. A recording will not start `clean` from a
rehearsal snapshot. Produce has neither flag.

On success the guest is stopped, the new mapping and origin are committed
together, and the take's facts gain `end-state: { snapshot, image }`. A
capture that fails before that commit keeps the previous snapshot, writes
`result: capture-failed`, and keeps the take as an attempt.

For managed stages, `clean`/`reuse` connect before running hooks and organize the
desktop afterwards. Hooks still run on the host and can use:

- `BACKSTAGE_STAGE`, `BACKSTAGE_VM_DOMAIN`, `BACKSTAGE_VM_ADDRESS`
- `BACKSTAGE_VM_USER`, `BACKSTAGE_VM_ADMIN`
- `BACKSTAGE_VM_KEY`, `BACKSTAGE_VM_KNOWN_HOSTS`

Passwords are not exported. Legacy external-VM hook ordering is unchanged.
Each clip's `.facts.json` records the Backstage version, the take `result`
(`ok`, `steps-failed`, `short`, or `capture-failed`), `inputs-sha256` (the digest of the
picture inputs, including the clean image id or the `continue` predecessor),
and the time it was made. A guest clip also records the stage, image origin,
start mode, snapshot, ISO version/checksum, provisioning recipe, domain,
user, address and Omarchy package version. A `vm-start: clean` take adds
`start-image` (the restored image) and `start-state.snapshot` (the snapshot
name). Host takes omit the guest fields. A guest take also records
`timings` when a phase completed: `restore-stop-seconds` and
`restore-activate-seconds` on a clean start, `boot-seconds` only when
Begin booted a stopped domain, `session-seconds` plus `stage-phases`
(`up`, `omarchy`, `tools`, `desktop`, `terminal`), and on `vm-end`
`shutdown-seconds`, `capture-seconds`, `capture-bytes` (`st_blocks*512`)
and `capture-apparent-bytes`. A failed or skipped phase is omitted.
Timings do not enter `inputs-sha256` or status. The stage
`provision.log` gets one `timing <field> <value>` line per measure;
`stage snapshot` writes shutdown, capture and bytes, and
`stage restore` writes the two restore times. Capture progress is
`>> stage NAME: capture (12.3s, 4.1 GiB)`; without a measured size it
is `>> stage NAME: capture (12.3s)`. A timing-log write
failure is a warning on stderr and does not fail the take.
VM takes start recording after staging even with `produce --show-staging`;
installation and disk restoration are not part of the recorded clip.

## Snapshots and clones

```bash
backstage stage snapshot demo product-installed
backstage stage snapshots demo
backstage stage snapshots demo --origins
backstage stage snapshot-delete demo product-installed
backstage stage prune-states demo --workspace .
backstage stage clone demo tutorial --snapshot product-installed
backstage stage restore tutorial initial
```

Snapshots shut down the VM cleanly and save a standalone disk plus matching
UEFI variables. Restore also leaves the machine stopped. These are disk states;
they do not restore RAM, terminal processes or open windows. Use `continue` for
live-session continuity.

`stage snapshots` prints the name-to-image map. `--origins` prints each name as
`{ image, origin }`. `origin` is `null` for a manual snapshot (`stage snapshot`
or `initial`). A produced snapshot records the leaf project, scene,
`inputs-sha256`, the start and captured images, whether the take was a
recording or a rehearsal, when it was made, and the Backstage version.
`inspect --json` includes `snapshot-origins` next to `snapshots`.

`snapshot-delete` holds the stage lock and the image catalog, refuses
`initial`, removes the mapping and origin together, then collects unused
images. A collection failure leaves the deletion committed and reports
cleanup as pending; the next catalog mutation retries it.

`prune-states` uses the same deletion for every produced snapshot that no
valid scene in `--workspace` still declares as `vm-end` on that stage. It
walks the workspace the way `status` does. A configuration or scene error,
or an unreadable `backstage.json` under the workspace, aborts before any
lock — `--dry-run` included — and lists those errors and warnings, with
no plan: the broken file may be the one that still names the state. A
real run evaluates again under the stage and image-catalog locks and
aborts without removing if that second look finds an error, a warning
or a conflict. It never removes `initial`, a manual snapshot, a relative
or unreadable origin (a path that still has `..`, `.` or `//` is
unreadable; the engine stores a cleaned, resolved path), an origin that
points at a project outside this workspace (a hidden directory or a
nested workspace counts as outside), a snapshot whose origin project is
gone (`missing-project`, unless `--include-missing-projects`), a snapshot
a declared `vm-start clean` consumer still uses, or the image the stage
is running from. Reasons: `undeclared`, `initial`, `manual`,
`outside-workspace`, `missing-project`, `consumed`, `in-use`. `--dry-run`
prints `would remove`. Mid-run failure lists `failed` and `not attempted`.
Every error, including a missing argument or an unknown flag, is printed
once to stderr. Workspace errors, warnings and conflicts are not
repeated there. After flags parse, `--json` writes exactly one JSON
document: the report (success or mid-run failure, pending cleanup in
`cleanup-warnings`), a workspace abort, a conflict, or
`{"stage": STAGE, "error": "..."}`.

Clones use their own writable overlay and UEFI variables. Before their first
boot, Backstage changes their domain UUID, MAC, hostname, machine ID, SSH host
keys and access credentials. **Application files and login sessions stored in
those files are copied**, just like other snapshot contents.

Clones keep the original disk size. Resizing, live snapshots, remote hosts and
other guest operating systems are outside this version.

## Failure, recovery and deletion

```bash
backstage stage inspect demo
backstage stage create demo --omarchy latest
backstage stage delete demo
backstage stage delete tutorial --yes
```

Repeating `create` with the same configuration reuses the resolved ISO and
completed base. A failed stage remains registered with its phase and error.
Repeat a failed `clone` with the same origin and snapshot to retry that clone.
Provisioning phase logs and the validation clip live under
`${XDG_DATA_HOME:-~/.local/share}/backstage/machines/stages/NAME/`.
ISOs live under `${XDG_CACHE_HOME:-~/.cache}/backstage/iso/`.

Disk activation is journaled so interrupted replacements can be completed on
the next operation while the machine is stopped. Old disk files are retained
until deletion to avoid destroying a recovery source.

Deletion checks the libvirt UUID and removes only the stage's private resources.
It does not break clones: referenced immutable images remain available. Unused
snapshot images are collected after deletion; cached OS bases remain reusable.
Deletion does not erase project files or recorded videos.

## Contributor acceptance tests

Normal `go test ./...` never creates VMs. On a prepared host, explicitly run:

```bash
BACKSTAGE_VM_INSTALL_TEST=1 go test ./internal/machine -run TestRealOmarchyStages -v -timeout 70m
BACKSTAGE_VM_INTEGRATION=1 go test ./internal/machine -run TestRealOmarchyStages -v -timeout 70m
BACKSTAGE_VM_INTEGRATION=1 go test ./internal/engine -run TestRealVMEndProducerConsumer -v -timeout 180m -count=1
```

`TestRealVMEndProducerConsumer` installs a stage and records two takes.
Its context is 150 minutes; `-timeout` must be larger or the default 10
minute test timeout panics and skips `Cleanup`, leaving an `accept-*`
stage behind.

The first command tests installation and recording without requiring clone
customization. The second also checks snapshot contents, clone identity,
restoration, and deleting an origin while retaining a working clone. The
third records a `vm-end` producer and a clean consumer on a fresh
`accept-*` stage and deletes that stage in `Cleanup`, including when
`Create` leaves a partial record. Set `BACKSTAGE_TEST_OMARCHY` to pin a
release. Failed acceptance VMs from `TestRealOmarchyStages` are retained
under their printed `accept-*` names for diagnosis; delete them explicitly.
