# Backstage Remotion bridge

This directory is a portable export. Install its optional dependencies with
`npm install`, then run `npm run studio` or `npm run render`.

Edit `stage.jsx` to use real React application components. Its `steps`, `cues`,
`event`, and `time` props follow the exported recording and presentation clocks;
there are no manually copied step or narration timings. Keep `backstage.json`
as generated and export again to a new directory when editing the source plan.

`backstage.json` version 1 includes dimensions, frame count, resolved document,
theme, compiled captions and actions, and tracks with mapped step/cue intervals.
Intervals use presentation seconds; `source-start`, `source-end`, and `rate`
retain their selected source intervals, including repeated cuts. Frame bounds
use the same rounding as Backstage. `from`/`end` control visibility separately
from the prepared video's `frames`, including fractional-rate end rounding.
Each track includes its original media and
optional facts sidecar for provenance and its prepared, lossless H.264 video
for playback. Prepared videos reuse Backstage's cut/rate pipeline. Shared
originals are copied once. Mixed audio includes narration, effects and ducking.

The sample React composition displays visible tracks side by side, basic cards,
active cues and key labels. It is an editable integration starting point:
Backstage HTML templates, visual HTML scenes, transitions, camera crops and
action graphics are exported as metadata for your React implementation. Use
Backstage's native renderer when those existing visuals should be reproduced.
Node and Remotion are needed only in this exported project.
