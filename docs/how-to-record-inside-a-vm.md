# How to record inside a virtual machine

**The task:** film a product on a clean machine, and do it again tomorrow with
the same result.

Backstage stages the guest, drives it, and records its screen from inside it.
The scene file does not change.

---

## Before you start

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

The rate is lower. Each frame is a full grab. Use `inside` for every other
scene.

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
