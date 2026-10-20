# Production benchmark

This project defines three eight-second scenes, three playback speeds with a
badge, and an offline transition. Record it with `backstage produce benchmark`
from this directory to exercise a real desktop.

For repeatable CPU measurements without a desktop, use the same project with
deterministic 1280×720 test footage:

```sh
BACKSTAGE_BENCH_OUT=/tmp/production.mp4 go test ./internal/production \
  -run '^$' -bench '^BenchmarkProduction$' -benchtime=1x -count=3
```

Run the command from the repository root. Each iteration uses fresh segment
files and records wall seconds in `production.facts.json` under
`production-phases`: recording, retime, transitions, normalization, concat and
total. Recording includes take publication and teardown. Fixture generation is
outside the measured interval. No render cache is involved. Alternate baseline
and candidate runs on the same machine; compare frame timestamps, decoded frame
count and PSNR before accepting a performance change.

`BenchmarkProductionCopy` uses the same three source clips without retiming or
transitions. It measures the common case where all H.264 stream configurations
match and normalization can copy the video without changing its encoded bytes.
Use `-bench '^BenchmarkProductionCopy$'` to measure it separately. Both benchmarks
use the default FFmpeg thread sizing. Batching can run concurrent encoders with
those automatic settings; this is not a fixed total thread budget. Set explicit
project thread limits when measuring a CPU budget.

Automatic CPU retiming batches independent outputs into one FFmpeg invocation
when host resources allow it. Encoding settings, source seeks and per-piece
clocks remain unchanged. Explicit encoding/filter thread limits keep retiming
sequential so batching cannot multiply the requested thread budget. VAAPI also
stays sequential. Normalization is a separate pass in both cases.
