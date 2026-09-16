# Presentation templates

Reference for HTML/CSS/SVG used by [presentations](presentations.md).

```bash
backstage template init household
```

This creates editable `template.html` and an example `visual.html` under
`templates/household/`, refusing an existing directory. Register the template
in `backstage.json` and reference the visual entry from a visual scene.

A template declares this contract in its HTML:

```javascript
window.backstageTemplate = {
  version: 1,
  layouts: { single: ["center"] },
  captionSlots: ["subtitle"]
};
window.render = async function (context) {
  // context.time: global seconds; context.localTime: seconds since this event.
  // context.layout, context.parameters, context.narration are also available.
  // Set styles and SVG attributes as a function of these values.
};
```

Provide elements with `data-slot="center"` and
`data-caption-slot="subtitle"`. `render(context)` must create the slots for its
layout before resolving. Set `data-fit="cover"` to crop a video to its slot,
`contain` to preserve the entire screen, or `fill` to stretch. Only those
three values exist: any other `data-fit` (including `none` and `scale-down`)
is an error at initialize; the message names the slot and the value. The built-in template provides `single` (`center`), `two-screens` (`left`, `right`) and
`three-screens` (`left`, `right-top`, `right-bottom`).

Slots describe axis-aligned screen rectangles. Their border, background,
rounded corners and shadow are carried with the rendered screen; CSS controls
the rest of the page. The runtime replaces slot pixels with prepared video
frames. Screens stack in layout slot order (first slot is behind). During a
blend, tracks that appear only on the previous event are drawn last. The
compositor, the probe, and `draw` share this order. A chunk whose slots keep
constant geometry (fractional CSS `%`
included), equal borders and equal corner radii whose computed values are a
single `px` token (or `0`), no CSS/Web Animation, no SMIL, no `canvas`/`video`,
and no `.gif`/`.apng`/`.webp`
images can be composed from two layer stills plus the prepared tracks.
Percent radii, elliptical two-value radii, and a scroll offset or form
`.value` that changes keep the screenshot path. The track image is placed in
an integer-pixel rectangle inside the slot on both the screenshot path and
the overlay path (`contain`/`cover`/`fill`; blend and morph still use
`object-fit`). The probe hashes the template DOM together with each
element's `scrollTop` / `scrollLeft`, the `.value` of `input` /
`textarea` / `select`, `checked` / `indeterminate` on checkboxes, selected
option indices on `select`, and the `activeElement` path at every frame of
the chunk; a node that appears and disappears in the middle, or a scroll
offset that changes, keeps the screenshot path. Arbitrary 3D transforms and
masks on video slots are not supported. Caption appearance uses the slot's
font, text alignment, color and background. The letterbox of `contain` shows
the slot background that the template already painted.

The probe does not see shadow DOM, nested iframes, CSSOM /
`adoptedStyleSheets`, `setTimeout`/`setInterval`, a template's own
`requestAnimationFrame`, WebGL, `object`/`embed`, `background-image` on
`::before`/`::after`, or `mask-image`/`border-image`. It also does not see
`getSelection()` or caret offset: `activeElement` identity is hashed, but a
selection or caret move that paints without changing that path is unseen. A
template that changes pixels through those means is unsupported on the
layered path and must stay on the screenshot loop.

The built-in parameters are `title`, `background`, `foreground` and `border`.
Custom templates may interpret additional JSON parameters. Visual scenes use
`version: 1`, a `render` function and caption slots but need no layout manifest.

The runtime waits for images and fonts, pauses CSS/Web Animations and evaluates
their current time, and sets SVG animation time explicitly. Custom JavaScript
must derive state from `context`; wall-clock timers, random values and external
network data cannot produce repeatable frames. Load assets locally. Fonts,
images, CSS and scripts may be separate files. Each timeline event is served
under `/event-<i>/`, so relative URLs resolve inside that event. Every file
fetched through that prefix is recorded in the chunk's cache manifest; changing
the bytes of such a file re-renders the events that loaded it.

## Leave time for the animation to finish

The timeline determines how long an event appears. A visual scene duration is
its available length, not an instruction to extend the presentation. Reserve
enough event time for the animation plus a reading pause. The supplied SVG
needs three seconds to draw its arrow; the example reserves four seconds.
Check the final frame as well as the transition into the scene.
