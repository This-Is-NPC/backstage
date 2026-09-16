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
`encode-write`) with total, mean, max, p95 and frame count, and intermediate
byte sizes (`track-bytes`, `audio-part-bytes`, `mix-wav-bytes`,
`video-mp4-bytes`, `final-mp4-bytes`, `decoded-png-bytes`, `screenshot-bytes`)
and cache counters (`cache-hits`, `cache-misses`, each `{tracks, audio, segments}`).
Absent phases and missing files are omitted. `renderer-start-seconds` is the
sum of Chromium startups across the worker pool (one browser per worker);
`decoder-start-seconds` sums decoder opens on cache misses; `encode-seconds` is
the longest missed-chunk encoder Start to Wait. `concat-seconds` is omitted when there is
only one chunk. Progress ends with
`>> render timings: ...` including `decoder-start=`, `workers=` and `concat=`.
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
`builtin:runtime.html` changes only because the runtime adds `drawTimed`.
Measured draw includes what `draw` itself waits for — image load, host fonts,
and a compositor paint — not pure canvas cost. `encode-seconds` overlaps that
chunk's Chromium frame loop; it does not mean the encoder is the bottleneck. On a typical
`complete` render the loop itself spends about 4.3 s in screenshot and
3.7 s in draw of about 10 s total.

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
libx264; each chunk encoder uses `max(1, encode/workers)` threads and runs at
the same time as that chunk's Chromium frame loop, not after it.
`render.workers` is Chromium concurrency (one browser per worker). `0`,
`null` or an omitted key picks `max(1, min(NumCPU/2, memoryBudget/2GiB,
nMiss))` where `memoryBudget` is MemAvailable minus a 2 GiB host reserve.
An explicit `N` runs `N` workers, capped at the number of chunks that miss
the segment cache. Chunks are cut at event boundaries and then into pieces of
at most `max(16, 2*fps)` frames, measured from the event start and then clipped
to the render interval, so a preview that covers whole interior pieces shares
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
