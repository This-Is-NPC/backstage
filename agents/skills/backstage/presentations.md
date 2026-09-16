# Compose Existing Media

Use `render` for an existing recording's montage. `produce` records again.
No VM or recording hook is invoked by a presentation. Chromium, FFmpeg and
ffprobe must be available. Resources are local; no Node.js is required.

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

Events start at zero and increase strictly. `morph` is for layouts; `fade` also
supports visual scenes. Transition duration must fit before the next event.
The explicit presentation duration determines the end of the movie.

## Visual Scenes And Endings

Create `scenes/explain.json`:

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
two layer stills plus the prepared tracks (`>> chunk-N layered`). Percent
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
animation frame. Project `render.threads` (`prepare`, `filter`, `encode`)
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
the rendered interval. Progress ends with `>> render timings: ...`. Source input hashes stay
the same; `builtin:runtime.html` changes only because the runtime adds
`drawTimed`. Measured draw includes image load, host fonts, and a compositor
paint, not pure canvas cost. Source recordings remain
unchanged; failed exports do not replace an existing MP4.
