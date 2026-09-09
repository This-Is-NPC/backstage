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
Do not run the entire Backstage CLI as root to work around missing permissions.

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
`clean` and `continue` require managed stages. Omitting `vm-start` retains the
existing behavior: preserve disk contents, then prepare the desktop.

For managed stages, `clean`/`reuse` connect before running hooks and organize the
desktop afterwards. Hooks still run on the host and can use:

- `BACKSTAGE_STAGE`, `BACKSTAGE_VM_DOMAIN`, `BACKSTAGE_VM_ADDRESS`
- `BACKSTAGE_VM_USER`, `BACKSTAGE_VM_ADMIN`
- `BACKSTAGE_VM_KEY`, `BACKSTAGE_VM_KNOWN_HOSTS`

Passwords are not exported. Legacy external-VM hook ordering is unchanged.
Each clip's `.facts.json` records the stage, image origin, start mode, snapshot,
ISO version/checksum and provisioning recipe alongside the guest package version.
VM takes start recording after staging even with `produce --show-staging`;
installation and disk restoration are not part of the recorded clip.

## Snapshots and clones

```bash
backstage stage snapshot demo product-installed
backstage stage snapshots demo
backstage stage clone demo tutorial --snapshot product-installed
backstage stage restore tutorial initial
```

Snapshots shut down the VM cleanly and save a standalone disk plus matching
UEFI variables. Restore also leaves the machine stopped. These are disk states;
they do not restore RAM, terminal processes or open windows. Use `continue` for
live-session continuity.

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
```

The first command tests installation and recording without requiring clone
customization. The second also checks snapshot contents, clone identity,
restoration, and deleting an origin while retaining a working clone. Set
`BACKSTAGE_TEST_OMARCHY` to pin a release. Failed acceptance VMs are retained
under their printed `accept-*` names for diagnosis; delete them explicitly.
