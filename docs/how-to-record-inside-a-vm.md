# How to record inside a virtual machine

**The task:** film a product on a clean machine, and do it again tomorrow with
the same result.

Backstage stages the guest, drives it, and records its screen from inside it.
The scene file does not change.

---

## Before you start

To have Backstage create and provision the VM, follow
[Create and manage VM stages](how-to-manage-vm-stages.md). The instructions below
describe connecting an existing, externally managed VM.

- Run libvirt without `sudo`. Add your account to the `libvirt` group.
- Prepare a guest that runs **Omarchy**. Backstage refuses any other guest.
- Create an account on the guest with a key and `sudo`.
- Start the guest one time, and confirm `ssh` answers.

**The guest must be Omarchy.** Backstage installs its tools from the Omarchy
repository. It records through the compositor of Omarchy. It puts a keyboard on
the one seat of the guest. It uses the shell of Omarchy for idle, notifications
and restart. Another distribution has none of these in the same places.

---

## 1. Declare the guest

```json
"vms": {
  "laptop": {
    "domain": "omahouse-kid",
    "user": "kid",
    "admin": "parent",
    "key": "~/.ssh/id_vms",
    "open": "omahouse-studio",
    "language": "C.UTF-8"
  }
}
```

| Field | Meaning |
|---|---|
| `domain` | the libvirt domain |
| `user` | the account that you film |
| `admin` | the account that `ssh` uses; it needs the key and `sudo` |
| `key` | the private key; `~` expands |
| `open` | the program to leave on the desktop; empty opens a terminal |
| `language` | the locale of that program |
| `recorder` | `inside` (default) or `framebuffer` |

**`user` and `admin` are two people.** You film a person who has no privilege.
Do not give the filmed account a key and `sudo` to make a recorder reach it.

---

## 2. Name the guest in the scene

```json
{ "name": "01-installing", "vm": "laptop", "layout": "solo", "steps": [] }
```

The steps go to the keyboard of that guest. The recorder records its screen.

---

## 3. Play the scene

```bash
backstage play scenes/01-installing.json
```

```
>> stage omahouse-kid: up (45.1s)
>> stage omahouse-kid: omarchy (0.5s)
>> stage omahouse-kid: tools (3.6s)
>> stage omahouse-kid: desktop (15.0s)
>> stage omahouse-kid: terminal (1.5s)
>> start recording
```

Each phase reports its time. A guest that is off costs about 66 seconds. A
guest that is on costs about 14.

If the guest is not Omarchy, Backstage stops before it installs anything.

---

## What the stage does to the guest

Backstage makes the desktop ready to film. It does these steps in this order.
Each step is the reason that the next step can work.

1. It sets a password. An account from `useradd -m` has none, and a locked
   session with no password has no answer.
2. It disables the idle timers. Omarchy blanks the screen after 150 seconds and
   locks it after 300.
3. It moves the mouse to wake the screen. A key does not wake a blank screen.
4. It types the password, in case a lock is already on the screen.
5. It restarts the shell, which makes the new timers current.
6. It dismisses the first-run cards. Those cards do not expire.
7. It closes every window, and opens one program.

---

## Record a scene that ends the session

The recorder runs inside the session. A scene that ends that session kills the
recorder with it. The clip is then short or has no duration.

Use the other recorder for that scene:

```json
{ "name": "05-out-of-time", "vm": "laptop", "recorder": "framebuffer" }
```

`framebuffer` photographs the screen through libvirt. Nothing in the guest can
stop it. It survives a logout, a compositor restart, and a reboot.

The rate is lower. Each frame is a full grab.

---

## Read the length the run reports

A clip can come back short without the session having ended. The recorder
inside the guest can stop before the scene does, and what it drops is the end.
The run reports it and names both lengths:

```
the take is 46.2s but the recorder ran for 62.0s: 15.8s is missing
```

The clip is kept. Watch it, and film the scene again if it lost the beat it was
made for. Each guest take also prints how long its recorder took to close.

A `wait` step at the end of the scene moves the loss into padding. Size it
against the take: about a quarter of the running time for a scene driving a
window. A scene that is long and still, and whose last beat is the point, is
better filmed with `framebuffer` -- it assembles at the rate it measured, so it
is real time and has no end to lose.

---

## What this does not do

- **It does not shut the guest down.** A take is usually one of several, and a
  boot costs minutes. Shut the domain down yourself when you finish.
- **It does not restore the guest.** Write a `setup` hook, or take a libvirt
  snapshot and revert it between takes.
- **It does not install the product you are filming.** The hook does that.

---

## Next

- Put the clips together: [Compose clips into one video](how-to-compose-a-production.md)
