# Compose a presentation from existing recordings

A presentation lays out existing video files alongside HTML/SVG scenes. It does
not execute recording actions, hooks or VM commands. Change its layout, cuts,
captions or audio and render again without recording another take.

Requirements: Chromium (or Google Chrome), FFmpeg and ffprobe on PATH. The
renderer uses a temporary browser profile with the Chromium sandbox enabled.
It does not require Node.js. All template resources must be local.

## Try the example

From the repository root:

```bash
python3 examples/presentation/generate.py
go run ./cmd/backstage --project examples/presentation render complete --check
go run ./cmd/backstage --project examples/presentation render complete
go run ./cmd/backstage --project examples/presentation render short
go run ./cmd/backstage --project examples/presentation preview complete
```

The generated inputs use numbered frames and test tones, not recorded speech.
The complete example shows three screens, changes to two, then displays an
animated SVG. The short version accelerates the video and keeps its cue audio
at normal speed. Outputs are under the example's `exports/` directory.

Preview **renders first**, then opens the exact result with playback, seek and
frame-step controls. This first implementation has an initial rendering wait;
it is not a live editing interface. Ctrl-C closes the preview server and removes
its temporary files. No second approximation of the timeline is used.

## Register presentations in backstage.json

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

These fields extend the existing project configuration. `layouts` still controls
recording, while presentation layouts belong to templates. Omitting `template`
selects the built-in template. New presentations default to 1920×1080 and 30 fps
when the project has no render settings. An optional presentation `render`
overrides the project values.

All JSON paths are relative to the project root, including scene entries, audio
files and output overrides. HTML and CSS resources are relative to their own
files. Paths cannot escape the project through parent traversal or symlinks.

## Declare the timeline

A complete presentation document uses schema version 1:

```json
{
  "version": 1,
  "duration": 42,
  "parameters": { "title": "Three computers" },
  "sources": {
    "parent": { "scene": "parent" },
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
      "at": 15,
      "layout": "two-screens",
      "slots": { "left": "parent", "right": "laptop" },
      "transition": { "effect": "morph", "duration": 0.8 }
    },
    {
      "at": 30,
      "scene": "explain",
      "transition": { "effect": "fade", "duration": 0.5 }
    }
  ]
}
```

A source `scene` references `scenes/NAME.json` and defaults to the existing
`<record.out>/<scene.name>.mp4` (the filename supplies the name when omitted). Supply `file` alongside `scene` to use another take while
retaining its editorial content. It never re-records a missing file.

Tracks are instances of sources: use distinct IDs to place the same source in
multiple slots or play it at different positions. Each track can select cuts:

```json
{
  "source": "parent",
  "start": 2,
  "on-end": "freeze",
  "segments": [
    { "from": 4, "to": 10, "rate": 1 },
    { "from": 20, "to": 40, "rate": 4 },
    { "from": 4, "to": 10, "rate": 1 }
  ]
}
```

`from`/`to` are source seconds; `start` and event `at` are presentation seconds.
Segments play in their declared order, including repeats. Omitted segments mean
the entire source at normal speed; omitted `rate` means 1. Positive rates below
1 slow the video down. Source spans must exist in the input file.

A hidden track continues advancing. A layout change never restarts it. Before
its start it is absent; after its end it freezes by default, or disappears with
`on-end: "hide"`. The explicit presentation duration determines export length.
Supported output dimensions are even values from 2 to 8192; fps is an integer
from 1 to 120, and duration is at most 24 hours. Output timing is quantized to
frames.

Events start at zero and have strictly increasing times. Each persists until
the next event. Transitions start at the destination event's `at`, must finish
before the next event and cannot be used on the first event. `morph` moves and
resizes retained tracks between layouts; `fade` also supports visual scenes.

## Keep narration text in the scene

Existing recording scenes default to `type: "recording"`. Add editorial content
without changing recording actions:

```json
{
  "narration": {
    "language": "pt-BR",
    "cues": [
      {
        "id": "rule",
        "start": 4,
        "end": 10,
        "text": "Cada computador aplica a regra localmente.",
        "audio": "assets/audio/rule.wav"
      }
    ]
  }
}
```

This is a block to add to a scene, not a complete recording scene. Text and cue
IDs belong to the scene; presentations reference them. `audio` is optional.
There is no speech provider or automatic transcription yet. Cue times refer to
the original take, not recording action delays, and must be authored explicitly.

A visual scene requires no recording configuration:

```json
{
  "name": "explain",
  "type": "visual",
  "entry": "slides/explain.html",
  "duration": 12,
  "narration": {
    "language": "pt-BR",
    "cues": [
      { "id": "concept", "start": 0, "end": 8, "text": "Uma regra, vários computadores." }
    ]
  }
}
```

Its cue times are local to that visual scene. Its declared duration must cover
the timeline event that uses it. Use it through `render` or `preview`; recording
commands reject visual scenes. There is no separate `slides` registry.

## Select audio per use

Scenes can also own reusable non-speech files with, for example,
`"audio": {"ambience": {"file": "assets/room.wav"}}`. Select one in a presentation
with `"scene": "parent", "asset": "ambience"`; its timing is still chosen per use.
A scene asset following video uses source time zero as its origin.

Audio is opt-in. Add an `audio` array to the presentation. Each item needs a
unique `id` and selects a `file`, a scene `cue`, or a track's original sound.

```json
[
  { "id": "voice", "track": "parent", "cue": "rule", "mode": "follow-video" },
  { "id": "music", "file": "assets/music.wav", "at": 0, "loop": true,
    "volume": 0.15, "fade-in": 1, "fade-out": 2 }
]
```

`follow-video` needs a track and follows its cuts and speeds, preserving pitch.
A cue's audio file starts at that cue's source `start`; only intersections of
the file, cue interval and selected video segments are used. A plain `track`
selection uses the source video's own audio. A file plus track is also accepted;
in this case file time zero corresponds to source video time zero.

For speech at normal speed while the video accelerates:

```json
{
  "id": "voice",
  "scene": "parent",
  "cue": "rule",
  "mode": "independent",
  "at": 3,
  "from": 0,
  "rate": 1,
  "volume": 1
}
```

Independent audio has its own `at`, source `from`/`to` and `rate`. Defaults are
independent mode, source start zero, end of file, rate 1 and volume 1. Fades use
seconds of the resulting audio. `mute` disables a selected item. `loop` repeats
the selected interval through the end of the presentation and defaults to false.

Independent speech can extend beyond its video segment but cannot exceed the
presentation unless `trim-end: true` explicitly allows truncation. Following
video uses the video's segments instead of independent timing or looping.
Mixing produces stereo 48 kHz audio with a limiter to prevent clipping.

## Place captions without copying text

Add a `captions` array referencing cue IDs:

```json
[
  { "track": "parent", "cue": "rule", "clock": "source", "slot": "subtitle" },
  { "scene": "explain", "cue": "concept", "clock": "presentation",
    "at": 30, "duration": 8, "slot": "subtitle" }
]
```

- `source` follows the selected track's source cuts and speeds, repeating or
  splitting cues where the selected segments repeat or cut them.
- `presentation` uses explicit `at` and positive `duration` in the final timeline.
- `audio` uses `audio: "ID"` to follow that selected audio item's resulting spans.

For audio-clock captions, also reference the cue through `scene` or `track`.
Caption slots belong to the active template/visual scene. Multiple simultaneous
cues in one slot are displayed on separate lines; use separate slots for
independent positions. Source cues beyond the final duration are clipped.

## Customize the HTML

```bash
backstage template init household
```

This creates editable `template.html` and an example `visual.html` under
`templates/household/`, refusing an existing directory. Register the template
in `backstage.json` and reference the visual entry from a visual scene.

A template declares this contract in its HTML:

```javascript
window.backstageTemplate = {
  version: 1,
  layouts: { single: ["center"] },
  captionSlots: ["subtitle"]
};
window.render = async function (context) {
  // context.time: global seconds; context.localTime: seconds since this event.
  // context.layout, context.parameters, context.narration are also available.
  // Set styles and SVG attributes as a function of these values.
};
```

Provide elements with `data-slot="center"` and
`data-caption-slot="subtitle"`. `render(context)` must create the slots for its
layout before resolving. Set `data-fit="cover"` to crop a video to its slot;
`contain` preserves its entire screen. The built-in template provides `single`
(`center`), `two-screens` (`left`, `right`) and `three-screens` (`left`,
`right-top`, `right-bottom`).

Slots describe axis-aligned screen rectangles. Their border, background,
rounded corners and shadow are carried with the rendered screen; CSS controls
the rest of the page. The runtime replaces slot pixels with prepared video
frames. Arbitrary 3D transforms and masks on video slots are not supported.
Caption appearance uses the slot's font, text alignment, color and background.

The built-in parameters are `title`, `background`, `foreground` and `border`.
Custom templates may interpret additional JSON parameters. Visual scenes use
`version: 1`, a `render` function and caption slots but need no layout manifest.

The runtime waits for images and fonts, pauses CSS/Web Animations and evaluates
their current time, and sets SVG animation time explicitly. Custom JavaScript
must derive state from `context`; wall-clock timers, random values and external
network data cannot produce repeatable frames. Load assets locally. Fonts,
images, CSS and scripts may be separate files.

## Outputs and validation

`render --check` probes inputs and checks timing, template initialization and
slots without decoding every video frame. Runtime errors can still occur later
inside custom code; these abort the export. The final MP4 is replaced only when
rendering and encoding succeed. A companion `.facts.json` records configuration,
resolved dimensions, source/resource hashes and tool versions.

Visual reproducibility assumes fixed inputs, browser, fonts and tool versions;
MP4 byte identity across environments is not promised. The renderer uses bounded
frame buffers and lossless intermediate video on disk rather than storing a
whole take as PNGs. Disk use can still be significant for long/high-resolution
sources. Ctrl-C cancels work and removes intermediates.

For integration tests, generate the example inputs and run:

```bash
BACKSTAGE_RENDER_TEST=1 go test ./internal/presentation -v -count=1
```

The quality gate builds its vulnerability scanner with Go 1.26.8 to avoid the
Go 1.25 `go/types` panic when analyzing chromedp's JSON dependency. The project
still targets Go 1.25.13. The first scan downloads that scanner toolchain; the
source-level vulnerability check is retained.
