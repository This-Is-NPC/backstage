# Create a declarative video

**The task:** render an animated title and a message from native JSON elements,
without a recording, HTML entry, React component or project-specific script.
Rendering needs Chromium, FFmpeg and ffprobe. Use a Backstage build with native
`composition` support; from this checkout, `make build` produces `bin/backstage`.

## 1. Register the presentation

Create `backstage.json` in your project:

```json
{
  "render": {"w": 1280, "h": 720, "fps": 30},
  "presentations": {
    "intro": {"file": "presentations/intro.json", "out": "exports/intro.mp4"}
  }
}
```

Create `presentations/intro.json`:

```json
{
  "version": 1,
  "duration": 4,
  "timeline": [{"at": 0, "scene": "title"}]
}
```

There are no sources or tracks because this presentation uses generated visuals.
They remain available when you want to combine graphics and recorded media.

## 2. Describe the visual scene

Create `scenes/title.json`:

```json
{
  "type": "visual",
  "duration": 4,
  "composition": {
    "width": 1280,
    "height": 720,
    "nodes": [{
      "style": {
        "position": "absolute", "inset": "0", "background": "#11111a",
        "color": "#e4e8f7", "fontFamily": "sans-serif"
      },
      "children": [{
        "tag": "h1",
        "text": "Meet the update",
        "style": {
          "position": "absolute", "left": "80px", "top": "180px",
          "margin": "0", "fontSize": "64px"
        },
        "animate": [{
          "property": "opacity",
          "keyframes": [
            {"at": 0, "value": "0", "ease": "ease-out"},
            {"at": 0.5, "value": "1"}
          ]
        }, {
          "property": "transform",
          "keyframes": [
            {"at": 0, "value": "translateY(24px)", "ease": "ease-out"},
            {"at": 0.5, "value": "translateY(0px)"}
          ]
        }]
      }, {
        "text": "Elements, timing and motion are scene data.",
        "start": 0.5,
        "style": {
          "position": "absolute", "left": "80px", "top": "340px",
          "fontSize": "32px"
        }
      }]
    }]
  }
}
```

A visual scene selects exactly one of `composition` or a custom HTML `entry`.
The composition's design coordinates scale uniformly to the output dimensions.
Each node can use a supported HTML/SVG tag, text or children, string attributes,
CSS styles, visibility intervals and animated properties.

All `start`, `end` and keyframe `at` values use scene seconds, including nested
nodes. A parent's start does not reset its children's clocks. Visibility is
start-inclusive and end-exclusive; omit `end` to keep a node until the scene
ends. Keyframes increase strictly, hold before/after their range, and use the
`ease` on the interval starting at that keyframe. Presentation duration sets
the movie's end; scene duration only provides available content.

## 3. Validate, render and review

Run from the project directory, or add `--project /path/to/project`:

```bash
backstage render intro --check
backstage render intro
ffprobe -v error -select_streams v:0 -count_frames \
  -show_entries stream=width,height,r_frame_rate,nb_read_frames,duration \
  -of json exports/intro.mp4
```

The output has 120 frames, 1280×720, 30 fps and a four-second duration. Inspect
the entrance, the message at 0.5 seconds and the final frame. Preview controls
let you seek forward and backward to confirm that the same time reconstructs
the same state. `--check` validates resources and the plan, not every frame.
`lint` checks built-in editorial graphics; arbitrary composition text still
needs visual review.

## 4. Add assets and other content

Use project-relative `composition.stylesheets` for local CSS, and `img` nodes
with `attributes.src` for local images. Font and image URLs inside a stylesheet
are relative to that CSS file. Files stay inside the workspace. Native rendering
needs no build step for those assets. Set node typography and styles explicitly;
presentation themes control the editorial cards and captions.

For ordinary release titles and CTAs, use [cards and release structure](presentations.md#editorial-graphics-and-recorded-timing).
For recorded demos, add tracks, cameras and evidence-based action overlays.
Selected audio and narration cues use the same presentation mixer. Changing
`--aspect` scales a native composition; author another layout if elements need
to rearrange for portrait output.

When converting a reference video, move its elements, state changes and motion
into JSON. Rendering the original React composition through an adapter, or
using the original movie as the sole input, does not validate that conversion.
Keep reference media separate from rendering inputs and inspect the exported
facts to confirm the actual dependencies. The optional React export is an
editable bridge, not an automatic Remotion importer.

See [JSON compositions](presentations.md#json-compositions) for field semantics
and [the runnable examples](../examples/visual/README.md) for other visual features.
