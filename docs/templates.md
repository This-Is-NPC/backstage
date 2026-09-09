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
layout before resolving. Set `data-fit="cover"` to crop a video to its slot;
`contain` preserves its entire screen. The built-in template provides `single`
(`center`), `two-screens` (`left`, `right`) and `three-screens` (`left`,
`right-top`, `right-bottom`).

Slots describe axis-aligned screen rectangles. Their border, background,
rounded corners and shadow are carried with the rendered screen; CSS controls
the rest of the page. The runtime replaces slot pixels with prepared video
frames. Arbitrary 3D transforms and masks on video slots are not supported.
Caption appearance uses the slot's font, text alignment, color and background.

The built-in parameters are `title`, `background`, `foreground` and `border`.
Custom templates may interpret additional JSON parameters. Visual scenes use
`version: 1`, a `render` function and caption slots but need no layout manifest.

The runtime waits for images and fonts, pauses CSS/Web Animations and evaluates
their current time, and sets SVG animation time explicitly. Custom JavaScript
must derive state from `context`; wall-clock timers, random values and external
network data cannot produce repeatable frames. Load assets locally. Fonts,
images, CSS and scripts may be separate files.

## Leave time for the animation to finish

The timeline determines how long an event appears. A visual scene duration is
its available length, not an instruction to extend the presentation. Reserve
enough event time for the animation plus a reading pause. The supplied SVG
needs three seconds to draw its arrow; the example reserves four seconds.
Check the final frame as well as the transition into the scene.
