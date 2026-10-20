# Native Declarative Videos

Use `composition` to author graphics, interface states and animation as JSON.
Backstage supplies the renderer. The project needs no HTML entry, custom
JavaScript, React, Remotion, Node.js build step or recording. Existing video,
selected audio and captions can be combined through a normal presentation;
see [presentations.md](presentations.md) for those controls.

## A Complete Project

Create these three files. `backstage.json` registers the output:

```json
{
  "render": {"w": 1280, "h": 720, "fps": 30},
  "presentations": {
    "launch": {"file": "presentations/launch.json", "out": "exports/launch.mp4"}
  }
}
```

`presentations/launch.json` selects the visual scene. Sources and tracks are
optional when the film contains only generated visuals:

```json
{
  "version": 1,
  "duration": 4,
  "timeline": [{"at": 0, "scene": "intro"}]
}
```

`scenes/intro.json` contains the elements and motion:

```json
{
  "type": "visual",
  "duration": 4,
  "composition": {
    "width": 1280,
    "height": 720,
    "nodes": [{
      "style": {
        "position": "absolute", "inset": "0",
        "background": "#11111a", "color": "#e4e8f7",
        "fontFamily": "sans-serif"
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
        "text": "A simpler workflow.",
        "start": 1,
        "end": 3,
        "style": {
          "position": "absolute", "left": "80px", "top": "340px",
          "fontSize": "32px"
        }
      }, {
        "text": "Available today.",
        "start": 3,
        "style": {
          "position": "absolute", "left": "80px", "top": "340px",
          "fontSize": "32px"
        }
      }]
    }]
  }
}
```

Use `render`, not `play` or `rehearse`. Visual scenes require a positive
`duration` and exactly one of `composition` or `entry`. They cannot declare
recording steps, VM or recording layout configuration. Presentation duration
sets the movie's end; scene duration only provides available content.

## Elements, Timing And Assets

| Field | Meaning |
| --- | --- |
| `composition.width`, `height` | Positive design dimensions, uniformly scaled to fit the output |
| `composition.stylesheets` | Optional project-relative local CSS files |
| `composition.nodes` | Nonempty list of root elements |
| Node `tag` | Supported HTML/SVG element; defaults to `div` |
| Node `text` or `children` | Text content or nested nodes, mutually exclusive |
| Node `attributes` | String values, such as `class`, `id`, `viewBox` or image `src` |
| Node `style` | String CSS values; camelCase, CSS property names and `--variables` are supported |
| Node `start`, `end` | Visibility interval in scene seconds; start inclusive, end exclusive |
| Node `animate` | One animation per property, with at least two strictly increasing keyframes |

All nested times use the scene clock, not the parent's start. A scene placed
at presentation second 10 with a node starting at 1 shows that node at second 11.
A parent starting at 1 does not reset its children's clocks. Hidden nodes occupy
no layout space. An omitted `start` is zero; omit `end` to keep a node until the
scene ends. Keep intervals and keyframes within the scene duration.

Keyframe `value` is a string CSS value. `ease` belongs to the interval starting
at that keyframe and defaults to `linear`. Supported easing includes `ease`,
`ease-in`, `ease-out`, `ease-in-out`, `step-start`, `step-end`,
`steps(N, start)` / `steps(N, end)` and `cubic-bezier(...)`. Values hold before
the first and after the last keyframe. Use step easing for discrete states or
sampled integer positions. Backstage seeks paused animations to the scene clock.
Do not add wall-clock timers or an imperative frame callback.

Use `span`, `div`, headings, paragraphs and supported table elements for UI;
use `svg`, `g`, `path`, `rect`, `circle`, `text` and `tspan` for vectors.
`{"tag":"#text","text":" words "}` preserves mixed text among children;
text nodes only accept text. Scripts, embedded documents and event handlers
are rejected; link attributes may only reference fragments such as `#label`.

For an image, use `{"tag":"img","attributes":{"src":"assets/logo.png"}}`.
CSS files can load local fonts with `@font-face`; CSS resource URLs are relative
to that CSS file. JSON resource paths are project-relative and must remain
inside the workspace. Keep images in `src`, and fonts/background resources in
stylesheets, rather than assuming inline relative CSS URLs resolve from the
scene file. Set the background and typography explicitly. Changing output
aspect uniformly scales a composition; it does not rearrange its elements.

## Convert An Existing Video

For a requested fully declarative conversion, inspect the reference and source
when available: identify scene boundaries, overlaps, copy, layouts, UI states,
fonts, logos and interactions. Convert source frames to scene seconds using the
source fps. Preserve intentional overlaps with explicit visibility and opacity;
use native timeline transitions when their behavior matches the reference.

Rebuild the elements and state changes as nodes and keyframes. Static DOM from
an existing interface can be converted once into scene data; it must no longer
need the application's component tree to render. Such data represents authored
UI states, while recording scenes execute an actual workflow. Download or copy
permitted assets locally and freeze any date-dependent displayed content.

When extracting styles from an application, include rules generated through
CSSOM, not only style-tag text: calendars and component libraries can otherwise
lose their layout. Keep the styles the video uses, and remove incidental UI
transitions when motion is already defined by JSON keyframes.

Inspecting or extracting source assets may need source-project tools once.
The delivered native project should render independently of those tools.
Wrapping the original composition in an HTML entry or frame-clock adapter,
exporting it back to React, or using the original MP4 as the sole content does
not validate a conversion of its elements and animations to native JSON.
Backstage has no automatic Remotion-import command; do not invent one.

## Validate And Deliver

Use a binary that includes native `composition` support. A newer skill does
not update the executable. If working from the Backstage checkout, `make build`
produces `bin/backstage`; use that binary explicitly when the installed command
is older. Pass `--project /path/to/project` when not running inside the project.

```bash
backstage render launch --check
backstage render launch
ffprobe -v error -select_streams v:0 -count_frames \
  -show_entries stream=width,height,r_frame_rate,nb_read_frames,duration \
  -of json exports/launch.mp4
```

The example produces 120 frames at 1280×720, 30 fps, lasting 4 seconds. `--check`
validates the plan and resources; it does not inspect every animation frame.
Review the exported MP4 at entrances, state changes, transition boundaries and
the final frame. For a conversion, compare those moments with the reference.
Use preview controls to seek forward and backward; verify that the same moment
reconstructs the same state. `lint` can check built-in editorial graphics but
cannot establish that arbitrary composition text fits or matches the reference.

Confirm that the delivered project renders without the original application's
bundle. Export facts list the actual inputs: a native conversion should have
its JSON and local assets, not a project-specific JavaScript adapter or the
reference MP4. Embedded `builtin:` renderer entries are expected. Report the
absolute MP4 path and the scene JSON path, plus what was actually validated.
