# How to record your first scene

**The task:** make a video of a workflow, and make it again when the product
changes.

This page is sufficient on its own. At the end you have an `.mp4` and a scene
file that produces it again.

---

## Before you start

- Install Backstage. Run `backstage --version` to confirm it.
- Install `tmux`, a terminal, `gpu-screen-recorder`, and `ffmpeg`.
- Use a Hyprland desktop. Backstage stages windows with the compositor.
- Export `WAYLAND_DISPLAY`. The recorder cannot find the screen without it.

---

## 1. Make the project

```bash
mkdir -p tour/scenes && cd tour
```

```bash
cat > backstage.json <<'JSON'
{
  "record": { "monitor": "eDP-1", "fps": 30, "out": "recordings" },
  "layouts": { "solo": { "panes": [ { "name": "shell" } ] } }
}
JSON
```

`monitor` is the output to record. Run `hyprctl monitors` to read its name.

---

## 2. Write the scene

```bash
cat > scenes/01-hello.json <<'JSON'
{
  "name": "01-hello",
  "layout": "solo",
  "steps": [
    {"action": "dialog", "value": "One command, and the tree is a repository."},
    {"action": "run", "target": "shell", "value": "git init demo", "delay-after": 3},
    {"action": "run", "target": "shell", "value": "ls -a demo", "delay-after": 4}
  ]
}
JSON
```

`delay-after` is the time the viewer gets to read the result. Measure the
command once, and write that number.

---

## 3. Rehearse it

```bash
backstage rehearse scenes/01-hello.json
```

The steps run with short delays and no recorder. Correct the flow before you
record it.

---

## 4. Record it

Close your other windows first. `play` takes the whole display.

```bash
backstage play scenes/01-hello.json
```

```
>> stage layout: solo
>> start recording
   step 1: dialog
   step 2: run -> shell
   step 3: run -> shell
>> stop recording
>> done. recordings/01-hello.mp4  (stage open — backstage kill)
```

The video is at `recordings/01-hello.mp4`.

---

## 5. Strike the set

```bash
backstage kill
```

The stage stays open after a take, so that you can look at the result.

---

## What this does not do

- **It does not build the state the scene needs.** Write a `setup` hook for
  that. See [Configuration](configuration.md).
- **It does not record two screens.** One scene records one screen. Record a
  second scene, then compose the clips. See
  [Compose clips into one video](how-to-compose-a-production.md).
- **It does not check that the scene is still true.** A scene that types a
  command the product no longer has records that command failing.

---

## Next

- Record on a virtual machine: [Record inside a virtual machine](how-to-record-inside-a-vm.md)
- Put clips together: [Compose clips into one video](how-to-compose-a-production.md)
