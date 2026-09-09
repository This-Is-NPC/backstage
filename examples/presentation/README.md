# Declarative presentation example

Generate numbered video frames and audio test tones, then export both versions:

```bash
python3 examples/presentation/generate.py
go run ./cmd/backstage --project examples/presentation render complete
go run ./cmd/backstage --project examples/presentation render short
```

The complete version shows three screens, morphs to two and fades to an SVG
explanation. The short version reuses the inputs at double video speed while
keeping cue audio at normal speed. These are synthetic test tones, not speech.

`preview complete` renders a temporary MP4 and opens playback controls. It waits
for the initial render. Inputs and outputs are ignored by Git; the declarations
and generator are reproducible without VMs or network access.

See [the composition guide](../../docs/how-to-compose-presentations.md) for the
schema, audio policies, captions and HTML contract.
