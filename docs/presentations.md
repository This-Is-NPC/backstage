# Presentations

Reference for version 1 presentation JSON files. For a working sequence, follow
[Create a declarative video](how-to-create-a-declarative-video.md) or
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

A source `scene` references `scenes/NAME.json` and the last successful take
of that scene (the filename supplies the name when omitted). The documented
`<record.out>/<scene.name>.mp4` path is a projection of that take; a `scene`
source reads the published generation and holds it for the whole render.
Supply `file` alongside `scene` to use another file while retaining editorial
content. A `file` source follows the projection, so an existing
`recordings/NAME.mp4` reference still tracks new successful takes. A failed
take never replaces that path. It never re-records a missing file.

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

When a presentation or its template is inherited from an ancestor
`backstage.json`, relative references inside the template (CSS, scripts, fonts,
images) keep working as long as they stay inside the workspace. Requests that
leave the workspace, including through symlinks, are refused. Companion facts
record input hashes under workspace-relative keys; without `extends` those keys
match today's project-relative paths.

`render --check` probes inputs and checks timing, template initialization and
slots without decoding every video frame. Runtime errors can still occur later
inside custom code; these abort the export. The final MP4 is replaced only when
rendering and encoding succeed. A companion `.facts.json` records configuration,
resolved dimensions, source/resource hashes and tool versions. A successful
export also writes a `timings` object beside `render`: phase seconds
(`renderer-start-seconds`, `decoder-start-seconds`, `prepare-track-seconds`, `audio-seconds`,
`audio-part-seconds`, `encode-seconds`, `concat-seconds`, `mux-seconds`, `metadata-seconds`,
`total-seconds`), `workers`, per-chunk wall times (`chunk-seconds`), per-frame
stages (`decode`, `transfer`, `draw`, `screenshot`,
`encode-write`, `probe`) with total, mean, max, p95 and frame count
(`probe` is the Go round-trip of the geometry check, like decode),
`layered-chunks`, `composite-seconds`, `static-violations` (omitted when
zero), and intermediate
byte sizes (`track-bytes`, `audio-part-bytes`, `mix-wav-bytes`,
`video-mp4-bytes`, `final-mp4-bytes`, `decoded-png-bytes`, `screenshot-bytes`)
and cache counters (`cache-hits`, `cache-misses`, each `{tracks, audio, segments}`).
Absent phases and missing files are omitted. `renderer-start-seconds` is the
sum of Chromium startups across the worker pool (one browser per worker);
`decoder-start-seconds` sums decoder opens on cache misses; `encode-seconds` is
the longest frames-path encoder Start to Wait (image2pipe chunks). Layered
overlay time is `composite-seconds` only. `concat-seconds` is omitted when there is
only one chunk. Progress ends with
`>> render timings: ...` including `decoder-start=`, `workers=`, `concat=`,
`probe-p95=`, `composite=` and `layered=`.
A still chunk may print `>> chunk-N layered` after `running` and encode from
two layer stills plus the prepared tracks instead of a screenshot per frame.
A template that declares `static` for the event's layout (or `static: true` on
a visual scene) probes the first and last frame of each chunk and keeps one
below still and one above still on that worker, recapturing when the event,
geometry, or caption set changes. A broken static promise prints
`>> chunk-N static template <layout>: <reason>; using frames` or
`>> chunk-N static scene <name>: <reason>; using frames`
(`css animation`, `smil`, or `geometry`) and facts record `static-violations`
(omitted when zero). The timings line includes `static-violations=` only
when the count is positive. A geometry change that comes and goes between
the two samples stays under the static promise.
Layered eligibility needs constant slot geometry (integer or fractional)
and radii whose computed value is a single `px` token. Percent radii and scroll
that changes per frame use screenshots. A fade between two events declared
`static`, with no slotted tracks or visible captions, captures each event once
and blends the stills in FFmpeg. Stable push and wipe events use `xfade` under
the same constraints and with frame-aligned timing. Other transition chunks use
screenshots.
On both paths the track image sits in an
integer-pixel rectangle inside the slot (`contain`/`cover`/`fill`; blend and
morph still use `object-fit`).
`decoder-start-seconds`
covers opening the decoders; when a preview starts after frame 0 it also
includes the first-frame peek and any packet count plus reopen. Facts `inputs`
list plan entries plus files loaded by events that fall inside the rendered
interval (template-fetched assets included). A second render of the same
interval reuses encoded chunks; progress prints `>> chunk-N cached` on a hit.
Changing an event's template file, layout, slots, narration, slotted tracks,
or a file that event loaded invalidates that event's chunks and the incoming
transition chunk of the next event. A global parameter change invalidates every
chunk. Preview uses the same cache: `first`, `end` and `scale` are part of the
key, so a scale-1 preview that covers whole chunks of a prior full export is a
hit. Source and template input hashes stay the same;
`builtin:runtime.html` changes when the host runtime changes.
Measured draw includes what `draw` itself waits for — image load, host fonts,
and a compositor paint — not pure canvas cost. `encode-seconds` overlaps that
chunk's Chromium frame loop; it does not mean the encoder is the bottleneck. On a typical
`complete` render the loop itself spends about 4.3 s in screenshot and
3.7 s in draw of about 10 s total. Layered chunks skip that per-frame loop after
a geometry probe; `composite-seconds` is the FFmpeg overlay encode and is not
included in `encode-seconds`. A layered chunk records two screenshot samples
unless a static event reuses a still from an earlier chunk on that worker.
Animated overlays record one transparent screenshot per output frame while
video stays in FFmpeg, counted by `animated-overlay-frames`.

Visual reproducibility assumes fixed inputs, browser, fonts and tool versions;
MP4 byte identity across environments is not promised. Prepared FFV1 tracks use
`-g 1` so every frame is a seek point. Screenshots stay lossless PNG with
`optimizeForSpeed`. Track prepare runs in parallel (at most one worker per
CPU). Prepared tracks, the mixed soundtrack, and encoded timeline chunks are cached
under the user cache directory (`backstage/render`). A warm render reuses those
files when the source bytes, compiled cuts, fps, FFmpeg version, encode recipe,
browser version and embedded runtime match. Thread counts are not part of the
key. Warm and cold renders keep the same `inputs` hashes and the same composed
frames; a full segment-cache hit copies the stored H.264 without Chromium.
`backstage cache prune` drops least-recently-used track, audio and segment
entries (default 10G).
Project `render.threads` sets FFmpeg counts for that prepare and for
the chunk encoder (`render.encoder`: `libx264`, or `vaapi` for the GPU);
each chunk encoder uses `max(1, encode/workers)` threads and runs at
the same time as that chunk's Chromium frame loop, not after it.
`render.workers` is Chromium concurrency (one browser per worker). `0`,
`null` or an omitted key picks `max(1, min(NumCPU/2, memoryBudget/2GiB,
ceil(missFrames/max(16, 2*fps))))` where `memoryBudget` is MemAvailable minus
a 2 GiB host reserve and `missFrames` counts frames in chunks that miss the
segment cache. An explicit `N` runs `N` workers, capped at the number of those
chunks. Each worker takes a contiguous run of missed chunks, split so the
largest frame total is as small as possible. Browsers start while tracks are
prepared, and the mix runs alongside the chunks. Chunks are cut at event boundaries and into pieces of at
most `max(16, 2*fps)` frames, measured from the event start. Transition ends
and caption visibility changes split those pieces further, without leaving
a piece shorter than 3 frames, before they are clipped to the render interval, so a preview that covers whole interior pieces shares
those cache keys with a full export. Chunks share prepared tracks and the mix,
encode H.264 with `-video_track_timescale <fps>`, and join with the concat
demuxer `-c copy` when there is more than one file. A worker keeps its browser
and track decoders across consecutive misses; each miss still starts its own
encoder. Joined output keeps PSNR-Y ≥ 40 dB (mean ≥ 45 dB)
against a single-worker export, frame codes and 1/fps timestamps, and audio
cross-correlation ≥ 0.95 with `|lag| ≤ 5 ms`. A faster encode is accepted when
every decoded H.264 frame keeps PSNR-Y ≥ 40 dB against the previous thread
count (mean ≥ 45 dB), and when frame count, timestamps, duration and audio stay
aligned. The same PSNR-Y limits apply when a preview interval is compared to
the matching frames of the full export (`select='between(n,first,end-1)'`,
never `-ss` on H.264).
`preview --from/--to/--scale` renders `[first, end)` at absolute `t = n/fps`
and may screenshot at a clip scale; `render` stays the full film at scale 1.
Interval audio is a sample-accurate `atrim` of the cached 48 kHz mix
(`end_sample` exclusive). The player clock is `first/fps + currentTime`.
The renderer uses
bounded frame buffers and lossless intermediate video on disk rather than
storing a whole take as PNGs. Disk use can still be significant for
long/high-resolution sources. Ctrl-C cancels every chunk, removes
intermediates, and leaves an existing MP4 in place. The chunk that returns
the error prints `failed`; chunks cancelled because of it or because of
Ctrl-C print `interrupted`.

See [Templates](templates.md) for visual slots and animation timing.
## Editorial graphics and recorded timing

Recording facts include `steps`, with each action's label, resolved target,
start/end seconds and key commands. The take manifest points to this facts file;
the timeline is stored once and remains attached to the published generation.
Actions without labels are recorded but cannot be addressed by name.

Timeline, independent audio and presentation-clock caption `at` values accept
seconds or an anchor:

```json
{"at": {"step": "open-settings", "track": "demo", "edge": "end", "offset": 0.5}}
```

Use `cue` instead of `step` for narration cues. `edge` defaults to `start`.
An omitted `track` selects the sole matching track; `scene` can narrow that
selection. The anchor follows source cuts and playback rates. Offset is applied
in presentation seconds. Missing timings, excluded steps, repeated occurrences
and ambiguous tracks fail compilation. Old takes need to be recorded again to
provide step timings. A cue anchor still works from the scene's narration.

An optional `theme` sets `font`, hexadecimal `background`, `foreground` and
`accent`, pixel `min-font`, `title-size`, `text-size`, `caption-size`,
`seconds-per-word`, `cta-duration`, and `ease`. Defaults are system sans-serif,
26 px minimum, 72/36/32 px sizes, 0.3 seconds per word, 3.5 readable seconds for
a CTA and cubic `ease-out`. Other easings are `linear` and `ease-in-out`.

Events can declare a `card`, alone or over a layout or visual scene:

```json
{"at": 0, "card": {"type": "statement", "title": "Repeated manual editing", "strike": true}}
```

Card types are `title`, `statement`, `lower-third`, `badge` and `cta`. Fields
are `title`, `subtitle`, `text`, project-relative `logo`, `url`, `strike` (only
statements), and `entrance`. Cards require sufficient reading time after their
entrance. An entrance has `effect`, `duration` and `ease`; effects are `rise`,
`fade`, `type`, `scale` and `none`, with a default duration of 0.35 seconds.
Captions also accept `entrance` and pixel `font-size`. A declared theme or
caption animation enables minimum font and reading-time validation for captions.
Unstyled captions retain the template's slot typography.

All motion uses render time, including previews and seeks. With stable chrome
and video slots, independent moving graphics use an animated alpha layer.
Other motion uses Chromium frames; stable intervals use `layered` when eligible.
Custom template `render(context)` receives `theme` and the shared `motion`
functions. See [`examples/visual`](../examples/visual/cards.json) for all five
card types without recording or a custom HTML template.

Measure a fixed 60-second variant with fresh caches using
`python3 examples/visual/benchmark.py /path/to/backstage` from the repository
root. The script reuses the example's card definitions and reports three runs
and their median from render facts. On the development machine, the 1280×720,
30 fps example rendered 1,800 frames in 6.247, 5.901 and 5.435 seconds, with
32 layered chunks each time. These measurements cover the five card types and
their entrances together; they do not measure a camera or video-slot animation.
## Transition effects

`transition.effect` accepts `fade`, `morph`, `slide`, `push`, `wipe`, `zoom`
and `blur`. Slide, push and wipe accept `direction`: `left` (default), `right`,
`up` or `down`, describing the direction of motion. Slide moves the incoming
event over the outgoing event; push moves both. Wipe reveals the incoming event
from the opposite edge. The transition applies to HTML, video slots, captions
and cards together. Motion is linear over the declared duration.

Static fade, push and wipe events without video slots or visible captions use
FFmpeg composition when their HTML and graphics are stable. Push and wipe use
`xfade`. Slide, zoom, blur, morph and dynamic content use Chromium frames.
Transitions preserve their progress when a preview starts midway through them.

## Narration, music and sound effects

Set `audio[].role` to `music`, `narration` or `effect`. Cue audio is narration
by default. Music with `"duck": true` is compressed automatically by audible
narration, with a 20 ms attack and 250 ms release. Music keeps its configured
volume outside speech. Multiple voice clips share one narration bus; effects
do not trigger ducking. Without narration, music plays normally.

An event's `"sound": {"file": "audio/reveal.wav", "volume": 0.4}` starts at
the event boundary, including a transition or card entrance. For a sound tied
to a recorded action, use ordinary effect audio with an `at` step reference.
All effects use the existing audio mixer, sample timing, preview trimming and
cache. Sound effects extending past the presentation end are trimmed.

### Source camera

A track's `camera.keyframes` crops its source video inside the assigned slot.
Each `region` uses normalized source coordinates (`x`, `y`, `w`, `h` in 0..1),
with a maximum 16× zoom. Keyframe `at` uses the presentation clock and also
accepts step/cue references. The first region takes effect at its own time;
the source remains full frame before it. `ease` defaults to `cut`: the previous
region holds until the next keyframe. Explicit `linear`, `ease-out` or
`ease-in-out` interpolates from the previous keyframe to this keyframe.
Moving regions must preserve their aspect ratio; cuts can change it.

```json
"camera": {"keyframes": [
  {"at": 0, "region": {"x": 0, "y": 0, "w": 1, "h": 1}},
  {"at": 2, "region": {"x": 0.25, "y": 0.25, "w": 0.5, "h": 0.5}},
  {"at": 4, "region": {"x": 0.5, "y": 0.5, "w": 0.5, "h": 0.5}, "ease": "ease-out"}
]}
```

An event can override cameras by track ID:
`"camera": {"demo": {"keyframes": [...]}}`. An empty keyframe list resets
that event to the full source. Slot borders, cards and captions remain at their
original size. Smooth cameras use FFmpeg zoom/pan expressions on the layered
path and the same render clock in Chromium. Transitions and chunks containing
an aspect-changing cut use frames when a stable image mask is unavailable.
Frozen tracks still support camera movement after the source video ends.
Zooms above 10× use frames because FFmpeg clamps its zoom/pan filter at 10×.

`"camera": "follow"` builds cut keyframes from recorded step regions, mapped
through the track's cuts and rates. A keyframe can also use `"step": "open"`
in place of `region`. These require actual normalized `region` metadata in the
take's step facts and fail clearly when it is missing. Coordinates are never
inferred from the step's name or target.

With `record.observations: true`, a host Ghostty stage records the resolved
target pane from tmux's cell positions and pixel sizes. The stage uses a known
zero-padding content origin; monitor scale and status-line placement are applied
before normalizing coordinates. When those metrics are unavailable or the
viewport cannot be matched, the observed window region is retained. Guest
observations use the active window. Rotation, reflection and DPI scale are
mapped to the selected recorder's output coordinates.

See `examples/visual/camera.json`. Put a recording at `examples/visual/demo.mp4`
(or generate an eight-second test fixture with FFmpeg), then run
`backstage --project examples/visual render camera`.

### Visible actions

`actions` selects evidence from a track's step facts. It follows the source
cuts and playback rates, including repeated source segments:

```json
"actions": [{
  "track": "demo",
  "keys": true,
  "highlight": "outline",
  "cursor": true
}]
```

`step` optionally selects one labeled action. `keys` shows the recorded key
sequence or typed input for the action, using theme typography. `highlight`
accepts `outline` or `spotlight` and uses the recorded source region. An explicit
normalized `region` can highlight a particular field instead of its window.
`duration` optionally overrides the mapped action interval, in presentation
seconds. `sound` accepts the same file and volume fields as event sounds.

Cursor highlights use recorded normalized pointer coordinates; a recorded
`click: true` adds a 350 ms pulse. Missing pointer observations disable the
cursor effect. Selecting all steps skips missing regions; selecting a particular
step with a highlight requires either a recorded or explicit region. Invalid
recorded coordinates fail validation. No position or click is inferred from
key names, text or targets. The built-in observer records a `pointer-trace`
array once per take, containing recording-clock `at` times and normalized
`pointer` positions, or `null` for gaps. Real mouse presses set `click: true`
when the account can read its pointer devices. Keyboard events are not stored.
The renderer interpolates adjacent positions at most 120 ms apart, hides gaps,
and maps the trace through cuts, repeats and playback rates. `step` limits a
trace to that action's original interval; `duration` affects action labels and
highlights. Facts without a trace retain the action-boundary pointer behavior.

Overlays stay inside their source slot and follow its camera crop. Stable
keystrokes, outlines, spotlights and pointer markers use layered composition.
A click pulse or an overlay that follows a moving camera uses an animated
alpha layer when the template and camera support layered composition, and
frames otherwise. `examples/visual/actions.json` expects an original `demo.mp4` and its
`demo.facts.json` sidecar containing recorded steps.

### Optional Remotion bridge

Run `backstage export-remotion NAME --out exports/react` to create a portable
Remotion project. `--aspect` accepts the same three aspect ratios as rendering.
The destination must be new; failed or cancelled exports remove their temporary
files. Published take generations remain protected.

The export contains original takes and facts, prepared lossless H.264 tracks,
mixed audio, and `backstage.json` version 1. Step and narration cue intervals
are mapped through the selected cuts and playback rates, including repeats.
Original source intervals and rates remain available beside presentation times.
Preparation and audio mixing reuse the native pipeline. Shared originals and
facts are copied once. No Node dependency is added to ordinary recording or
rendering.

Inside the export, run `npm install`, then `npm run studio` or `npm run render`.
Edit `stage.jsx` to render actual React application components using its active
`steps`, `cues`, `event` and `time` props. The sample displays video slots, basic
cards, cues and keys. Existing HTML templates, visual scenes, transitions,
camera crops and action graphics are metadata for the React integration, not
automatically reproduced by that sample. Use the native renderer to retain
those visuals. See the generated README for the bundle contract.

### Release structure and aspect ratios

`release` builds the recurring statement → title → recorded demos → CTA
structure without a handwritten timeline. Use it instead of `timeline`.
`statements` is a list of short pain statements, `title` and `cta` use ordinary
card fields, and `demos` selects existing tracks:

```json
"release": {
  "statements": ["Too many clicks"],
  "title": {"title": "Meet the update", "subtitle": "A simpler workflow"},
  "demos": [{"track": "demo", "duration": 8, "title": "See it work"}],
  "cta": {"title": "Try it today", "url": "example.com"}
}
```

Card durations derive from theme reading times, entrance motion and an
eight-frame fade. Demos start their tracks at their own cut; use separate track
instances for multiple demos of the same source. Track cuts, camera anchors,
audio and action overlays use those starts. The CTA fills the remaining
presentation duration. A total duration that cannot fit the content and CTA is
an error. The expanded timeline uses the ordinary graphics and render caches.

`render`, `preview` and `lint` accept `--aspect 16:9`, `--aspect 9:16` or
`--aspect 1:1`. Source coordinates and timing stay unchanged. Rectangle outputs
retain the original long edge; a square uses its short edge. Dimensions are
even. The built-in release layout uses `cover` for portrait and square output,
so camera regions or recorded follow regions determine the visible UI; wide
output contains the source. Other templates keep their own responsive CSS.

```bash
backstage --project examples/visual render release --aspect 16:9 --out exports/release-wide.mp4
backstage --project examples/visual render release --aspect 9:16 --out exports/release-vertical.mp4
backstage --project examples/visual render release --aspect 1:1 --out exports/release-square.mp4
```

See `examples/visual/release.json`. Use `lint` with the same aspect before an
export to check its typography and reading time.

### Animated graphics composition

With video slots, when a template declares stable chrome and slot geometry,
moving editorial
cards, captions and action overlays are captured once per frame into a
transparent PNG layer for that chunk. FFmpeg composes that sequence over the
prepared video tracks. Video frames stay out of Chromium. Stable chunks keep
the existing two-still path. Template animation, changing slot geometry,
transitions and unsupported camera chunks retain the frames path.

The temporary sequence is removed after composition or cancellation. Segment
caching and preview timing use the same keys and clock as ordinary rendering.
Facts and timing summaries report `animated-overlay-frames` so this path is
visible in measurements.

## JSON compositions

A visual scene can declare `composition` instead of `entry`. Backstage supplies
the renderer, so the project needs no HTML entry, JavaScript, React or build step.
Static stylesheets, images and fonts stay local resources.

```json
{
  "type": "visual",
  "duration": 3,
  "composition": {
    "width": 1920,
    "height": 1080,
    "nodes": [{
      "tag": "div",
      "text": "A simpler workflow",
      "style": {"position": "absolute", "left": "80px", "top": "300px", "fontSize": "80px"},
      "animate": [{
        "property": "opacity",
        "keyframes": [{"at": 0, "value": "0", "ease": "ease-out"}, {"at": 0.5, "value": "1"}]
      }]
    }]
  }
}
```

`width` and `height` define the composition's coordinate system, uniformly scaled
to fit the output. `stylesheets` is an optional list of project-relative CSS
files; their own font and image URLs remain relative to those files. Each node
supports `tag` (default `div`), `text`, string-valued `attributes`, `style`,
`children`, `start`, `end` and `animate`. Use `img` with a project-relative `src`,
SVG elements for vector graphics, and `#text` for mixed text inside children.
Text and children are mutually exclusive. Scripts, embedded documents, event
handlers and external links are rejected.

Every `start`, `end` and keyframe `at` is in seconds on the scene clock, including
nested nodes. Visibility starts inclusively and ends exclusively; omitting
`end` keeps a node until the scene ends. A hidden node occupies no layout space.
Each animated property has at least two strictly increasing keyframes with
string CSS values. `ease` belongs to the interval starting at that keyframe and
defaults to `linear`; CSS easing keywords, `steps(...)` and `cubic-bezier(...)`
are supported. Values hold before the first and after the last keyframe.
One property can have only one animation per node. The renderer evaluates
paused Web Animations from the render clock, making backward seeks deterministic.
CSS variables can be set through `style`, including names starting with `--`.

Add the visual scene to a normal presentation timeline. The existing resource
sandbox, cache invalidation, captions, audio mixer and frame renderer apply to
these scenes. See [the declarative example](../examples/visual/scenes/declarative.json).
