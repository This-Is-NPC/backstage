# Visual presentation examples

`cards` and `declarative` run without a recording. The other examples use an original recording
at `demo.mp4`; `actions` also needs its original `demo.facts.json` step sidecar.
Enable `record.observations` when recording to supply optional pane/window and
pointer coordinates. Camera regions can also be authored explicitly.

```bash
backstage --project examples/visual render cards
backstage --project examples/visual render declarative --check
backstage --project examples/visual render declarative
backstage --project examples/visual render camera
backstage --project examples/visual render actions
backstage --project examples/visual lint release --aspect 9:16
backstage --project examples/visual render release --aspect 9:16 --out exports/release-vertical.mp4
```

The examples reuse the ordinary presentation timeline, sources, tracks and
theme. Release expands its recurring structure before compilation; no video
specific HTML or React code is required.

`declarative` uses a native visual `composition`: elements, copy, visibility and
keyframes live in `scenes/declarative.json`. Static fonts, images and stylesheets
can be added locally. See [the walkthrough](../../docs/how-to-create-a-declarative-video.md).

Measure three cold renders of a fixed 60-second workload:

```bash
python3 examples/visual/benchmark.py /path/to/backstage all
```

Select `cards`, `camera`, `actions`, `release`, `captions`, `transitions` or
`audio` instead of `all` for one workload. The script creates synthetic media
and action facts in temporary directories outside the measured interval. These
facts are benchmark fixtures, not observations attributed to a real recording.
It reports actual output frames, wall time, workers, layered chunks and animated
overlay frames from render facts. Each iteration has a fresh project and cache.

To use real React components with these sources, export the optional bridge:

```bash
backstage --project examples/visual export-remotion release --out exports/react
cd examples/visual/exports/react
npm install
npm run studio
```

Edit the generated `stage.jsx`. Its active step/cue props use times derived
from recording facts and narration metadata, including selected cuts and rates.
The generated README documents the JSON contract and the sample's visual scope.
