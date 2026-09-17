# Presentation implementation

The `internal/presentation` package composes files without calling the recording
engine. Existing `produce` behavior is separate and continues to record scenes.

## Pipeline

1. Load the project, strict version 1 presentation JSON and referenced scenes.
2. Probe sources and compile track spans, cue placement and selected audio.
3. Shared prepare: lossless FFV1 tracks (`-g 1`, in parallel up to the CPU
   count) and the stereo 48 kHz mix. Hits come from
   `${UserCacheDir}/backstage/render` (hardlink into the work dir, copy on
   `EXDEV`). Work files that came from the cache are read-only (0444); the
   render must never open `track-N.mkv` or `mix.wav` for writing.
4. Deterministic chunks: cut at every event boundary (`intervalFrames(e.At)`),
   then into pieces of at most `minChunkFrames = max(16, 2*fps)` measured from
   the event start, then clip to `[first, end)`. A piece that contains frames
   of event *i*'s incoming transition also hashes event *i−1*. Each chunk key
   (kind `segments`, `.mp4`) is SHA-256 of a v1 JSON object: size, fps, scale,
   `[first,end)`, encoder args without `-threads` or the output path, FFmpeg
   and browser versions (browser once per render), SHA-256 of the three
   embedded `web/` files, the event block (Event JSON, parameters, narration,
   intersecting captions, entry content hash, slotted tracks with
   `trackCacheKey`, `[from,to]`, `on-end`, `duration`), and for a
   transition chunk the previous event block plus overlap effect/duration/
   progress. Track `start` is not in the key: `to` is `floor(end-1 - start*fps)`,
   so a boundary frame that becomes invisible already changes `to`. Audio stays
   in the mix cache. Meta stores `{rel → sha256}` for files that event
   requested; a well-formed allowlisted asset that did not resolve is
   `rel → ""`, and the entry stays valid only while that path still does not
   resolve. Disallowed extensions never enter the manifest. A missing `files`
   field is invalid. Hits print
   `>> chunk-N cached`. A full hit skips Chromium and track decoders: concat,
   mux, facts. A single-chunk render with no audio copies the cache entry to a
   new 0644 file before rename, so the export is never a hardlink of the cache.
5. Misses are partitioned into `N` contiguous slices (`N` from
   `resolveWorkers`: CPU/2, memory/2 GiB, number of misses). Each worker
   starts one Chromium for its life and keeps track decoders open across
   consecutive chunks. Chromium runs with `--disable-partial-raster` so the
   anti-aliased edge of a rounded slot is identical from one screenshot to
   the next under CPU load. At the start of each miss, a decoder for a
   track that is no longer needed (`!trackNeeded`) is closed. `ensureDecoder` reuses the
   decoder when `index == d.frame` (already on that frame),
   `index == d.frame+1` (the next frame after the last `get`), or `d.done`
   (hold after EOF). Otherwise it closes and reopens at `index` with input
   `-ss`. The frame loop only calls `decoders[id].get`. The frames seam skips
   the geometry probe. A chunk that is not a transition, is at scale 1, keeps
   constant slot geometry (each of x,y,w,h equal across the chunk within
   1e-3; fractional `%` layouts are eligible), equal borders and equal corner
   radii whose computed values are a single `px` token (percent and two-value
   elliptical radii stay on the screenshot path), no active CSS/Web Animation, SMIL,
   `canvas`, `video`, or `.gif`/`.apng`/`.webp`, the same DOM hash at every
   probed frame, and the same caption set, is
   encoded as `layered`: probe every frame, capture a below still on the opaque
   host and an above still with a transparent host once, and run one FFmpeg
   graph (input `-ss` at `(S-0.5)/fps` plus relative `trim=start_frame=0`,
   scale `flags=area`, rounded-rect∩image mask, overlay `eof_action=repeat`,
   libx264 tail, `-frames:v`). The track image is an integer-pixel rectangle
   inside the snapped slot on both paths. When the event's template declares
   `static` for that layout (or `static: true` on a visual scene), the same
   overlay graph runs after probes at the chunk's **first and last** frames.
   Eligibility then ignores DOM hash, `canvas`,
   `video`, and animated images, and takes the caption set from `Plan.Text`
   (`t >= at && t < end` at every frame of the chunk). CSS/SMIL activity on
   either sample, or slot geometry that differs between them, prints
   `>> chunk-N static template <layout>: <reason>; using frames` (or
   `static scene <name>` when the event has no layout) and increments
   `static-violations`. A change that appears and disappears between those
   two samples stays under the static promise. Each worker holds one below
   still and one above still and replaces them when the event, geometry, or
   (for above) caption key changes. Track width/height and packet count are
   measured once per track path per render and shared across workers. Other misses
   stay on the screenshot loop. The DOM hash includes `scrollTop`/`scrollLeft`,
   form-control `.value`, checkbox `checked`/`indeterminate` and `select`
   `selectedOptions` indices for every `querySelectorAll('*')` node, plus the
   `activeElement` path. `getSelection()` and caret offset stay unseen.
   `data-fit` is `contain`, `cover` or `fill`; any other value is an initialize
   error that names the slot and the value. Progress is
   `>> chunk-N running|layered|ok|failed|interrupted`, the static-promise
   warning, and `>> render k/n frames` summing frames written across misses.
   Layered misses do not open PNG track decoders. Layer stills live under the
   render work dir (`.backstage-render-*`).
6. Concat demuxer `-c copy` when there is more than one chunk, then one AAC
   mux, facts, and rename. First error or Ctrl-C cancels every worker
   (all-or-nothing). The failing chunk prints `failed` and the error is
   `chunk N: …`; cancelled chunks print `interrupted` and a parent cancel
   returns `ctx.Err()`. Produce jobs let running takes finish; render chunks
   do not.
7. Render calls `drawTimed`; Check and initialize keep using `draw`. Measured
   draw includes image load, host fonts, and waiting for a compositor paint.
8. Companion facts include configuration, input hashes and render `timings`.
   Histogram p95 is from the merged missed-chunk counts. `renderer-start-seconds`
   sums worker `NewRenderer` times; `decoder-start-seconds` sums decoder opens
   on misses; `encode-seconds` is the max frames-path encoder Start to Wait
   (image2pipe chunks only). `composite-seconds` sums layered overlay encodes
   and is not copied into `encode-seconds`. A layered miss adds two screenshot
   histogram samples (below and above) unless a static event reuses a still.
   `chunk-seconds` lists misses only.
   Screenshot and draw dominate the screenshot loop; `probe` is the Go
   round-trip of `evalValue("probe")` (same histogram as decode/draw, not the
   JS `performance.now` inside the page); `layered-chunks` counts misses that
   took the overlay path. `static-violations` counts static-promise fallbacks
   and is omitted when zero. `initialize` returns one boolean per event for
   that declaration.
   Facts `inputs` are the union of plan entries, files loaded while rendering
   misses, and manifests of hits for events in this interval.

`model.go` owns validation and timing, `media.go` owns FFmpeg preparation,
`cache.go` owns the track and audio cache (`getOrFill` takes a validity
func and stores the fill's hashed `files` map as-is), `segments.go` owns
segment keys, manifest hashing and validity, `render.go` owns Chromium,
`layers.go` owns probe eligibility and the overlay graph, `chunks.go` and
`workers.go` own export, and `preview.go` owns preview and
template initialization. The asset server serves the host runtime at
`/TOKEN/builtin/runtime.html` and each event at `/TOKEN/event-<i>/builtin/`
and `/TOKEN/event-<i>/asset/`. `ConfinedPath`, the extension allowlist and CSP
are unchanged. The browser runtime and default HTML are embedded under `web/`.
`internal/budget` owns MemAvailable, NumCPU and the 2 GiB host reserve used by
both presentation workers and produce jobs.

## Preview intervals and draft scale

`preview --from/--to/--scale` validates options and calls an internal render
of `[first, end)` at absolute `t = n/fps`. `render` stays the full film at
scale 1 and uses today's mux (`-t` duration, no `atrim`).

Each needed track decoder starts with input `-ss` at `(S − 0.5)/fps`
(accurate seek, omitted when `S == 0`), using the local index with no
packet-count estimate. A first read that is not a clean `io.EOF` closes
the pipe, kills the process group and returns the read error plus
decoder stderr, without `ffprobe`. A clean EOF waits for ffmpeg: a
non-zero exit is `decode: …` with stderr and no packet count; exit 0
runs one `ffprobe -count_packets`. When the count is at most `S`, the
decoder reopens at the last packet so later `get` calls freeze there
(same as a full render). When the count is greater than `S`, the
no-frames error includes the count and decoder stderr. A corrupted tail
therefore aborts a preview that seeks past the last decodable frame,
while a full render freezes on that last frame. `S == 0` does not peek.

`decoder-start-seconds` (and `decoder-start=` on `>> render timings:`)
covers opening the decoders, the first-frame peek when `S > 0`, and
packet count plus reopen when those run.

Draft scale clips the full viewport `{0, 0, W, H, scale}`; odd PNG sizes are
cropped even by the encoder (`crop=trunc(iw/2)*2:trunc(ih/2)*2:0:0`).
Interval mux cuts the cached 48 kHz mix with
`atrim=start_sample:end_sample` (exclusive end). The player clock is
`first/fps + currentTime`.

Interval video against the matching full-export frames
(`select='between(n,first,end-1)'`, never `-ss` on H.264) must keep PSNR-Y
min ≥ 40 dB and mean ≥ 45 dB. Interval audio against the mix slice must keep
normalized cross-correlation ≥ 0.95 with `|lag| ≤ 5 ms` (same control on the
full export). Tests paint a binary frame index on the source with `geq` /
`bitand(N, 2^k)` and mix a deterministic chirp (`aevalsrc`) so a one-frame
or 4800-sample shift cannot hide behind a periodic tone.

Cache keys are SHA-256 of a `v1` JSON object (source content hash, compiled
plan, fps, FFmpeg version, encode args that change bytes). Segment keys also
cover browser version, embedded runtime hashes, the event (and previous event
on a transition), track `duration`/`on-end`, and a manifest of files the event
requested (including `rel → ""` for a missing allowlisted asset). A memo maps
resolved path + size + mtime-ns + inode to the content hash. Entries are
written to a `.tmp-*` name, `fsync`'d, chmod 0444, then renamed. Each key
has a lock file that prune never deletes. A live render holds `LOCK_SH` on
that lock until it finishes. `encode-seconds` is still Start→Wait. Timings
record `cache-hits` and `cache-misses` by type (`tracks`, `audio`, `segments`).

## Preview and isolation

Preview first renders a temporary MP4, then serves that file with playback,
seek and frame-step controls. It has an initial wait and is not an incremental
editor. Templates receive global and event-local times and must reconstruct
state when asked to render an arbitrary instant.

Use a temporary browser profile with its sandbox enabled. Serve resources only
on loopback, resolve configuration and scene files through `InputPath`, then
serve them from workspace-relative `/event-<i>/asset/` URLs (host runtime stays
at `/builtin/runtime.html`). The handler uses the same
confinement helper with the workspace as base and boundary. Normalized requests
outside that route are refused. A template or visual entry that cannot be
resolved inside the workspace is an error from `NewRenderer`, not a silent
fallback. Relative CSS, script, font and image references inside an inherited
template stay under the workspace. Block external resource requests. Runtime
images, fonts and scripts are local. Cancellation
terminates subprocesses and removes work directories; an unfinished export does
not replace an existing output.

Frame buffers are bounded, but lossless intermediate files can require
substantial disk space. Visual equivalence assumes fixed browser, fonts and
inputs; byte-identical MP4s across environments are not guaranteed.

## Validation

Run the standard quality gate, then opt into real Chromium/FFmpeg coverage:

```bash
python3 examples/presentation/generate.py
BACKSTAGE_RENDER_TEST=1 go test ./internal/presentation -v -count=1
BACKSTAGE_RENDER_TEST=1 go test -race -count=1 ./internal/presentation
```

The tests cover cuts and repeated spans, cue clocks, independent/following audio,
bounded music loops, template lookup, seek equivalence, cancellation, pixels
at the end of the example arrow animation, preview intervals (exact pixels,
frame-index codes, PSNR-Y, audio xcorr ≥ 0.95 / 5 ms against a chirp mix),
draft scale, deterministic chunks (composed pixels at fade/caption/visual
boundaries, coded exports, concat timestamps, PSNR/xcorr against one worker,
fail/cancel leftovers, automatic worker counts, segment cache cold/warm and
invalidation), and one-worker ffmpeg args
(mix in shared prepare, `-video_track_timescale fps` on the chunk encoder).
The generator uses tones and numbered
frames, so no VM, speech provider or externally recorded footage is required.

The quality gate builds govulncheck with Go 1.26.8 because Go 1.25's `go/types`
panics while analyzing chromedp's JSON dependency. The scanner still loads the
project using its Go environment; the project targets Go 1.25.13. The first run
requires downloading the scanner toolchain. Source-level vulnerability analysis
remains enabled. See [the upstream issue](https://github.com/golang/go/issues/73871).

Public contracts: [Presentations](../presentations.md), [Templates](../templates.md)
and [Scenes](../scenes.md).
