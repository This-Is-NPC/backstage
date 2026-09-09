# Presentation implementation

The `internal/presentation` package composes files without calling the recording
engine. Existing `produce` behavior is separate and continues to record scenes.

## Pipeline

1. Load the project, strict version 1 presentation JSON and referenced scenes.
2. Probe sources and compile track spans, cue placement and selected audio.
3. Prepare cuts and rates as lossless FFV1 tracks, and mix stereo 48 kHz audio.
4. Read PNG frames sequentially through FFmpeg pipes with bounded frame buffers.
5. Pass the selected frames and time to the embedded Chromium HTML runtime.
6. Capture the composed frames and encode H.264/yuv420p; mux AAC when selected.
7. Publish the MP4 and companion facts containing configuration and input hashes.

`model.go` owns validation and timing, `media.go` owns FFmpeg preparation,
`render.go` owns Chromium and export, and `preview.go` owns preview and template
initialization. The browser runtime and default HTML are embedded under `web/`.

## Preview and isolation

Preview first renders a temporary MP4, then serves that file with playback,
seek and frame-step controls. It has an initial wait and is not an incremental
editor. Templates receive global and event-local times and must reconstruct
state when asked to render an arbitrary instant.

Use a temporary browser profile with its sandbox enabled. Serve resources only
on loopback, restrict paths and symlinks to the project, and block external
resource requests. Runtime images, fonts and scripts are local. Cancellation
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
```

The tests cover cuts and repeated spans, cue clocks, independent/following audio,
bounded music loops, template lookup, seek equivalence, cancellation and pixels
at the end of the example arrow animation. The generator uses tones and numbered
frames, so no VM, speech provider or externally recorded footage is required.

The quality gate builds govulncheck with Go 1.26.8 because Go 1.25's `go/types`
panics while analyzing chromedp's JSON dependency. The scanner still loads the
project using its Go environment; the project targets Go 1.25.13. The first run
requires downloading the scanner toolchain. Source-level vulnerability analysis
remains enabled. See [the upstream issue](https://github.com/golang/go/issues/73871).

Public contracts: [Presentations](../presentations.md), [Templates](../templates.md)
and [Scenes](../scenes.md).
