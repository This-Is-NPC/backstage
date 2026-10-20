# Rendering and performance decisions

This reference records the implemented behavior and measured decisions from
rendering, production and visual-capability work completed on 2026-10-20.
Public JSON contracts live in [Presentations](../presentations.md),
[Scenes](../scenes.md) and [Configuration](../configuration.md). Benchmark values
below are local observations, not speed guarantees on other hardware.

## Implemented pipelines

Presentation rendering chooses between Chromium frames, two static HTML layers,
static fade/push/wipe composition, and independent animated RGBA graphics over
prepared video. Stable slot geometry and supported masks are required for
layered composition. General template animation and unsupported geometry keep
the frame compositor. Native JSON compositions use the embedded scene renderer;
they do not require a project-specific script. Preparation overlaps browser
startup, audio mixes alongside chunks, and compatible cache hits avoid decoding
and rendering. Deterministic cuts, resource manifests and bounded worker budgets
are shared by full exports and preview intervals.

`internal/encode` owns CPU/VAAPI selection, probing, quality arguments and thread
settings. An unavailable explicitly selected encoder fails before recording or
rendering replaces an output. Production normalization probes the complete H.264
stream configuration, including SPS/PPS hash, color, aspect, frame rates and
timebase. A compatible batch is remuxed without re-encoding; other batches are
normalized sequentially. CPU normalization retains medium/CRF 23; retime and
framebuffer encoding retain veryfast/CRF 20.

Automatic CPU retiming batches independent piece outputs into one FFmpeg
invocation, then uses the shared atomic concat. Each piece retains its input
seek, source duration, rate, badge and output clock. Batch size is bounded by
half the host CPU count and one piece per two GiB of available memory after the
host reserve. Missing memory information, explicit filter/encode thread limits
and VAAPI retain one piece per invocation. Batching reduces process launches;
it does not fuse normalization or reduce lossy encode passes.

The framebuffer recorder streams one complete libvirt screenshot at a time
through memory into FFmpeg. Only encoded staging video is written during
capture. A final copy/remux restores the measured capture clock and H.264 timing
without another lossy encode. One worker owns cancellation and cleanup; repeated
stops are safe and failed finalization preserves an existing output. Screenshot
acquisition remains coarse; streaming does not promise a higher capture rate.

Recorded action intervals use the recorder-ready clock. Optional observations
add precise pane/window regions, continuous pointer samples and real mouse
presses. All eight output transforms are normalized for the selected capture
path. Missing evidence remains a gap. Step/cue references, cameras and action
overlays share source-span mapping through cuts, repeats and rates. Native
cards, transitions, caption entrances, ducking/effects, release layouts and aspect
variants use the existing render and audio pipelines. The optional React export
reuses preparation, mixing and timing metadata; it does not duplicate the native
visual renderer or import Remotion compositions.

## Acceptance criteria

Measure cold baseline/candidate pairs in alternating order on the same machine,
with fixtures generated outside the interval. Compare the affected phase and
total wall time. Keep frame counts, timestamps, duration and audio alignment.
Presentation path comparisons require PSNR-Y of at least 40 dB per frame and
45 dB mean; production recipe experiments used a stricter 45 dB per-frame floor.
Copy optimizations additionally compare encoded payloads or exact decoded pixels.
Thread counts and concurrent encoders must be reported with the results.

Cover cancellation, failed encodes, existing-output preservation, cache
invalidation, preview seeks, transition/caption boundaries and track endings.
The presentation audio mixer fixes blocks at 1024 samples before limiting so
thread scheduling does not change the final PCM. Preview audio comparisons use
cross-correlation of at least 0.95 and lag no greater than 5 ms.

## Accepted measurements

These are separate controlled fixtures. Their changes are not additive.

| Workload | Before median | Accepted median | Verification |
| --- | ---: | ---: | --- |
| Three compatible production clips | 3.348 s | 0.338 s | H.264 payload unchanged |
| Production retime phase, automatic threads | 0.642 s | 0.531 s | Same piece clocks and bytes |
| Complete mixed production with that batching | 3.121 s | 2.959 s | 692 frames, identical PTS and H.264 payload |
| Independent animated graphics over video | 4.906 s | 4.776 s | 60 frames, same PTS, minimum PSNR-Y 63.84 dB |
| Static one-second fade at 30 fps | 3.360 s | 0.894 s | Minimum PSNR-Y 47.86 dB, mean 54.30 dB |
| Decode each layered still once, 1080p/60 frames | 1.75 s | 1.18 s | Output bit-identical; CPU 9.6 s → 3.7 s |
| Live framebuffer finalization | 0.057624 s | 0.045916 s | 22 frames in every six-second capture |

The retime batching measurements used Go 1.26.10 and three cold alternating
pairs with 1280×720/30 fps footage. Automatic threading can run concurrent
encoders; it is not an equal-total-thread comparison. The compatible-batch
measurements precede that toolchain update. A mixed batch requiring normalization
initially increased from 3.140 s to 3.515 s because compatibility probing adds
overhead; the compatible-batch speedup does not apply universally.

Three live framebuffer pairs used a temporary diskless 256 MiB KVM guest.
Total medians including capture were 6.084393 s with PNG files and 6.069370 s
with the pipe; achieved rates were 3.6663 and 3.6666 fps. Closing fell 20.3%,
while total time changed only 0.25%. The guest was removed afterwards. Moving
20-frame fixtures at 0.5, 2.5, 3.1415, 3.3333 and 4 fps retained exact frame
clocks and durations; minimum PSNR-Y was 50.75 dB for the changed-rate cases.
A separate live VAAPI capture passed clock validation.

Earlier 60-second 1080p30 render comparisons, under external load averaging
20–31, measured 40.6 s/251.7 CPU-s for baseline, 38.3 s/193.2 CPU-s for the
CPU candidate, and 38.1 s/173.7 CPU-s with VAAPI encoding. Minimum PSNR-Y was
48.03 dB for CPU and 47.74 dB for VAAPI. These predate the final toolchain
update and are not a comparison against today's fixtures or a throughput promise.

## Rejected experiments

These decisions complete the evaluated optimizations. Their implementations
were removed; they are not dormant alternative paths or unfinished features.

| Experiment | Measured reason for rejection |
| --- | --- |
| Parallel normalization with four encoding threads | 4.898 s versus 3.270 s sequential |
| Uniform veryfast/CRF 20 normalization | Minimum PSNR-Y 41.72 dB, below the 45 dB floor |
| Fused retime and normalization | 0.753 s versus 1.422 s, but 180 frames instead of 182 and changed PTS |
| Dividing explicit retime thread limits across pieces | Minimum PSNR-Y 42.12 dB; explicit limits remain sequential |
| Hardware decode/scale/pad | Minimum PSNR-Y 29.99 dB; final high-quality attempt was also slower, 0.484 s versus 0.428 s |
| Vulkan overlay composition | 11.8 s versus 0.93 s per 1080p/60-frame chunk due to GPU transfers |

PAM stills, smaller worker slices, GPU Chromium screenshots and lossless H.264
presentation intermediates also failed their speed or pixel-preservation goals.
FFV1 remains the native preparation format. Lossless H.264 in the optional React
export serves browser playback and is a different use case. General hybrid
composition was not justified by the measured transfer cost; the accepted
animated-alpha path is restricted to independent graphics and stable chrome.

## Visual workload costs

Three cold runs per isolated workload used Go 1.26.10, 1280×720/30 fps,
60-second outputs (1,800 frames) and six automatic workers on the measured host.
Synthetic sources and action facts were generated before timing. These are
separate resource costs, not per-feature overhead estimates.

| Workload | Median wall time | Layered chunks | Animated overlay frames |
| --- | ---: | ---: | ---: |
| Cards and entrances | 5.834 s | 32 | 0 |
| Camera cuts and eased motion | 16.266 s | 32 | 0 |
| Keys, outlines and click pulses | 19.300 s | 90 | 220 |
| Release structure and recorded demo | 16.441 s | 33 | 3 |
| Caption entrances | 7.887 s | 50 | 0 |
| Stable push transitions | 6.898 s | 59 | 0 |
| Music ducking and narration | 10.645 s | 30 | 0 |

Camera path/seek comparisons reached minimum PSNR-Y 48.45 dB; action overlays
reached 57.21 dB. Release coverage checks all three aspect ratios and their
frame clocks. Pointer tests include gaps, seeking and temporary output rotations;
React export checks include exact prepared pixels and fractional hide boundaries.

## Reproduce and inspect

From the checkout, build a binary and run the visual workloads:

```bash
make build
python3 examples/visual/benchmark.py ./bin/backstage all
```

Select `cards`, `camera`, `actions`, `release`, `captions`, `transitions` or
`audio` instead of `all` for an isolated workload. Each render gets a fresh
project and cache. See [Production benchmark](../../examples/production/README.md)
for `BenchmarkProduction` and `BenchmarkProductionCopy` and their thread-budget
semantics. Export facts record input hashes, tool versions and phase timings.
`encode-seconds` overlaps frame production; it is not isolated encoder CPU time.

Run the [developer checks](dev-guide.md#local-checks) for regression coverage.
The [presentation implementation](presentations.md) documents selection,
resource manifests, timing and fallback rules in detail.
