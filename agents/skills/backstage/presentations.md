# Compose Visuals And Existing Media

Use `render` for native visual scenes and existing recordings. `produce` records again.
No VM or recording hook is invoked by a presentation. Chromium, FFmpeg and
ffprobe must be available. Resources are local; no Node.js is required.

For a fully declarative project, start with [declarative.md](declarative.md).
The HTML contract below applies to custom entries, not native `composition`
scenes. The optional React export is described at the end of this guide.

## Project And Timeline

A leaf may inherit `templates` and `presentations` from an ancestor
`backstage.json`. Relative references inside the template (CSS, scripts, fonts,
images) keep working as long as they stay inside the workspace.

Add project registration (merge these keys into the existing backstage.json):

```json
{
  "render": { "w": 1920, "h": 1080, "fps": 30 },
  "presentations": {
    "demo": { "file": "presentations/demo.json", "out": "exports/demo.mp4" }
  }
}
```

A minimal `presentations/demo.json` using the built-in template:

```json
{
  "version": 1,
  "duration": 8,
  "sources": {
    "parent": { "scene": "parent" },
    "laptop": { "file": "recordings/laptop.mp4" }
  },
  "tracks": {
    "parent": { "source": "parent", "start": 0 },
    "laptop": { "source": "laptop", "start": 0 }
  },
  "timeline": [
    { "at": 0, "layout": "two-screens", "slots": { "left": "parent", "right": "laptop" } },
    { "at": 4, "layout": "single", "slots": { "center": "parent" },
      "transition": { "effect": "morph", "duration": 0.5 } }
  ]
}
```

`scene` resolves `scenes/NAME.json` and the last successful take of that
scene. The render holds that generation until it finishes. A `file` source
reads the path you name, including the projected `<record.out>/<scene>.mp4`.
A failed take does not replace that path. Supply `file` alongside `scene` to
select another file with the same editorial content. Missing files are errors,
not permission to record. Direct file sources need no scene.

Tracks are independently timed instances. To show the same source in two slots,
declare two track IDs. A track's optional `segments` lists `{from, to, rate}` in
source seconds and playback order. Repeats are allowed; rate defaults to 1.
`start` is final presentation time. A hidden track continues advancing; changing
layout never restarts it. End behavior is `freeze` by default, or `hide`.

Events start at zero and increase strictly. Transitions accept `fade`, `morph`,
`slide`, `push`, `wipe`, `zoom` and `blur`. Only slide, push and wipe accept
`direction`: `left`, `right`, `up` or `down`. `morph` requires two layouts;
other effects also support visual scenes and cards. Transition duration must fit
before the next event.
The explicit presentation duration determines the end of the movie.

## Visual Scenes And Endings

For native JSON elements and animation, use a `composition` scene as shown in
[declarative.md](declarative.md). For a custom HTML visual, create `scenes/explain.json`:

```json
{
  "type": "visual",
  "entry": "templates/household/visual.html",
  "duration": 4
}
```

Use `{"at": 4, "scene": "explain"}` in the timeline. A visual scene has no
recording configuration. Its duration is available content, not an instruction
to extend the timeline. The sample arrow needs three seconds; reserve four to
leave a reading pause. Inspect the final frame, not only the scene's entrance.

## Cards And Release Videos

An event may contain a `card`, alone or over a layout or visual scene:

```json
{"at": 0, "card": {"type": "title", "title": "Meet the update", "entrance": {"effect": "rise", "duration": 0.4}}}
```

Types are `title`, `statement`, `lower-third`, `badge` and `cta`; fields include
`subtitle`, `text`, local `logo` and `url`. Only a statement accepts `strike`.
Entrances support `rise`, `fade`, `type`, `scale` and `none`. Card and caption
motion easings are `linear`, `ease-out` and `ease-in-out`; native composition
keyframes additionally accept CSS easing functions. Presentation `theme` sets
editorial fonts, colors, sizes and reading times. It does not style arbitrary
composition nodes; use their styles or stylesheets.

For the standard statements → title → recorded demos → CTA structure, replace
`timeline` with `release`:

```json
"release": {
  "statements": ["Too many clicks"],
  "title": {"title": "Meet the update"},
  "demos": [{"track": "demo", "duration": 8, "title": "See it work"}],
  "cta": {"title": "Try it today", "url": "example.com"}
}
```

This fragment needs an existing `demo` track and enough presentation duration
for reading and the CTA. The release helper requires at least one demo; use
an explicit timeline for a video made only of graphics. `render`, `preview`
and `lint` accept `--aspect 16:9`, `9:16` or `1:1`. Inspect cropping and text
after changing aspect; arbitrary JSON compositions scale rather than reflow.

## Recorded Timing, Cameras And Actions

Event, independent audio and presentation-clock caption `at` values can anchor
to a labeled recorded step or narration cue:

```json
{"at": {"track": "demo", "step": "open-settings", "edge": "end", "offset": 0.5}, "card": {"type": "badge", "title": "Ready"}}
```

Anchors follow track cuts and rates; `offset` is in final presentation seconds.
Use `cue` instead of `step` for narration. Missing evidence or ambiguous repeated
occurrences fail compilation. Composition keyframe times remain numeric scene
seconds; they do not accept these anchor objects.

A track's `camera.keyframes` contains `at` and a normalized source `region`
(`x`, `y`, `w`, `h` within 0..1). Camera `at` uses presentation time and accepts
anchors. Camera easing belongs to the destination keyframe, unlike composition
easing: default `cut` holds the previous region; `linear`, `ease-out` or
`ease-in-out` interpolates toward the new one. Smooth moves preserve region
aspect ratio. `"camera": "follow"` uses recorded regions. Event `camera`
overrides are keyed by track ID. Cameras affect source video, not overlay text.

Presentation `actions` select recorded evidence, for example:

```json
"actions": [{"track": "demo", "step": "open-settings", "keys": true, "highlight": "outline", "cursor": true}]
```

Enable `record.observations: true` when capturing regions and pointer traces.
Highlights accept `outline` or `spotlight`, with an optional explicit normalized
`region`. Cursor motion and click pulses require real pointer evidence in the
take's facts; do not invent coordinates or clicks from key commands. A `step`
limits the overlay to that action. Traces follow cuts, repeats and rates, and
hide missing samples. These overlays require recorded track facts; animate a
drawn cursor as composition data when the task is an authored visual instead.

## Scene Text And Audio

A recording or visual scene can include:

```json
{
  "narration": {
    "language": "pt-BR",
    "cues": [
      { "id": "rule", "start": 1, "end": 3,
        "text": "Cada computador aplica a regra.", "audio": "assets/rule.wav" }
    ]
  },
  "audio": { "ambience": { "file": "assets/room.wav" } }
}
```

This is an editorial block, not a complete scene. Cue IDs and text stay in the
scene; never duplicate them into the presentation to position a caption. Audio
files are optional. Voice generation and automatic transcription are unavailable.

Add presentation `audio` items with a unique `id`. Select a `file`, a `track`'s
original audio, a scene `cue`, or a scene `asset`. Examples:

```json
[
  { "id": "voice", "track": "parent", "cue": "rule", "mode": "follow-video" },
  { "id": "music", "file": "assets/music.wav", "loop": true, "volume": 0.1 }
]
```

`follow-video` uses the selected track's cuts and speed, preserving pitch. A cue
file starts at its source cue's `start`; a plain file/asset starts at source zero.
Independent timing uses `mode: "independent"` (default), `at`, `from`, `to` and
`rate`. For example, select `scene: "parent", cue: "rule", at: 2, rate: 1` to keep
speech at normal speed while the video accelerates. Do not combine independent
timing or looping with follow-video.

Audio is opt-in; volume defaults to 1, mute and loop to false. `fade-in` and
`fade-out` use resulting audio seconds. Independent audio exceeding the film
fails unless `trim-end: true` explicitly permits truncation.

Set audio `role` to `music`, `narration` or `effect`. Music with `duck: true`
automatically lowers under audible narration; without narration it keeps its
configured volume. Cue audio is narration by default. Event `sound` selects
`file` and optional `volume` for a sound starting at the event boundary.

Presentation `captions` select `cue` and `scene` or `track`, plus `slot`:

- `clock: "source"`: requires a track; follows its cuts, repeats and speed.
- `clock: "presentation"`: requires `at` and positive `duration` in final time.
- `clock: "audio"`: requires `audio: "ID"`; follows that selected item's spans.

Text comes from the cue. The built-in caption slot is `subtitle`; simultaneous
cues there appear on separate lines. Use distinct custom slots for separate
positions. Source captions are split by cuts and clipped to presentation length.

## Templates And Animation

`backstage template init household` copies editable `template.html` and
`visual.html` into `templates/household/`, refusing an existing directory.
Register `templates.household.entry` in backstage.json and set a presentation's
`template` to `household`. Omitting it uses the built-in template.

Built-in layouts and slots:

| Layout | Slots |
| --- | --- |
| `single` | `center` |
| `two-screens` | `left`, `right` |
| `three-screens` | `left`, `right-top`, `right-bottom` |

Set presentation `parameters` for `title`, `background`, `foreground` and
`border`, or edit CSS. Custom HTML declares `window.backstageTemplate` with
`version: 1`, a `layouts` map to slot arrays, and `captionSlots`. Its async
`window.render(context)` must create the appropriate `data-slot` and
`data-caption-slot` elements before resolving. Visual scenes need the version,
render function and caption slots, but no layouts map.

Context provides `time`, `localTime`, `layout`, `parameters` and scene `narration`.
Derive animation state from these values so seeking reconstructs it. CSS/Web
Animations and SVG animation time are controlled by the runtime. Do not rely on
wall-clock timers or network resources.

Slots are axis-aligned rectangles. Borders, rounded corners, backgrounds and
shadows move with screens. `data-fit="cover"` crops; default `contain` preserves
the screen; `fill` stretches. On both the screenshot path and the overlay
path the track image is placed in an integer-pixel rectangle inside the
slot (`contain`/`cover`/`fill`; blend and morph still use `object-fit`). A still chunk with constant slot geometry (including CSS `%`),
equal borders and radii whose computed values are a single `px` token,
and no CSS/SMIL/`canvas` animation can encode from
two layer stills plus the prepared tracks (`>> chunk-N layered`). Set
`static: true` (or a list of layout names) when chrome and slot geometry do
not change with time; those events probe the first and last frame of each
chunk and keep one below still and one above still on that worker. The below
still is reused while the event and slot geometry stay the same; the above
still also keys on the chunk caption set. Percent
radii, elliptical two-value radii, and changing `scrollTop`/`scrollLeft` or form
`.value` stay on the screenshot path. The probe
must see the same DOM hash at every frame of the chunk (the hash includes
scroll offsets and form values). Templates that change pixels through shadow
DOM, nested iframes, CSSOM/`adoptedStyleSheets`, JS timers, their own
`requestAnimationFrame`, WebGL, `object`/`embed`, `background-image` on
`::before`/`::after`, or `mask-image`/`border-image` are unsupported on that
path. Arbitrary video-slot masks and 3D transforms are
unsupported. `.gif`, `.apng` and `.webp` in the template keep the screenshot
path.
JSON paths are project-relative; HTML/CSS resources are relative to their files.
Files a template fetches through the event prefix enter that chunk's cache
manifest; changing those bytes re-renders the events that loaded them.

## Validate And Review

```bash
backstage render demo --check
backstage preview demo
backstage render demo
```

`--check` validates resources, timing and slots but does not execute every
animation frame. Use `lint` for editorial typography and reading time; it does
not prove that arbitrary HTML/SVG or composition text fits. Review representative
frames and boundary states after rendering. Project `render.threads` (`prepare`, `filter`, `encode`)
applies only to presentation render; `0` uses the CPU defaults. Automatic
`render.workers` (`0`) runs one Chromium per worker from CPU, memory and the
number of segment-cache misses. The encoder shares the machine with Chromium during
the frame loop; `encode-seconds` overlaps that loop and is not the
bottleneck. Screenshot and draw are the loop cost unless a chunk is
`layered`. A second render reuses
cached tracks, mix and encoded chunks when the footage and the compiled plan
match (`>> chunk-N cached`; `backstage cache prune` reclaims that cache,
default 10G). Preview renders
first, then opens the exact MP4 with playback, seek and frame-step controls
(`preview --from --to --scale`; the player clock is absolute presentation
time). Ctrl-C closes it and removes its temporary files. It is not a live
editor. Check the final animation state and that audio/captions follow the
intended clock before exporting.

Output defaults to `exports/NAME.mp4`; `--out` overrides the project-relative
path. Companion facts record inputs, tool versions and a `timings` object
beside `render` (phase seconds, per-frame total/mean/max/p95, intermediate
bytes). Facts `inputs` list plan entries plus files loaded by events inside
the rendered interval, alongside hashes of the embedded renderers. Progress
ends with `>> render timings: ...`. Measured draw includes image load, host fonts, and a compositor
paint, not pure canvas cost. Source recordings remain
unchanged; failed exports do not replace an existing MP4.

## Optional React Export

For real React application components, `backstage export-remotion NAME --out
exports/react` creates a new portable project with takes/facts, prepared video,
mixed audio and resolved step/cue timing. Inside that optional export, run
`npm install` and `npm run studio`, then edit `stage.jsx`. Node is needed only
for this bridge. Its sample consumes the mapped evidence; native HTML layouts,
transitions, cameras and action graphics remain metadata for your React code.

Use this bridge when the user wants React authoring. It exports an existing
Backstage presentation; it does not import or convert a Remotion composition.
