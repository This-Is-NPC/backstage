# Presentations

Reference for version 1 presentation JSON files. For a working sequence, follow
[Compose a presentation](how-to-compose-presentations.md). Project registration
belongs in [Configuration](configuration.md#declarative-presentations); scene
content belongs in [Scenes](scenes.md#visual-scenes-and-editorial-content).

## Timeline and tracks

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

## Audio

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

## Captions

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

## Output and validation

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

See [Templates](templates.md) for visual slots and animation timing.
