# Compose a presentation from existing recordings

**The task:** show three recorded screens together, change to two, then explain
an idea with an animated SVG. Changing the montage does not re-record the takes.

## Before you start

- Have three video files in `recordings/`: `parent.mp4`, `laptop.mp4` and `browser.mp4`.
- Install Chromium (or Google Chrome), FFmpeg and ffprobe.
- Run the commands below from your project directory. All JSON paths are relative
  to that directory.

To try this with synthetic inputs instead, use the
[runnable example](../examples/presentation/README.md).

## 1. Create a template

```bash
backstage template init household
```

This writes an editable template and a sample animated SVG page under
`templates/household/`. It refuses to overwrite an existing directory.

Add these entries to `backstage.json`:

```json
{
  "render": { "w": 1920, "h": 1080, "fps": 30 },
  "templates": {
    "household": { "entry": "templates/household/template.html" }
  },
  "presentations": {
    "complete": {
      "file": "presentations/complete.json",
      "template": "household",
      "out": "exports/complete.mp4"
    }
  }
}
```

## 2. Declare the visual scene

Create `scenes/explain.json`:

```json
{
  "name": "explain",
  "type": "visual",
  "entry": "templates/household/visual.html",
  "duration": 4
}
```

The sample arrow takes three seconds to finish. Four seconds leaves a pause to
read the completed diagram. A scene's duration does not extend the timeline.

## 3. Arrange the recordings

Create `presentations/complete.json`:

```json
{
  "version": 1,
  "duration": 8,
  "parameters": { "title": "Three computers" },
  "sources": {
    "parent": { "file": "recordings/parent.mp4" },
    "laptop": { "file": "recordings/laptop.mp4" },
    "browser": { "file": "recordings/browser.mp4" }
  },
  "tracks": {
    "parent": { "source": "parent", "start": 0 },
    "laptop": { "source": "laptop", "start": 0 },
    "browser": { "source": "browser", "start": 0 }
  },
  "timeline": [
    {
      "at": 0,
      "layout": "three-screens",
      "slots": { "left": "parent", "right-top": "laptop", "right-bottom": "browser" }
    },
    {
      "at": 2,
      "layout": "two-screens",
      "slots": { "left": "parent", "right": "laptop" },
      "transition": { "effect": "morph", "duration": 0.5 }
    },
    {
      "at": 4,
      "scene": "explain",
      "transition": { "effect": "fade", "duration": 0.5 }
    }
  ]
}
```

At two seconds the screens move without restarting. At four seconds the SVG
appears; the film ends at eight seconds. The default is to freeze the last frame
of any source that ends early. Audio is opt-in, so this presentation is silent.

## 4. Validate, preview and export

```bash
backstage render complete --check
backstage preview complete
backstage render complete
```

Preview renders first, then opens playback, seek and frame-step controls. Wait
for that initial render; Ctrl-C closes its temporary server. Inspect the final
frame to confirm that the animation finishes and the viewer can read the result.
The export is `exports/complete.mp4` with a companion `.facts.json`.

## 5. Customize and reuse

Change the template's CSS for background, borders, shadows and screen positions,
or set its `title`, `background`, `foreground` and `border` parameters. Render
again; the source videos stay unchanged.

For another edit, register a second presentation using the same sources. Declare
cuts and speeds in its tracks. Keep narration text and optional audio in the
scene, then select cues and their clocks in the presentation.

- [Scenes](scenes.md#visual-scenes-and-editorial-content): visual scenes, text and audio assets.
- [Presentations](presentations.md): cuts, synchronization, audio and captions.
- [Templates](templates.md): HTML slots and time-controlled animations.
- [CLI](cli.md#render): command flags and outputs.
