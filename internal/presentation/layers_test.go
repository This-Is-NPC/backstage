package presentation

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

const (
	// Structure, all fixtures: TestD5ContainCoverFill, TestD5BuiltinBorderRadius,
	// TestD5Text, TestD5BuiltinTwoScreens960, TestD5FractionalPercent320,
	// TestD5SnapEdgesNotWidth, TestD5NegativeHalfSnap, TestLayerZOrderMatchesDraw,
	// TestLayerOpaqueBelowMatchesHost, text-960, text-1080, two-screens-960/1080,
	// three-screens-960/1080, complete-960, contain-1080, cover-1080.
	d5CaptionMaxDiff = 1
	d5RingArcMaxDiff = 31

	// Real content (TestD5Text text-960, text-1080): PSNR-Y ≥ 40, p99 ≤ 4, max ≤ 100.
	d5RealMinPSNR = 40
	d5RealP99     = 4
	d5RealMaxDiff = 100

	// Synthetics testsrc2 / coded frames: two-screens-*, three-screens-*, complete-960,
	// contain-1080, cover-1080, TestD5ContainCoverFill, TestD5BuiltinBorderRadius.
	d5SynthMinPSNR = 35

	// Box PSNR only when min(CW,CH) >= 64.
	d5PSNRMinSide = 64
)

func TestSnapEdges(t *testing.T) {
	x, w := snapEdges(48.4, 412.2)
	if x != 48 || w != 413 {
		t.Fatalf("x=%d w=%d", x, w)
	}
	x, w = snapEdges(5.0*960/100, 43.0*960/100)
	if x+w != int(math.Floor(5.0*960/100+43.0*960/100+0.5)) {
		t.Fatalf("edge snap x=%d w=%d", x, w)
	}
	x, w = snapEdges(-14.5, 374.5)
	if x != -14 || w != 374 {
		t.Fatalf("neg half x=%d w=%d want -14 374", x, w)
	}
	x, w = snapEdges(10.4, 200.4)
	if x != 10 || w != 201 {
		t.Fatalf("width-round trap x=%d w=%d want 10 201", x, w)
	}
}

func TestLayeredFit(t *testing.T) {
	for _, fit := range []string{"", "contain", "cover", "fill"} {
		if !layeredFit(fit) {
			t.Fatal(fit)
		}
	}
	if layeredFit("scale-down") {
		t.Fatal("scale-down")
	}
}

func TestChunkEligibleReasons(t *testing.T) {
	p := &Plan{
		FPS:      10,
		Document: Document{Timeline: []Event{{Slots: map[string]string{"center": "cam"}}}},
		Tracks:   map[string]CompiledTrack{"cam": {Track: Track{}, Duration: 10}},
	}
	ch := chunkRange{First: 0, End: 2, Event: 0}
	g := probeGeo{Track: "cam", Slot: "center", X: 10, Y: 11, W: 128, H: 72, Fit: "fill", RadiusPx: true}
	base := probeFrame{Event: 0, Geometry: []probeGeo{g}, DOMHash: "abc"}
	if d := chunkEligible(p, ch, []probeFrame{base, base}); !d.Layered {
		t.Fatalf("want layered: %+v", d)
	}
	ch3 := chunkRange{First: 0, End: 3, Event: 0}
	mid := base
	mid.DOMHash = "other"
	if d := chunkEligible(p, ch3, []probeFrame{base, mid, base}); d.Layered || d.Reason != "dom hash" {
		t.Fatalf("mid hash: %+v", d)
	}
	empty := base
	empty.Geometry = []probeGeo{{Track: "cam", Slot: "center", X: 10, Y: 11, W: 0, H: 0, Fit: "fill", RadiusPx: true}}
	if d := chunkEligible(p, ch, []probeFrame{empty, empty}); d.Layered || d.Reason != "empty" {
		t.Fatalf("empty: %+v", d)
	}
	asym := base
	ag := g
	ag.BL, ag.BR = 2, 1
	asym.Geometry = []probeGeo{ag}
	if d := chunkEligible(p, ch, []probeFrame{asym, asym}); d.Layered || d.Reason != "border" {
		t.Fatalf("asymmetric: %+v", d)
	}
	rad := base
	rg := g
	rg.RTL, rg.RTR, rg.RBR, rg.RBL = 4, 8, 4, 4
	rad.Geometry = []probeGeo{rg}
	if d := chunkEligible(p, ch, []probeFrame{rad, rad}); d.Layered || d.Reason != "border" {
		t.Fatalf("radii: %+v", d)
	}
	frac := base
	frac.Geometry = []probeGeo{{Track: "cam", Slot: "center", X: 10.2, Y: 11, W: 128, H: 72, Fit: "fill", RadiusPx: true}}
	if d := chunkEligible(p, ch, []probeFrame{frac, frac}); !d.Layered {
		t.Fatalf("fractional: %+v", d)
	}
	hash := base
	hash.DOMHash = "other"
	if d := chunkEligible(p, ch, []probeFrame{base, hash}); d.Layered || d.Reason != "dom hash" {
		t.Fatalf("%+v", d)
	}
	anim := base
	anim.AnimationsActive = true
	if d := chunkEligible(p, ch, []probeFrame{base, anim}); d.Layered || d.Reason != "css animation" {
		t.Fatalf("%+v", d)
	}
	dyn := base
	dyn.DynamicElements = []string{"x.gif"}
	if d := chunkEligible(p, ch, []probeFrame{dyn, dyn}); d.Layered || d.Reason != "dynamic image" {
		t.Fatalf("%+v", d)
	}
	fit := base
	fit.Geometry = []probeGeo{{Track: "cam", Slot: "center", X: 10, Y: 11, W: 128, H: 72, Fit: "none", RadiusPx: true}}
	if d := chunkEligible(p, ch, []probeFrame{fit, fit}); d.Layered || d.Reason != "fit" {
		t.Fatalf("%+v", d)
	}
	cap := base
	cap.Captions = []probeCaption{{At: 0, End: 1, Slot: "s", Text: "a"}}
	if d := chunkEligible(p, ch, []probeFrame{base, cap}); d.Layered || d.Reason != "captions" {
		t.Fatalf("%+v", d)
	}
	moved := base
	moved.Geometry = []probeGeo{{Track: "cam", Slot: "center", X: 11, Y: 11, W: 128, H: 72, Fit: "fill", RadiusPx: true}}
	if d := chunkEligible(p, ch, []probeFrame{base, moved}); d.Layered || d.Reason != "geometry" {
		t.Fatalf("%+v", d)
	}
	pct := base
	pg := g
	pg.RadiusPx = false
	pct.Geometry = []probeGeo{pg}
	if d := chunkEligible(p, ch, []probeFrame{pct, pct}); d.Layered || d.Reason != "radius" {
		t.Fatalf("radius: %+v", d)
	}
	tiny := base
	tiny.Geometry = []probeGeo{{Track: "cam", Slot: "center", X: 10, Y: 11, W: 40, H: 40, Fit: "fill", RadiusPx: true}}
	if d := chunkEligible(p, ch, []probeFrame{tiny, tiny}); !d.Layered {
		t.Fatalf("40x40: %+v", d)
	}
	paint := base
	pg2 := g
	pg2.BorderLeft = "2px solid rgb(57, 65, 94)"
	paint.Geometry = []probeGeo{pg2}
	if d := chunkEligible(p, ch, []probeFrame{base, paint}); d.Layered || d.Reason != "geometry" {
		t.Fatalf("border string: %+v", d)
	}
}

func TestVisibleCaptionsHalfOpen(t *testing.T) {
	text := []TextSpan{{At: 1, End: 2, Slot: "s", Text: "a"}}
	if got := visibleCaptions(text, 0.999); len(got) != 0 {
		t.Fatalf("before: %+v", got)
	}
	if got := visibleCaptions(text, 1); len(got) != 1 || got[0].Text != "a" {
		t.Fatalf("at: %+v", got)
	}
	if got := visibleCaptions(text, 1.999); len(got) != 1 {
		t.Fatalf("inside: %+v", got)
	}
	if got := visibleCaptions(text, 2); len(got) != 0 {
		t.Fatalf("end: %+v", got)
	}
}

func TestChunkCaptionsStable(t *testing.T) {
	p := &Plan{
		FPS:  10,
		Text: []TextSpan{{At: 0, End: 1.5, Slot: "s", Text: "a"}, {At: 1.5, End: 3, Slot: "s", Text: "b"}},
	}
	if key, ok := chunkCaptionsStable(p, chunkRange{First: 0, End: 10}); !ok || key != captionKey(visibleCaptions(p.Text, 0)) {
		t.Fatalf("stable 0-1s: %q %v", key, ok)
	}
	if _, ok := chunkCaptionsStable(p, chunkRange{First: 0, End: 20}); ok {
		t.Fatal("want unstable across caption change")
	}
	if key, ok := chunkCaptionsStable(p, chunkRange{First: 20, End: 30}); !ok || key != captionKey(visibleCaptions(p.Text, 2)) {
		t.Fatalf("stable 2-3s: %q %v", key, ok)
	}
}

func TestStaticChunkEligibleIgnoresCanvas(t *testing.T) {
	p := &Plan{
		FPS:      10,
		Document: Document{Timeline: []Event{{Slots: map[string]string{"center": "cam"}}}},
		Tracks:   map[string]CompiledTrack{"cam": {Track: Track{}, Duration: 10}},
	}
	ch := chunkRange{First: 0, End: 2, Event: 0}
	g := probeGeo{Track: "cam", Slot: "center", X: 10, Y: 11, W: 128, H: 72, Fit: "fill", RadiusPx: true}
	pr := probeFrame{Event: 0, Geometry: []probeGeo{g}, Canvas: true, Video: true, DOMHash: "", DynamicElements: []string{"x.gif"}}
	if d, _ := staticChunkEligible(p, ch, pr, pr); !d.Layered {
		t.Fatalf("static: %+v", d)
	}
	if d := chunkEligible(p, ch, []probeFrame{pr, pr}); d.Layered || d.Reason != "dom hash" {
		t.Fatalf("b7: %+v", d)
	}
	pr.DOMHash = "abc"
	if d := chunkEligible(p, ch, []probeFrame{pr, pr}); d.Layered || d.Reason != "canvas" {
		t.Fatalf("b7 canvas: %+v", d)
	}
}

func TestStaticChunkEligibleChecksBothEvents(t *testing.T) {
	p := &Plan{
		FPS:      10,
		Document: Document{Timeline: []Event{{Slots: map[string]string{"center": "cam"}}}},
		Tracks:   map[string]CompiledTrack{"cam": {Track: Track{}, Duration: 10}},
	}
	ch := chunkRange{First: 0, End: 2, Event: 0}
	g := probeGeo{Track: "cam", Slot: "center", X: 10, Y: 11, W: 128, H: 72, Fit: "fill", RadiusPx: true}
	first := probeFrame{Event: 0, Geometry: []probeGeo{g}}
	last := first
	last.Event = 1
	if d, _ := staticChunkEligible(p, ch, first, last); d.Layered || d.Reason != "event" {
		t.Fatalf("last: %+v", d)
	}
	first.Event = 1
	last.Event = 0
	if d, _ := staticChunkEligible(p, ch, first, last); d.Layered || d.Reason != "event" {
		t.Fatalf("first: %+v", d)
	}
}

func TestSkipLayerProbeGatesTransition(t *testing.T) {
	p := &Plan{
		FPS: 10,
		Document: Document{
			Duration: 4,
			Timeline: []Event{
				{At: 0, Slots: map[string]string{"center": "cam"}},
				{At: 2, Slots: map[string]string{"center": "cam"}, Transition: Transition{Effect: "fade", Duration: 0.5}},
			},
		},
		Tracks: map[string]CompiledTrack{"cam": {Track: Track{}, Duration: 10}},
	}
	g := probeGeo{Track: "cam", Slot: "center", X: 10, Y: 11, W: 128, H: 72, Fit: "fill", RadiusPx: true}
	opts := renderOpts{Scale: 1}
	trans := 0
	for _, ch := range planChunks(p, 0, 40) {
		ct := chunkTransition(p, ch)
		if skipLayerProbe(p, opts, ch) != ct {
			t.Fatalf("chunk %+v skip=%v transition=%v", ch, skipLayerProbe(p, opts, ch), ct)
		}
		if !ct {
			continue
		}
		trans++
		probes := make([]probeFrame, ch.End-ch.First)
		for i := range probes {
			probes[i] = probeFrame{Event: ch.Event, Geometry: []probeGeo{g}, DOMHash: "abc"}
		}
		if d := chunkEligible(p, ch, probes); !d.Layered {
			t.Fatalf("chunkEligible on transition: %+v", d)
		}
		if d, _ := staticChunkEligible(p, ch, probes[0], probes[len(probes)-1]); !d.Layered {
			t.Fatalf("staticChunkEligible on transition: %+v", d)
		}
	}
	if trans < 1 {
		t.Fatal("no transition chunk")
	}
	if !skipLayerProbe(p, renderOpts{Scale: 0.5}, planChunks(p, 0, 40)[0]) {
		t.Fatal("scale")
	}
}

func TestTrackTrimFreeze(t *testing.T) {
	start, endEx := clampTrackTrim(4, 5, 2)
	if start != 1 || endEx != 2 {
		t.Fatalf("start=%d end=%d", start, endEx)
	}
	start, endEx = clampTrackTrim(0, 1, 2)
	if start != 0 || endEx != 2 {
		t.Fatalf("mid start=%d end=%d", start, endEx)
	}
}

func TestX264EncodeTail(t *testing.T) {
	p := &Plan{FPS: 12}
	got := x264EncodeTail(p, 3, "out.mp4")
	want := []string{"-an", "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p", "-threads", "3", "-video_track_timescale", "12", "out.mp4"}
	if !stringSliceEqual(got, want) {
		t.Fatalf("%v", got)
	}
	frames := encoderArgs(p, renderOpts{Scale: 1}, 3, "out.mp4")
	if !stringSliceEqual(frames[len(frames)-len(want):], want) {
		t.Fatalf("frames tail %v", frames)
	}
}

func TestScaleFilterKinds(t *testing.T) {
	got := slotScaleFilter(slotBox{Fit: "fill", CW: 960, CH: 540}, 1920, 1080)
	if !strings.Contains(got, "flags=area") || strings.Contains(got, "bicubic") {
		t.Fatal(got)
	}
}

func TestSnapSlotInnerRadiusSubtractsBorder(t *testing.T) {
	box := snapSlot(probeGeo{
		X: 10, Y: 20, W: 80, H: 60,
		BL: 2, BT: 2, BR: 2, BB: 2,
		RTL: 16, RTR: 16, RBR: 16, RBL: 16,
		Fit: "contain", RadiusPx: true,
	})
	if box.InnerRadius != 14 {
		t.Fatalf("inner radius %v", box.InnerRadius)
	}
	if box.X != 10 || box.Y != 20 || box.CX != 12 || box.CY != 22 {
		t.Fatalf("origin %+v", box)
	}
}

func TestLayerGraphOverlayAtContentOrigin(t *testing.T) {
	box := slotBox{
		Track: "cam", Slot: "center", Fit: "fill",
		X: 10, Y: 20, W: 80, H: 60,
		CX: 12, CY: 22, CW: 76, CH: 56,
		InnerRadius: 14,
	}
	g := layerGraph([]slotBox{box}, [][2]int{{320, 180}}, []int{4}, 10, "out")
	if !strings.Contains(g, "overlay=12:22:") {
		t.Fatalf("want overlay at content origin\n%s", g)
	}
	if strings.Contains(g, "overlay=10:20:") {
		t.Fatalf("overlay at box origin\n%s", g)
	}
}

func layerSlotPlan(t *testing.T, w, h, fps int, duration float64, layout, html string, captions []Caption) *Plan {
	return layerSlotPlanScene(t, w, h, fps, duration, layout, html, captions, "")
}

func layerSlotPlanScene(t *testing.T, w, h, fps int, duration float64, layout, html string, captions []Caption, sceneJSON string) *Plan {
	t.Helper()
	slots := map[string]string{"center": "cam"}
	if layout == "two-screens" {
		slots = map[string]string{"left": "cam", "right": "aux"}
	}
	tracks := map[string]Track{"cam": {Source: "cam"}}
	sources := map[string]Source{"cam": {File: "cam.mp4"}}
	if layout == "two-screens" {
		tracks["aux"] = Track{Source: "aux"}
		sources["aux"] = Source{File: "aux.mp4"}
	}
	p := writeFixture(t, Document{
		Version:  1,
		Duration: duration,
		Render:   scene.RenderCfg{W: w, H: h, FPS: fps},
		Sources:  sources,
		Tracks:   tracks,
		Timeline: []Event{{At: 0, Layout: layout, Slots: slots}},
		Captions: captions,
	})
	if sceneJSON != "" {
		if err := os.WriteFile(filepath.Join(p.Dir, "scenes", "explain.json"), []byte(sceneJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeCodedMP4(t, filepath.Join(p.Dir, "cam.mp4"), w, h, fps, duration+1)
	if _, ok := sources["aux"]; ok {
		writeCodedMP4(t, filepath.Join(p.Dir, "aux.mp4"), w, h, fps, duration+1)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, "film.html"), []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Templates = map[string]scene.TemplateRef{"film": {Entry: "film.html"}}
	ref := p.Presentations["show"]
	ref.Template = "film"
	p.Presentations["show"] = ref
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

const layerFillHTML = `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#808080}[data-slot]{position:absolute;overflow:hidden}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center'],'two-screens':['left','right']},captionSlots:['subtitle']};
window.render=async function(c){
  if(c.layout==='two-screens'){document.body.innerHTML='<div data-slot="left" data-fit="fill" style="left:0;top:0;width:50%;height:100%"></div><div data-slot="right" data-fit="fill" style="left:50%;top:0;width:50%;height:100%"></div><div data-caption-slot="subtitle" style="position:absolute;left:8%;right:8%;bottom:4%;height:8%"></div>';}
  else{document.body.innerHTML='<div data-slot="center" data-fit="FIT" style="left:10%;top:15%;width:80%;height:65%"></div><div data-caption-slot="subtitle" style="position:absolute;left:8%;right:8%;bottom:4%;height:8%;text-align:center;color:#fff"></div>';}
};
</script>`

const layerRadiusHTML = `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#11111a}[data-slot]{position:absolute;overflow:hidden;border:2px solid #39415e;border-radius:16px;background:#080b12}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:['subtitle']};
window.render=async function(c){
  document.body.innerHTML='<div data-slot="center" data-fit="FIT" style="left:10%;top:15%;width:80%;height:65%"></div><div data-caption-slot="subtitle" style="position:absolute;left:8%;right:8%;bottom:4%;height:8%;text-align:center;color:#fff"></div>';
};
</script>`

const layerBigRadiusHTML = `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#11111a}[data-slot]{position:absolute;overflow:hidden;border:2px solid #39415e;border-radius:30px;background:#080b12}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:['subtitle']};
window.render=async function(c){
  document.body.innerHTML='<div data-slot="center" data-fit="contain" style="left:20px;top:20px;width:40px;height:40px"></div><div data-caption-slot="subtitle" style="position:absolute;left:8%;right:8%;bottom:4%;height:8%"></div>';
};
</script>`

const layerOverlapHTML = `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#111}[data-slot]{position:absolute;overflow:hidden}</style><script>
window.backstageTemplate={version:1,layouts:{stack:['back','front']},captionSlots:[]};
window.render=async function(c){
  document.body.innerHTML='<div data-slot="back" data-fit="fill" style="left:10px;top:10px;width:80px;height:60px"></div><div data-slot="front" data-fit="fill" style="left:40px;top:20px;width:80px;height:60px"></div>';
};
</script>`

const layerGIFHTML = `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#000 url(spin.gif)}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){document.body.innerHTML='<div data-slot="center" data-fit="fill" style="inset:0;position:absolute"></div>';};
</script>`

const layerCanvasHTML = `<!doctype html><meta charset="utf-8"><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){document.body.innerHTML='<div data-slot="center" data-fit="fill" style="inset:0;position:absolute"></div><canvas width=8 height=8></canvas>';};
</script>`

func TestLayerFillIsLayered(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := replaceFit(layerFillHTML, "fill")
	plan := layerSlotPlan(t, 320, 180, 10, 0.4, "single", html, nil)
	var log bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log.Bytes(), []byte(">> chunk-0 layered\n")) {
		t.Fatal(log.String())
	}
}

func TestLayerFractionalGeometryIsLayered(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#222}[data-slot]{position:absolute;overflow:hidden}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  document.body.innerHTML='<div data-slot="center" data-fit="fill" style="left:10.2px;top:11px;width:128px;height:72px"></div>';
};
</script>`
	plan := layerSlotPlan(t, 320, 180, 10, 0.4, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var log bytes.Buffer
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log.Bytes(), []byte(">> chunk-0 layered\n")) {
		t.Fatal(log.String())
	}
}

func TestD5BuiltinTwoScreens960(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html, err := web.ReadFile("web/template.html")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	plan := layerSlotPlan(t, 960, 540, 12, 0.4, "two-screens", string(html), []Caption{
		{Scene: "explain", Cue: "cue", Clock: "presentation", At: 0, Duration: 0.4, Slot: "subtitle"},
	})
	layerMode = "frames"
	frames := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "frames.mp4"), io.Discard)
	})
	resetSeams()
	useTempCache(t)
	layerMode = "auto"
	r, err := NewRenderer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	pr, _, err := r.probeTimed(0)
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	_, above, _, _, err := r.captureLayers(0)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	boxes := make([]slotBox, len(pr.Geometry))
	for i, g := range pr.Geometry {
		boxes[i] = snapSlot(g)
	}
	composed := map[int][]byte{}
	var log bytes.Buffer
	observeComposedFrame = func(n int, _ float64, png []byte) {
		composed[n] = append([]byte(nil), png...)
	}
	if err = Render(ctx, plan, filepath.Join(t.TempDir(), "layered.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log.Bytes(), []byte(">> chunk-0 layered\n")) {
		t.Fatal(log.String())
	}
	if len(composed) == 0 {
		t.Fatal("no composed frames")
	}
	for n, shot := range composed {
		ref, ok := frames[n]
		if !ok {
			t.Fatalf("missing frames shot %d", n)
		}
		d5Check(t, "two-screens-960", "synth", ref.png, shot, boxes, above)
	}
}

func TestLayerForcedIneligibleErrors(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	layerMode = "layered"
	plan := longVisualPlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), io.Discard)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("layered:")) {
		t.Fatalf("err=%v", err)
	}
}

func TestLayerGIFIsFrames(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.4,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 10},
		Sources:  map[string]Source{"cam": {File: "cam.mp4"}},
		Tracks:   map[string]Track{"cam": {Source: "cam"}},
		Timeline: []Event{{At: 0, Layout: "single", Slots: map[string]string{"center": "cam"}}},
	})
	writeCodedMP4(t, filepath.Join(p.Dir, "cam.mp4"), 160, 90, 10, 1)
	gif := []byte{71, 73, 70, 56, 57, 97, 1, 0, 1, 0, 0, 0, 0, 59}
	if err := os.WriteFile(filepath.Join(p.Dir, "spin.gif"), gif, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, "film.html"), []byte(layerGIFHTML), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Templates = map[string]scene.TemplateRef{"film": {Entry: "film.html"}}
	ref := p.Presentations["show"]
	ref.Template = "film"
	p.Presentations["show"] = ref
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err = Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(log.Bytes(), []byte("layered\n")) {
		t.Fatal(log.String())
	}
}

func TestLayerCanvasIsFrames(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := layerCanvasHTML
	plan := layerSlotPlan(t, 160, 90, 10, 0.4, "single", html, nil)
	var log bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(log.Bytes(), []byte("layered\n")) {
		t.Fatal(log.String())
	}
}

func TestLayerCaptionChangeIsFrames(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := replaceFit(layerFillHTML, "fill")
	plan := layerSlotPlan(t, 320, 180, 10, 2, "single", html, []Caption{
		{Scene: "explain", Cue: "cue", Clock: "presentation", At: 0, Duration: 1, Slot: "subtitle"},
		{Scene: "explain", Cue: "cue", Clock: "presentation", At: 1, Duration: 1, Slot: "subtitle"},
	})
	// two captions need two cue texts; writeVisual-style scene already has one cue.
	if err := os.WriteFile(filepath.Join(plan.Project.Dir, "scenes", "explain.json"), []byte(`{"type":"visual","entry":"visual.html","duration":10,"narration":{"cues":[{"id":"cue","start":0,"end":1,"text":"ONE"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(log.Bytes(), []byte("layered\n")) {
		t.Fatal(log.String())
	}
}

func TestLayerZOrderMatchesDraw(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.4,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 10},
		Sources:  map[string]Source{"cam": {File: "cam.mp4"}, "aux": {File: "aux.mp4"}},
		Tracks:   map[string]Track{"cam": {Source: "cam"}, "aux": {Source: "aux"}},
		Timeline: []Event{{At: 0, Layout: "stack", Slots: map[string]string{"back": "cam", "front": "aux"}}},
	})
	writeCodedMP4(t, filepath.Join(p.Dir, "cam.mp4"), 80, 60, 10, 1)
	writeCodedMP4(t, filepath.Join(p.Dir, "aux.mp4"), 80, 60, 10, 1)
	if err := os.WriteFile(filepath.Join(p.Dir, "film.html"), []byte(layerOverlapHTML), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Templates = map[string]scene.TemplateRef{"film": {Entry: "film.html"}}
	ref := p.Presentations["show"]
	ref.Template = "film"
	p.Presentations["show"] = ref
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	layerMode = "frames"
	frames := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "frames.mp4"), io.Discard)
	})
	resetSeams()
	useTempCache(t)
	layerMode = "layered"
	r, err := NewRenderer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	pr, _, err := r.probeTimed(0)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	boxes := make([]slotBox, len(pr.Geometry))
	for i, g := range pr.Geometry {
		boxes[i] = snapSlot(g)
	}
	if len(boxes) != 2 || boxes[0].Slot != "back" || boxes[1].Slot != "front" {
		t.Fatalf("z-order slots %+v", boxes)
	}
	composed := map[int][]byte{}
	observeComposedFrame = func(n int, _ float64, png []byte) {
		composed[n] = append([]byte(nil), png...)
	}
	if err = Render(ctx, plan, filepath.Join(t.TempDir(), "layered.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(composed) == 0 {
		t.Fatal("no composed frames")
	}
	for n, shot := range composed {
		ref, ok := frames[n]
		if !ok {
			t.Fatalf("missing frames shot %d", n)
		}
		d5Check(t, "overlap", "synth", ref.png, shot, boxes, nil)
	}
}

func TestD5ContainCoverFill(t *testing.T) {
	requireRenderTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cases := []struct {
		name, fit string
	}{
		{"contain", "contain"},
		{"cover", "cover"},
		{"fill", "fill"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			useTempCache(t)
			resetSeams()
			defer resetSeams()
			html := replaceFit(layerFillHTML, c.fit)
			plan := layerSlotPlan(t, 320, 180, 10, 1, "single", html, []Caption{
				{Scene: "explain", Cue: "cue", Clock: "presentation", At: 0, Duration: 1, Slot: "subtitle"},
			})
			layerMode = "frames"
			frames := collectShots(t, func() error {
				return Render(ctx, plan, filepath.Join(t.TempDir(), "frames.mp4"), io.Discard)
			})
			resetSeams()
			useTempCache(t)
			layerMode = "layered"
			var below, above []byte
			r, err := NewRenderer(ctx, plan)
			if err != nil {
				t.Fatal(err)
			}
			pr, _, err := r.probeTimed(0)
			if err != nil {
				r.Close()
				t.Fatal(err)
			}
			below, above, _, _, err = r.captureLayers(0)
			r.Close()
			if err != nil {
				t.Fatal(err)
			}
			if len(below) == 0 {
				t.Fatal("below")
			}
			boxes := make([]slotBox, len(pr.Geometry))
			for i, g := range pr.Geometry {
				boxes[i] = snapSlot(g)
			}
			composed := map[int][]byte{}
			observeComposedFrame = func(n int, _ float64, png []byte) {
				composed[n] = append([]byte(nil), png...)
			}
			if err = Render(ctx, plan, filepath.Join(t.TempDir(), "layered.mp4"), io.Discard); err != nil {
				t.Fatal(err)
			}
			if len(composed) == 0 {
				t.Fatal("no composed frames")
			}
			for n, shot := range composed {
				ref, ok := frames[n]
				if !ok {
					t.Fatalf("missing frames shot %d", n)
				}
				d5Check(t, c.name, "synth", ref.png, shot, boxes, above)
			}
		})
	}
}

const layerFrac43HTML = `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#808080}[data-slot]{position:absolute;overflow:hidden}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:['subtitle']};
window.render=async function(c){
  document.body.innerHTML='<div data-slot="center" data-fit="contain" style="left:5%;top:28%;width:43%;height:43%"></div><div data-caption-slot="subtitle" style="position:absolute;left:8%;right:8%;bottom:4%;height:8%;text-align:center;color:#fff"></div>';
};
</script>`

const layerSnapWidthHTML = `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#808080}[data-slot]{position:absolute;overflow:hidden}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:['subtitle']};
window.render=async function(c){
  document.body.innerHTML='<div data-slot="center" data-fit="fill" style="left:10.4px;top:20px;width:200.4px;height:80px"></div><div data-caption-slot="subtitle" style="position:absolute;left:8%;right:8%;bottom:4%;height:8%;text-align:center;color:#fff"></div>';
};
</script>`

const layerOffscreenHTML = `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#808080}[data-slot]{position:absolute;overflow:hidden}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:['subtitle']};
window.render=async function(c){
  document.body.innerHTML='<div data-slot="center" data-fit="fill" style="left:-14.5px;top:10px;width:374.5px;height:150px"></div><div data-caption-slot="subtitle" style="position:absolute;left:8%;right:8%;bottom:4%;height:8%;text-align:center;color:#fff"></div>';
};
</script>`

func TestD5FractionalPercent320(t *testing.T) {
	requireRenderTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := layerSlotPlan(t, 320, 180, 10, 1, "single", layerFrac43HTML, []Caption{
		{Scene: "explain", Cue: "cue", Clock: "presentation", At: 0, Duration: 1, Slot: "subtitle"},
	})
	layerMode = "frames"
	frames := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "frames.mp4"), io.Discard)
	})
	resetSeams()
	useTempCache(t)
	layerMode = "layered"
	r, err := NewRenderer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	pr, _, err := r.probeTimed(0)
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	_, above, _, _, err := r.captureLayers(0)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(pr.Geometry) != 1 {
		t.Fatalf("geometry %d", len(pr.Geometry))
	}
	g := pr.Geometry[0]
	if math.Abs(g.Y-math.Round(g.Y)) < 1e-3 && math.Abs(g.W-math.Round(g.W)) < 1e-3 && math.Abs(g.H-math.Round(g.H)) < 1e-3 {
		t.Fatalf("want fractional Y/W/H %+v", g)
	}
	boxes := []slotBox{snapSlot(g)}
	if boxes[0].CW < d5PSNRMinSide || boxes[0].CH < d5PSNRMinSide {
		t.Fatalf("fixture too small for synth PSNR %+v", boxes[0])
	}
	composed := map[int][]byte{}
	observeComposedFrame = func(n int, _ float64, png []byte) {
		composed[n] = append([]byte(nil), png...)
	}
	if err = Render(ctx, plan, filepath.Join(t.TempDir(), "layered.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(composed) == 0 {
		t.Fatal("no composed frames")
	}
	for n, shot := range composed {
		ref, ok := frames[n]
		if !ok {
			t.Fatalf("missing frames shot %d", n)
		}
		d5Check(t, "frac-43", "synth", ref.png, shot, boxes, above)
	}
}

func TestD5SnapEdgesNotWidth(t *testing.T) {
	requireRenderTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := layerSlotPlan(t, 320, 180, 10, 1, "single", layerSnapWidthHTML, []Caption{
		{Scene: "explain", Cue: "cue", Clock: "presentation", At: 0, Duration: 1, Slot: "subtitle"},
	})
	layerMode = "frames"
	frames := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "frames.mp4"), io.Discard)
	})
	resetSeams()
	useTempCache(t)
	layerMode = "layered"
	r, err := NewRenderer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	pr, _, err := r.probeTimed(0)
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	_, above, _, _, err := r.captureLayers(0)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(pr.Geometry) != 1 {
		t.Fatalf("geometry %d", len(pr.Geometry))
	}
	g := pr.Geometry[0]
	x, w := snapEdges(g.X, g.W)
	if int(math.Floor(g.X+0.5))+int(math.Floor(g.W+0.5)) == x+w {
		t.Fatalf("not a width-round trap x=%g w=%g snap=%d,%d", g.X, g.W, x, w)
	}
	boxes := []slotBox{snapSlot(g)}
	composed := map[int][]byte{}
	observeComposedFrame = func(n int, _ float64, png []byte) {
		composed[n] = append([]byte(nil), png...)
	}
	if err = Render(ctx, plan, filepath.Join(t.TempDir(), "layered.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(composed) == 0 {
		t.Fatal("no composed frames")
	}
	for n, shot := range composed {
		ref, ok := frames[n]
		if !ok {
			t.Fatalf("missing frames shot %d", n)
		}
		d5Check(t, "snap-width", "synth", ref.png, shot, boxes, above)
	}
}

func TestD5NegativeHalfSnap(t *testing.T) {
	requireRenderTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := layerSlotPlan(t, 320, 180, 10, 1, "single", layerOffscreenHTML, []Caption{
		{Scene: "explain", Cue: "cue", Clock: "presentation", At: 0, Duration: 1, Slot: "subtitle"},
	})
	layerMode = "frames"
	frames := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "frames.mp4"), io.Discard)
	})
	resetSeams()
	useTempCache(t)
	layerMode = "layered"
	r, err := NewRenderer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	pr, _, err := r.probeTimed(0)
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	_, above, _, _, err := r.captureLayers(0)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(pr.Geometry) != 1 {
		t.Fatalf("geometry %d", len(pr.Geometry))
	}
	g := pr.Geometry[0]
	if g.X >= 0 {
		t.Fatalf("want negative origin %+v", g)
	}
	x, w := snapEdges(g.X, g.W)
	if x != -14 || w != 374 {
		t.Fatalf("snap x=%d w=%d from %+v", x, w, g)
	}
	boxes := []slotBox{snapSlot(g)}
	composed := map[int][]byte{}
	observeComposedFrame = func(n int, _ float64, png []byte) {
		composed[n] = append([]byte(nil), png...)
	}
	if err = Render(ctx, plan, filepath.Join(t.TempDir(), "layered.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(composed) == 0 {
		t.Fatal("no composed frames")
	}
	for n, shot := range composed {
		ref, ok := frames[n]
		if !ok {
			t.Fatalf("missing frames shot %d", n)
		}
		d5Check(t, "neg-half", "synth", ref.png, shot, boxes, above)
	}
}

func TestD5Text(t *testing.T) {
	requireRenderTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	for _, c := range []struct {
		name string
		w, h int
	}{
		{"text-960", 960, 540},
		{"text-1080", 1920, 1080},
	} {
		t.Run(c.name, func(t *testing.T) {
			useTempCache(t)
			resetSeams()
			defer resetSeams()
			plan := d5TextPlan(t, c.w, c.h)
			layerMode = "frames"
			frames := collectShots(t, func() error {
				return Render(ctx, plan, filepath.Join(t.TempDir(), "frames.mp4"), io.Discard)
			})
			resetSeams()
			useTempCache(t)
			layerMode = "layered"
			r, err := NewRenderer(ctx, plan)
			if err != nil {
				t.Fatal(err)
			}
			pr, _, err := r.probeTimed(0)
			if err != nil {
				r.Close()
				t.Fatal(err)
			}
			_, above, _, _, err := r.captureLayers(0)
			r.Close()
			if err != nil {
				t.Fatal(err)
			}
			boxes := make([]slotBox, len(pr.Geometry))
			for i, g := range pr.Geometry {
				boxes[i] = snapSlot(g)
			}
			composed := map[int][]byte{}
			observeComposedFrame = func(n int, _ float64, png []byte) {
				composed[n] = append([]byte(nil), png...)
			}
			if err = Render(ctx, plan, filepath.Join(t.TempDir(), "layered.mp4"), io.Discard); err != nil {
				t.Fatal(err)
			}
			if len(composed) == 0 {
				t.Fatal("no composed frames")
			}
			for n, shot := range composed {
				ref, ok := frames[n]
				if !ok {
					t.Fatalf("missing frames shot %d", n)
				}
				d5Check(t, c.name, "real", ref.png, shot, boxes, above)
			}
		})
	}
}

func d5TextPlan(t *testing.T, w, h int) *Plan {
	t.Helper()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.4,
		Render:   scene.RenderCfg{W: w, H: h, FPS: 12},
		Sources:  map[string]Source{"cam": {File: "cam.mp4"}},
		Tracks:   map[string]Track{"cam": {Source: "cam"}},
		Timeline: []Event{{At: 0, Layout: "single", Slots: map[string]string{"center": "cam"}}},
		Captions: []Caption{{Scene: "explain", Cue: "cue", Clock: "presentation", At: 0, Duration: 0.4, Slot: "subtitle"}},
	})
	writeTextMP4(t, filepath.Join(p.Dir, "cam.mp4"), 1920, 1080, 12)
	html := strings.ReplaceAll(d5TextHTML, "FIT", "contain")
	if err := os.WriteFile(filepath.Join(p.Dir, "film.html"), []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Templates = map[string]scene.TemplateRef{"film": {Entry: "film.html"}}
	ref := p.Presentations["show"]
	ref.Template = "film"
	p.Presentations["show"] = ref
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func writeTextMP4(t *testing.T, path string, w, h, fps int) {
	t.Helper()
	font := "/usr/share/fonts/TTF/JetBrainsMonoNerdFont-Regular.ttf"
	if _, err := os.Stat(font); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		"user@host:~$ ls -la /etc",
		"drwxr-xr-x 1 root root  4096 Jan  1 00:00 .",
		"-rw-r--r-- 1 root root   298 Jan  1 00:00 hostname",
		"func renderChunk() error {",
		"    for n := first; n < end; n++ {",
		"        draw(n)",
		"    }",
		"}",
		"abcdefghijklmnopqrstuvwxyz 0123456789",
		"THE QUICK BROWN FOX jumps over the lazy dog",
	}
	var filters []string
	for i, line := range lines {
		esc := strings.ReplaceAll(line, `\`, `\\`)
		esc = strings.ReplaceAll(esc, `'`, `\'`)
		esc = strings.ReplaceAll(esc, `:`, `\:`)
		filters = append(filters, fmt.Sprintf("drawtext=fontfile=%s:fontsize=16:fontcolor=white:x=24:y=%d:text='%s'", font, 40+i*28, esc))
	}
	vf := "format=rgb24," + strings.Join(filters, ",")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", fmt.Sprintf("color=c=0x11111a:s=%dx%d:r=%d:d=3", w, h, fps), "-vf", vf, "-pix_fmt", "yuv420p", path); err != nil {
		t.Fatal(err)
	}
}

const d5TextHTML = `<!doctype html><html><head><meta charset="utf-8"><style>
*{box-sizing:border-box}html,body{margin:0;width:100%;height:100%;overflow:hidden}body{background:var(--background,#11111a);color:var(--foreground,#e4e8f7);font:24px sans-serif}h1{position:absolute;left:5%;top:3%;font-size:1.5em;font-weight:500}.slot{position:absolute;border:2px solid var(--border,#39415e);border-radius:16px;background:#080b12;overflow:hidden}.caption{position:absolute;left:8%;right:8%;bottom:4%;height:8%;text-align:center;font-size:24px}
body[data-layout="single"] [data-slot="center"]{left:10%;top:15%;width:80%;height:65%}
</style></head><body><h1 id="title"></h1><div id="slots"></div><div class="caption" data-caption-slot="subtitle"></div>
<script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:['subtitle']};
window.render=async function(c){
 if(document.body.dataset.layout!==c.layout){document.body.dataset.layout=c.layout;document.querySelector('#slots').replaceChildren(...backstageTemplate.layouts[c.layout].map(name=>{const e=document.createElement('div');e.className='slot';e.dataset.slot=name;e.dataset.fit='FIT';return e}));}
 const p=c.parameters||{};document.querySelector('#title').textContent=p.title||'';
};
</script></body></html>`

func TestLayeredCacheReuse(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := replaceFit(layerFillHTML, "fill")
	plan := layerSlotPlan(t, 320, 180, 10, 0.4, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var log bytes.Buffer
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "a.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log.Bytes(), []byte("layered\n")) {
		t.Fatal(log.String())
	}
	log.Reset()
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "b.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log.Bytes(), []byte(">> chunk-0 cached\n")) {
		t.Fatal(log.String())
	}
}

func TestLayeredCancelLeavesPriorMP4(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := replaceFit(layerFillHTML, "fill")
	plan := layerSlotPlan(t, 320, 180, 10, 2, "single", html, nil)
	dir := t.TempDir()
	out := filepath.Join(dir, "keep.mp4")
	if err := os.WriteFile(out, []byte("prior mp4"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var seen int
	observeComposedFrame = func(int, float64, []byte) {
		seen++
		if seen >= 1 {
			cancel()
		}
	}
	layerMode = "layered"
	before := descendantSet(os.Getpid())
	var works []string
	observeCommand = func(name string, args []string) {
		if name != "ffmpeg" {
			return
		}
		for _, a := range args {
			if w := renderWorkDir(a); w != "" {
				works = append(works, w)
			}
		}
	}
	err := Render(ctx, plan, out, io.Discard)
	if err == nil {
		t.Fatal("expected cancel")
	}
	b, err := os.ReadFile(out)
	if err != nil || string(b) != "prior mp4" {
		t.Fatal("replaced output", err)
	}
	waitTreeIdle(t, before)
	if leftover := leftoverWork(filepath.Dir(out)); len(leftover) > 0 {
		t.Fatalf("work remains %v", leftover)
	}
	for _, w := range works {
		if _, err := os.Stat(w); err == nil {
			t.Fatalf("work dir remains %s", w)
		}
	}
}

func renderWorkDir(arg string) string {
	parts := strings.Split(arg, string(os.PathSeparator))
	for i, p := range parts {
		if strings.HasPrefix(p, ".backstage-render-") {
			return strings.Join(parts[:i+1], string(os.PathSeparator))
		}
	}
	return ""
}

func replaceFit(html, fit string) string {
	return string(bytes.ReplaceAll([]byte(html), []byte("FIT"), []byte(fit)))
}

func withStatic(html, value string) string {
	const needle = "window.backstageTemplate={version:1,"
	if !strings.Contains(html, needle) {
		panic("template missing backstageTemplate")
	}
	return strings.Replace(html, needle, "window.backstageTemplate={version:1,static:"+value+",", 1)
}

func d5Check(t *testing.T, name, kind string, refPNG, distPNG []byte, boxes []slotBox, above []byte) {
	t.Helper()
	psnr, p99, maxd, nBox := d5Zones(t, name, refPNG, distPNG, boxes, above)
	if nBox == 0 {
		return
	}
	minSide := d5PSNRMinSide
	for _, box := range boxes {
		s := box.CW
		if box.CH < s {
			s = box.CH
		}
		if s < minSide {
			minSide = s
		}
	}
	if minSide < d5PSNRMinSide {
		return
	}
	if kind == "real" {
		if psnr < float64(d5RealMinPSNR) {
			t.Fatalf("%s box PSNR-Y %.2f < %d", name, psnr, d5RealMinPSNR)
		}
		if p99 > d5RealP99 {
			t.Fatalf("%s box p99 %d > %d", name, p99, d5RealP99)
		}
		if maxd > d5RealMaxDiff {
			t.Fatalf("%s box max %d > %d", name, maxd, d5RealMaxDiff)
		}
		return
	}
	if psnr < float64(d5SynthMinPSNR) {
		t.Fatalf("%s box PSNR-Y %.2f < %d", name, psnr, d5SynthMinPSNR)
	}
}

func d5Zones(t *testing.T, name string, refPNG, distPNG []byte, boxes []slotBox, above []byte) (psnr float64, p99, maxd, nBox int) {
	t.Helper()
	ref, err := decodeNRGBABytes(refPNG)
	if err != nil {
		t.Fatal(err)
	}
	dist, err := decodeNRGBABytes(distPNG)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Bounds() != dist.Bounds() {
		t.Fatalf("%s bounds %v vs %v", name, ref.Bounds(), dist.Bounds())
	}
	var capImg *image.NRGBA
	if len(above) > 0 {
		capImg, err = decodeNRGBABytes(above)
		if err != nil {
			t.Fatal(err)
		}
	}
	b := ref.Bounds()
	var mseBox float64
	var boxAbs []int
	captionMax := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			a := ref.NRGBAAt(x, y)
			c := dist.NRGBAAt(x, y)
			dr := absInt(int(a.R) - int(c.R))
			dg := absInt(int(a.G) - int(c.G))
			db := absInt(int(a.B) - int(c.B))
			m := dr
			if dg > m {
				m = dg
			}
			if db > m {
				m = db
			}
			ink := capImg != nil && capImg.NRGBAAt(x, y).A > 0
			zone := d5Zone(x, y, boxes)
			switch {
			case ink && zone != "box":
				if m > captionMax {
					captionMax = m
				}
			case zone == "box":
				dy := (0.2126*float64(a.R) + 0.7152*float64(a.G) + 0.0722*float64(a.B)) - (0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B))
				mseBox += dy * dy
				nBox++
				boxAbs = append(boxAbs, m)
			case zone == "arc":
				if m > d5RingArcMaxDiff {
					t.Fatalf("%s arc pixel %d,%d diff=%d", name, x, y, m)
				}
			default:
				if m != 0 {
					t.Fatalf("%s %s pixel %d,%d diff=%d", name, zone, x, y, m)
				}
			}
		}
	}
	if captionMax > d5CaptionMaxDiff {
		t.Fatalf("%s caption max %d", name, captionMax)
	}
	if nBox == 0 {
		return 0, 0, 0, 0
	}
	psnr = 99.0
	if mseBox > 0 {
		psnr = 10 * math.Log10(255*255*float64(nBox)/mseBox)
	}
	p99 = percentile(boxAbs, 0.99)
	for _, v := range boxAbs {
		if v > maxd {
			maxd = v
		}
	}
	return psnr, p99, maxd, nBox
}

func d5Zone(x, y int, boxes []slotBox) string {
	for _, box := range boxes {
		if x >= box.CX && x < box.CX+box.CW && y >= box.CY && y < box.CY+box.CH {
			return "box"
		}
	}
	for _, box := range boxes {
		if d5NearRect(x, y, box.CX, box.CY, box.CW, box.CH, 3) {
			if d5CornerArc(x, y, box) {
				return "arc"
			}
			return "ring"
		}
	}
	return "out"
}

func d5CornerArc(x, y int, box slotBox) bool {
	if box.InnerRadius <= 0 {
		return false
	}
	r := int(math.Ceil(box.InnerRadius)) + 3
	near := func(cx, cy int) bool {
		dx, dy := x-cx, y-cy
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		return dx <= r && dy <= r
	}
	return near(box.CX, box.CY) || near(box.CX+box.CW-1, box.CY) ||
		near(box.CX, box.CY+box.CH-1) || near(box.CX+box.CW-1, box.CY+box.CH-1)
}

func decodeNRGBABytes(b []byte) (*image.NRGBA, error) {
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	if n, ok := img.(*image.NRGBA); ok {
		return n, nil
	}
	n := image.NewNRGBA(img.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			n.Set(x, y, img.At(x, y))
		}
	}
	return n, nil
}

func percentile(v []int, p float64) int {
	if len(v) == 0 {
		return 0
	}
	cp := append([]int(nil), v...)
	sort.Ints(cp)
	i := int(math.Ceil(p*float64(len(cp)))) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(cp) {
		i = len(cp) - 1
	}
	return cp[i]
}

func d5NearRect(x, y, rx, ry, rw, rh, pad int) bool {
	if x < rx-pad || x >= rx+rw+pad || y < ry-pad || y >= ry+rh+pad {
		return false
	}
	if x >= rx && x < rx+rw && y >= ry && y < ry+rh {
		return false
	}
	return true
}

func TestContainPadCenters(t *testing.T) {
	w, h, x, y := containPad(1280, 720, 408, 228)
	if w != 406 || h != 228 || x != 1 || y != 0 {
		t.Fatalf("%d %d %d %d", w, h, x, y)
	}
	if x != 408-w-x {
		t.Fatalf("not centered x=%d w=%d", x, w)
	}
}

func TestD5BuiltinBorderRadius(t *testing.T) {
	requireRenderTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cases := []struct {
		name, fit, html string
	}{
		{"contain-radius", "contain", replaceFit(layerRadiusHTML, "contain")},
		{"cover-radius", "cover", replaceFit(layerRadiusHTML, "cover")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d5RenderCompare(t, ctx, c.html, c.fit)
		})
	}
}

func d5RenderCompare(t *testing.T, ctx context.Context, html, fit string) {
	t.Helper()
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := layerSlotPlan(t, 320, 180, 10, 1, "single", html, []Caption{
		{Scene: "explain", Cue: "cue", Clock: "presentation", At: 0, Duration: 1, Slot: "subtitle"},
	})
	layerMode = "frames"
	frames := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "frames.mp4"), io.Discard)
	})
	resetSeams()
	useTempCache(t)
	layerMode = "layered"
	r, err := NewRenderer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	pr, _, err := r.probeTimed(0)
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	_, above, _, _, err := r.captureLayers(0)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	boxes := make([]slotBox, len(pr.Geometry))
	for i, g := range pr.Geometry {
		boxes[i] = snapSlot(g)
	}
	composed := map[int][]byte{}
	observeComposedFrame = func(n int, _ float64, png []byte) {
		composed[n] = append([]byte(nil), png...)
	}
	if err = Render(ctx, plan, filepath.Join(t.TempDir(), "layered.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(composed) == 0 {
		t.Fatal("no composed frames")
	}
	for n, shot := range composed {
		ref, ok := frames[n]
		if !ok {
			t.Fatalf("missing frames shot %d", n)
		}
		d5Check(t, fit, "synth", ref.png, shot, boxes, above)
	}
}

func TestD5BigRadiusRingArc(t *testing.T) {
	requireRenderTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := layerSlotPlan(t, 320, 180, 10, 0.4, "single", layerBigRadiusHTML, nil)
	layerMode = "frames"
	refShots := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "frames.mp4"), io.Discard)
	})
	work := t.TempDir()
	paths, _, err := prepareTracks(ctx, plan, work, io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRenderer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	pr, _, err := r.probeTimed(0)
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	below, above, _, _, err := r.captureLayers(0)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	boxes := make([]slotBox, len(pr.Geometry))
	for i, g := range pr.Geometry {
		boxes[i] = snapSlot(g)
	}
	var composed []byte
	ch := chunkRange{First: 0, End: 1, Index: 0, Event: 0}
	if err = compositeChunk(ctx, plan, ch, paths, boxes, below, above, 1, work, filepath.Join(work, "comp.mp4"), nil, func(_ int, b []byte) error {
		composed = append([]byte(nil), b...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	psnr, p99, maxd, nBox := d5Zones(t, "big-radius", refShots[0].png, composed, boxes, above)
	t.Logf("big-radius box PSNR-Y %.2f p99=%d max=%d n=%d inner=%dx%d r=%.0f", psnr, p99, maxd, nBox, boxes[0].CW, boxes[0].CH, boxes[0].InnerRadius)
	if nBox < 1 {
		t.Fatal("empty content box")
	}
	if boxes[0].CW >= d5PSNRMinSide && boxes[0].CH >= d5PSNRMinSide {
		t.Fatalf("fixture no longer small: %+v", boxes[0])
	}
}

func TestLayerMidChunkDOMHashIsFrames(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#222}[data-slot]{position:absolute}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  let h='<div data-slot="center" data-fit="fill" style="left:10px;top:10px;width:80px;height:60px"></div>';
  if(c.time>=0.5 && c.time<1.5) h+='<div id="badge">LIVE</div>';
  document.body.innerHTML=h;
};
</script>`
	plan := layerSlotPlan(t, 320, 180, 10, 2, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var log bytes.Buffer
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(log.Bytes(), []byte("layered\n")) {
		t.Fatal(log.String())
	}
	useTempCache(t)
	resetSeams()
	layerMode = "layered"
	err := Render(ctx, plan, filepath.Join(t.TempDir(), "forced.mp4"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "dom hash") {
		t.Fatalf("want dom hash, got %v", err)
	}
}

func TestLayerOpaqueBelowMatchesHost(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden}h1{position:absolute;left:8px;top:4px;margin:0;color:#fff;font:24px sans-serif}[data-slot]{position:absolute}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  document.body.innerHTML='<h1>Hello</h1><div data-slot="center" data-fit="fill" style="left:40px;top:40px;width:120px;height:80px"></div>';
};
</script>`
	plan := layerSlotPlan(t, 320, 180, 10, 0.4, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	layerMode = "frames"
	frames := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "frames.mp4"), io.Discard)
	})
	resetSeams()
	useTempCache(t)
	layerMode = "layered"
	r, err := NewRenderer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	pr, _, err := r.probeTimed(0)
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	r.Close()
	boxes := make([]slotBox, len(pr.Geometry))
	for i, g := range pr.Geometry {
		boxes[i] = snapSlot(g)
	}
	composed := map[int][]byte{}
	observeComposedFrame = func(n int, _ float64, png []byte) {
		composed[n] = append([]byte(nil), png...)
	}
	if err = Render(ctx, plan, filepath.Join(t.TempDir(), "layered.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	for n, shot := range composed {
		d5Check(t, "opaque", "synth", frames[n].png, shot, boxes, nil)
	}
}

func TestLayerHiddenSlotIsFrames(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  document.body.innerHTML='<div data-slot="center" data-fit="fill" style="display:none"></div>';
};
</script>`
	plan := layerSlotPlan(t, 320, 180, 10, 0.4, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var log bytes.Buffer
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(log.Bytes(), []byte("layered\n")) {
		t.Fatal(log.String())
	}
}

func TestLayerScrollIsFrames(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#222}[data-slot]{position:absolute}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  document.body.innerHTML='<div id="pane" style="position:absolute;left:0;top:0;width:80px;height:40px;overflow:auto"><div style="height:400px">scroll</div></div><div data-slot="center" data-fit="fill" style="left:10px;top:50px;width:128px;height:72px"></div>';
  document.getElementById('pane').scrollTop=c.localTime*100;
};
</script>`
	plan := layerSlotPlan(t, 320, 180, 10, 0.4, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var log bytes.Buffer
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(log.Bytes(), []byte("layered\n")) {
		t.Fatal(log.String())
	}
}

func TestLayerCheckboxIsFrames(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#222}[data-slot]{position:absolute}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  document.body.innerHTML='<input id="cb" type="checkbox"><div data-slot="center" data-fit="fill" style="left:10px;top:50px;width:128px;height:72px"></div>';
  document.getElementById('cb').checked=c.localTime>=0.2;
};
</script>`
	plan := layerSlotPlan(t, 320, 180, 10, 0.4, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var log bytes.Buffer
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(log.Bytes(), []byte("layered\n")) {
		t.Fatal(log.String())
	}
}

func TestInvalidDataFitErrors(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden}[data-slot]{position:absolute}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  document.body.innerHTML='<div data-slot="center" data-fit="scale-down" style="left:10px;top:10px;width:128px;height:72px"></div>';
};
</script>`
	plan := layerSlotPlan(t, 160, 90, 10, 0.4, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, err := NewRenderer(ctx, plan)
	if r != nil {
		r.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "center") || !strings.Contains(err.Error(), "scale-down") {
		t.Fatalf("want slot and value in error, got %v", err)
	}
}

func TestAsymmetricBorderPainted(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#222}[data-slot]{position:absolute;overflow:hidden;background:#080b12;border-style:solid;border-color:#ff0000;border-width:2px 8px 2px 2px}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  document.body.innerHTML='<div data-slot="center" data-fit="fill" style="left:40px;top:40px;width:80px;height:60px"></div>';
};
</script>`
	plan := layerSlotPlan(t, 320, 180, 10, 0.4, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var log bytes.Buffer
	layerMode = "frames"
	shots := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log)
	})
	if bytes.Contains(log.Bytes(), []byte("layered\n")) {
		t.Fatal(log.String())
	}
	r, err := NewRenderer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	pr, _, err := r.probeTimed(0)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(pr.Geometry) != 1 || pr.Geometry[0].BR < 7.5 || pr.Geometry[0].BL > 2.5 {
		t.Fatalf("want 2/2/8/2 borders %+v", pr.Geometry)
	}
	box := snapSlot(pr.Geometry[0])
	shot, ok := shots[0]
	if !ok {
		t.Fatal("missing frame 0")
	}
	img, err := decodeNRGBABytes(shot.png)
	if err != nil {
		t.Fatal(err)
	}
	y := box.Y + box.H/2
	var nRed int
	for x := box.X + box.W - 8; x < box.X+box.W; x++ {
		c := img.NRGBAAt(x, y)
		if c.R >= 200 && c.G < 40 && c.B < 40 {
			nRed++
		}
	}
	if nRed < 6 {
		t.Fatalf("right border not painted nRed=%d box=%+v", nRed, box)
	}
}

func TestLayerPercentRadiusIsFrames(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#222}[data-slot]{position:absolute;overflow:hidden;border-radius:30%;background:#080b12}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  document.body.innerHTML='<div data-slot="center" data-fit="fill" style="left:10px;top:10px;width:200px;height:120px"></div>';
};
</script>`
	plan := layerSlotPlan(t, 320, 180, 10, 0.4, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var log bytes.Buffer
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(log.Bytes(), []byte("layered\n")) {
		t.Fatal(log.String())
	}
}

func TestLayerAsymmetricBorderIsFrames(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#222}[data-slot]{position:absolute;overflow:hidden;border-style:solid;border-color:#39415e;border-left-width:2px;border-right-width:8px;border-top-width:2px;border-bottom-width:2px;background:#080b12}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  document.body.innerHTML='<div data-slot="center" data-fit="fill" style="left:10px;top:10px;width:200px;height:120px"></div>';
};
</script>`
	plan := layerSlotPlan(t, 320, 180, 10, 0.4, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var log bytes.Buffer
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(log.Bytes(), []byte("layered\n")) {
		t.Fatal(log.String())
	}
}

func TestLayerSmallBoxIsLayered(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := layerSlotPlan(t, 320, 180, 10, 0.4, "single", layerBigRadiusHTML, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var log bytes.Buffer
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log.Bytes(), []byte(">> chunk-0 layered\n")) {
		t.Fatal(log.String())
	}
}

func TestLayerHoldProbesTrackOnce(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 6,
		Render:   scene.RenderCfg{W: 320, H: 180, FPS: 10},
		Sources:  map[string]Source{"cam": {File: "coded.mp4"}},
		Tracks:   map[string]Track{"cam": {Source: "cam", Segments: []Segment{{From: 0, To: 0.25, Rate: 1}}}},
		Timeline: []Event{{At: 0, Layout: "single", Slots: map[string]string{"center": "cam"}}},
	})
	writeCodedMP4(t, filepath.Join(p.Dir, "coded.mp4"), 320, 180, 10, 1)
	if err := os.WriteFile(filepath.Join(p.Dir, "full.html"), []byte(fullBleedTemplate), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Templates = map[string]scene.TemplateRef{"full": {Entry: "full.html"}}
	ref := p.Presentations["show"]
	ref.Template = "full"
	p.Presentations["show"] = ref
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	plan.Project.Render.Workers = 1
	var packets, sizes int
	observeCountPackets = func(string) { packets++ }
	observeCommand = func(name string, args []string) {
		if name != "ffprobe" {
			return
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "stream=width,height") {
			sizes++
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var log bytes.Buffer
	if err = Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "layered\n") {
		t.Fatal(log.String())
	}
	if strings.Count(log.String(), " layered\n") != 3 {
		t.Fatalf("layered chunks\n%s", log.String())
	}
	if packets != 1 {
		t.Fatalf("countPackets=%d want 1\n%s", packets, log.String())
	}
	if sizes != 1 {
		t.Fatalf("ffprobeSize=%d want 1\n%s", sizes, log.String())
	}
}

func TestLayerHostRestoreAfterLayeredChunk(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden}h1{position:absolute;left:8px;top:4px;margin:0;color:#fff;font:24px sans-serif}[data-slot]{position:absolute}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  let h='<h1>Hello</h1><div data-slot="center" data-fit="fill" style="left:40px;top:40px;width:128px;height:72px"></div>';
  if(c.time>=2.5 && c.time<3.5) h+='<div id="badge">LIVE</div>';
  document.body.innerHTML=h;
};
</script>`
	plan := layerSlotPlan(t, 320, 180, 10, 4, "single", html, nil)
	plan.Project.Render.Workers = 1
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	var log bytes.Buffer
	mixed := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "auto.mp4"), &log)
	})
	body := log.String()
	if !strings.Contains(body, ">> chunk-0 layered\n") {
		t.Fatal(body)
	}
	if strings.Contains(body, ">> chunk-1 layered\n") {
		t.Fatal(body)
	}
	chunks := planChunks(plan, 0, plan.Frames)
	if len(chunks) < 2 {
		t.Fatalf("chunks %d", len(chunks))
	}
	cut := chunks[1].First
	resetSeams()
	useTempCache(t)
	layerMode = "frames"
	plan.Project.Render.Workers = 1
	ref := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "frames.mp4"), io.Discard)
	})
	if len(mixed) != len(ref) {
		t.Fatalf("shots mixed=%d frames=%d", len(mixed), len(ref))
	}
	var nCmp int
	for n, shot := range mixed {
		if n < cut {
			continue
		}
		want, ok := ref[n]
		if !ok {
			t.Fatalf("missing frames shot %d", n)
		}
		if err := samePixels(shot.png, want.png); err != nil {
			t.Fatalf("frame %d: %v", n, err)
		}
		nCmp++
	}
	if nCmp == 0 {
		t.Fatal("no frames-path shots")
	}
}

func TestLayerSeekUsesSS(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := replaceFit(layerFillHTML, "fill")
	plan := layerSlotPlan(t, 320, 180, 10, 8, "single", html, nil)
	var ss []string
	observeCommand = func(name string, args []string) {
		if name != "ffmpeg" || !containsArg(args, "-filter_complex") {
			return
		}
		for i, a := range args {
			if a == "-ss" && i+1 < len(args) {
				ss = append(ss, args[i+1])
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var log bytes.Buffer
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log.Bytes(), []byte("layered\n")) {
		t.Fatal(log.String())
	}
	want := decoderSeekTime(minChunkFrames(plan.FPS), plan.FPS)
	if want == "" {
		t.Fatal("expected a late-chunk seek")
	}
	found := false
	for _, v := range ss {
		if v == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing -ss %s in %v", want, ss)
	}
}

func TestLayerHoldRepeatsLastFrame(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.6,
		Render:   scene.RenderCfg{W: 320, H: 180, FPS: 10},
		Sources:  map[string]Source{"cam": {File: "coded.mp4"}},
		Tracks:   map[string]Track{"cam": {Source: "cam", Segments: []Segment{{From: 0, To: 0.25, Rate: 1}}}},
		Timeline: []Event{{At: 0, Layout: "single", Slots: map[string]string{"center": "cam"}}},
	})
	writeCodedMP4(t, filepath.Join(p.Dir, "coded.mp4"), 320, 180, 10, 1)
	if err := os.WriteFile(filepath.Join(p.Dir, "full.html"), []byte(fullBleedTemplate), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Templates = map[string]scene.TemplateRef{"full": {Entry: "full.html"}}
	ref := p.Presentations["show"]
	ref.Template = "full"
	p.Presentations["show"] = ref
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var log bytes.Buffer
	shots := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log)
	})
	if !bytes.Contains(log.Bytes(), []byte("layered\n")) {
		t.Fatal(log.String())
	}
	if len(shots) != plan.Frames {
		t.Fatalf("shots %d want %d", len(shots), plan.Frames)
	}
	last, err := readFrameCode(shots[plan.Frames-1].png)
	if err != nil {
		t.Fatal(err)
	}
	prev, err := readFrameCode(shots[plan.Frames-2].png)
	if err != nil {
		t.Fatal(err)
	}
	if last != prev {
		t.Fatalf("hold last=%d prev=%d", last, prev)
	}
	first, err := readFrameCode(shots[0].png)
	if err != nil {
		t.Fatal(err)
	}
	if first == last {
		t.Fatal("hold used a single frame")
	}
}

func TestLayerTransitionIntegerNeighbors(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#222}[data-slot]{position:absolute;overflow:hidden}</style><script>
window.backstageTemplate={version:1,layouts:{a:['center'],b:['center']},captionSlots:[]};
window.render=async function(c){
  document.body.innerHTML='<div data-slot="center" data-fit="fill" style="left:10px;top:10px;width:200px;height:120px"></div>';
};
</script>`
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 8,
		Render:   scene.RenderCfg{W: 320, H: 180, FPS: 10},
		Sources:  map[string]Source{"cam": {File: "cam.mp4"}},
		Tracks:   map[string]Track{"cam": {Source: "cam"}},
		Timeline: []Event{
			{At: 0, Layout: "a", Slots: map[string]string{"center": "cam"}},
			{At: 4, Layout: "b", Slots: map[string]string{"center": "cam"}, Transition: Transition{Effect: "fade", Duration: 0.5}},
		},
	})
	writeCodedMP4(t, filepath.Join(p.Dir, "cam.mp4"), 320, 180, 10, 9)
	if err := os.WriteFile(filepath.Join(p.Dir, "film.html"), []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Templates = map[string]scene.TemplateRef{"film": {Entry: "film.html"}}
	ref := p.Presentations["show"]
	ref.Template = "film"
	p.Presentations["show"] = ref
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	chunks := planChunks(plan, 0, plan.Frames)
	if len(chunks) < 3 {
		t.Fatalf("chunks %d", len(chunks))
	}
	var trans, layered []int
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var log bytes.Buffer
	if err = Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	body := log.String()
	for _, ch := range chunks {
		if chunkTransition(plan, ch) {
			trans = append(trans, ch.Index)
		}
	}
	if len(trans) == 0 {
		t.Fatal("no transition chunk")
	}
	transSet := map[int]bool{}
	for _, idx := range trans {
		transSet[idx] = true
		if strings.Contains(body, fmt.Sprintf(">> chunk-%d layered\n", idx)) {
			t.Fatalf("transition chunk %d layered\n%s", idx, body)
		}
	}
	for _, ch := range chunks {
		if transSet[ch.Index] {
			continue
		}
		if !strings.Contains(body, fmt.Sprintf(">> chunk-%d layered\n", ch.Index)) {
			t.Fatalf("neighbor chunk %d not layered\n%s", ch.Index, body)
		}
		layered = append(layered, ch.Index)
	}
	if len(layered) < 2 {
		t.Fatalf("layered neighbors %v trans %v\n%s", layered, trans, body)
	}
}

func TestStaticCanvasIsLayeredD5(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := withStatic(layerCanvasHTML, "true")
	plan := layerSlotPlan(t, 160, 90, 10, 0.4, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	layerMode = "frames"
	frames := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "frames.mp4"), io.Discard)
	})
	resetSeams()
	useTempCache(t)
	r, err := NewRenderer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !r.eventStatic(0) {
		r.Close()
		t.Fatal("event 0 should be static")
	}
	pr, _, err := r.probeTimed(0)
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	_, above, _, _, err := r.captureLayers(0)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	boxes := make([]slotBox, len(pr.Geometry))
	for i, g := range pr.Geometry {
		boxes[i] = snapSlot(g)
	}
	composed := map[int][]byte{}
	observeComposedFrame = func(n int, _ float64, png []byte) {
		composed[n] = append([]byte(nil), png...)
	}
	out := filepath.Join(t.TempDir(), "static.mp4")
	var log bytes.Buffer
	if err = Render(ctx, plan, out, &log); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log.Bytes(), []byte(">> chunk-0 layered\n")) {
		t.Fatal(log.String())
	}
	timings := asObject(t, readFactsMap(t, out)["timings"], "timings")
	probe := asObject(t, timings["probe"], "probe")
	if int(asFloat(t, probe["frames"], "probe.frames")) != 2 {
		t.Fatalf("probe %+v", probe)
	}
	if len(composed) == 0 {
		t.Fatal("no composed frames")
	}
	for n, shot := range composed {
		ref, ok := frames[n]
		if !ok {
			t.Fatalf("missing frames shot %d", n)
		}
		d5Check(t, "static-canvas", "synth", ref.png, shot, boxes, above)
	}
}

func TestStaticCSSAnimationUsesFrames(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#222}[data-slot]{position:absolute;overflow:hidden}@keyframes spin{to{transform:rotate(360deg)}}#mark{position:absolute;left:4px;top:4px;animation:spin 1s infinite}</style><script>
window.backstageTemplate={version:1,static:true,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  document.body.innerHTML='<div id="mark">x</div><div data-slot="center" data-fit="fill" style="left:10%;top:15%;width:80%;height:65%"></div>';
};
</script>`
	plan := layerSlotPlan(t, 320, 180, 10, 0.4, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out := filepath.Join(t.TempDir(), "out.mp4")
	var log bytes.Buffer
	if err := Render(ctx, plan, out, &log); err != nil {
		t.Fatal(err)
	}
	body := log.String()
	if bytes.Contains(log.Bytes(), []byte("layered\n")) {
		t.Fatal(body)
	}
	if !strings.Contains(body, ">> chunk-0 static template single: css animation; using frames") {
		t.Fatal(body)
	}
	if !strings.Contains(body, "static-violations=1") {
		t.Fatal(body)
	}
	timings := asObject(t, readFactsMap(t, out)["timings"], "timings")
	if int(asFloat(t, timings["static-violations"], "static-violations")) != 1 {
		t.Fatal(timings["static-violations"])
	}
}

func TestStaticGeometryChangeUsesFrames(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#222}[data-slot]{position:absolute;overflow:hidden}</style><script>
window.backstageTemplate={version:1,static:true,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  const left = c.localTime>=2.5 ? '20%' : '10%';
  document.body.innerHTML='<div data-slot="center" data-fit="fill" style="left:'+left+';top:15%;width:60%;height:65%"></div>';
};
</script>`
	plan := layerSlotPlan(t, 320, 180, 10, 6, "single", html, nil)
	plan.Project.Render.Workers = 2
	composed := map[int][]byte{}
	observeComposedFrame = func(n int, _ float64, png []byte) {
		composed[n] = append([]byte(nil), png...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out := filepath.Join(t.TempDir(), "out.mp4")
	var log bytes.Buffer
	if err := Render(ctx, plan, out, &log); err != nil {
		t.Fatal(err)
	}
	body := log.String()
	if !strings.Contains(body, ">> chunk-0 layered\n") {
		t.Fatal(body)
	}
	if strings.Contains(body, ">> chunk-1 layered\n") {
		t.Fatal(body)
	}
	if !strings.Contains(body, ">> chunk-1 static template single: geometry; using frames") {
		t.Fatal(body)
	}
	if !strings.Contains(body, ">> chunk-2 layered\n") {
		t.Fatal(body)
	}
	timings := asObject(t, readFactsMap(t, out)["timings"], "timings")
	if int(asFloat(t, timings["static-violations"], "static-violations")) != 1 {
		t.Fatal(timings["static-violations"])
	}
	for n := 20; n < 40; n++ {
		if _, ok := composed[n]; ok {
			t.Fatalf("chunk [2,4) frame %d was layered", n)
		}
	}
}

func TestStaticCaptionSwapRecapturesAbove(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := withStatic(replaceFit(layerFillHTML, "fill"), "true")
	sceneJSON := `{"type":"visual","entry":"visual.html","duration":10,"narration":{"cues":[{"id":"one","start":0,"end":1,"text":"ONE"},{"id":"two","start":0,"end":1,"text":"TWO"}]}}`
	plan := layerSlotPlanScene(t, 320, 180, 10, 6, "single", html, []Caption{
		{Scene: "explain", Cue: "one", Clock: "presentation", At: 0, Duration: 4, Slot: "subtitle"},
		{Scene: "explain", Cue: "two", Clock: "presentation", At: 4, Duration: 2, Slot: "subtitle"},
	}, sceneJSON)
	plan.Project.Render.Workers = 1
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	layerMode = "frames"
	frames := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "frames.mp4"), io.Discard)
	})
	resetSeams()
	useTempCache(t)
	plan.Project.Render.Workers = 1
	shots := 0
	screenshotObserver = func(bool) { shots++ }
	composed := map[int][]byte{}
	observeComposedFrame = func(n int, _ float64, png []byte) {
		composed[n] = append([]byte(nil), png...)
	}
	out := filepath.Join(t.TempDir(), "static.mp4")
	var log bytes.Buffer
	if err := Render(ctx, plan, out, &log); err != nil {
		t.Fatal(err)
	}
	body := log.String()
	if strings.Count(body, " layered\n") != 3 {
		t.Fatalf("want 3 layered\n%s", body)
	}
	if shots != 3 {
		t.Fatalf("screenshotObserver=%d want 3\n%s", shots, body)
	}
	timings := asObject(t, readFactsMap(t, out)["timings"], "timings")
	probe := asObject(t, timings["probe"], "probe")
	if int(asFloat(t, probe["frames"], "probe.frames")) != 6 {
		t.Fatalf("probe %+v", probe)
	}
	shot := asObject(t, timings["screenshot"], "screenshot")
	if int(asFloat(t, shot["frames"], "screenshot.frames")) != 3 {
		t.Fatalf("screenshot %+v", shot)
	}
	r, err := NewRenderer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	pr, _, err := r.probeTimed(4)
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	_, above, _, _, err := r.captureLayers(4)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	boxes := make([]slotBox, len(pr.Geometry))
	for i, g := range pr.Geometry {
		boxes[i] = snapSlot(g)
	}
	third := 0
	for n, shotPNG := range composed {
		if n < 40 {
			continue
		}
		ref, ok := frames[n]
		if !ok {
			t.Fatalf("missing frames shot %d", n)
		}
		d5Check(t, "caption-swap", "synth", ref.png, shotPNG, boxes, above)
		third++
	}
	if third == 0 {
		t.Fatal("no third-chunk composed frames")
	}
}

func TestStaticTwoEventsFourScreenshots(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := withStatic(replaceFit(layerFillHTML, "fill"), "true")
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.8,
		Render:   scene.RenderCfg{W: 320, H: 180, FPS: 10},
		Sources:  map[string]Source{"cam": {File: "cam.mp4"}},
		Tracks:   map[string]Track{"cam": {Source: "cam"}},
		Timeline: []Event{
			{At: 0, Layout: "single", Slots: map[string]string{"center": "cam"}},
			{At: 0.4, Layout: "single", Slots: map[string]string{"center": "cam"}},
		},
	})
	writeCodedMP4(t, filepath.Join(p.Dir, "cam.mp4"), 320, 180, 10, 2)
	if err := os.WriteFile(filepath.Join(p.Dir, "film.html"), []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Templates = map[string]scene.TemplateRef{"film": {Entry: "film.html"}}
	ref := p.Presentations["show"]
	ref.Template = "film"
	p.Presentations["show"] = ref
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	plan.Project.Render.Workers = 1
	shots := 0
	screenshotObserver = func(bool) { shots++ }
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var log bytes.Buffer
	if err = Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if strings.Count(log.String(), " layered\n") != 2 {
		t.Fatal(log.String())
	}
	if shots != 4 {
		t.Fatalf("screenshotObserver=%d want 4\n%s", shots, log.String())
	}
}

func TestStaticStillRecaptureOnGeometry(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#11111a}[data-slot]{position:absolute;overflow:hidden;border:8px solid #ff00ff;background:#080b12}</style><script>
window.backstageTemplate={version:1,static:true,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  const left = c.localTime>=4 ? '40%' : '10%';
  document.body.innerHTML='<div data-slot="center" data-fit="fill" style="left:'+left+';top:15%;width:40%;height:65%"></div>';
};
</script>`
	plan := layerSlotPlan(t, 320, 180, 10, 6, "single", html, nil)
	plan.Project.Render.Workers = 1
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	layerMode = "frames"
	frames := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "frames.mp4"), io.Discard)
	})
	resetSeams()
	useTempCache(t)
	plan.Project.Render.Workers = 1
	shots := 0
	screenshotObserver = func(bool) { shots++ }
	composed := map[int][]byte{}
	observeComposedFrame = func(n int, _ float64, png []byte) {
		composed[n] = append([]byte(nil), png...)
	}
	out := filepath.Join(t.TempDir(), "static.mp4")
	var log bytes.Buffer
	if err := Render(ctx, plan, out, &log); err != nil {
		t.Fatal(err)
	}
	body := log.String()
	if strings.Count(body, " layered\n") != 3 {
		t.Fatalf("want 3 layered\n%s", body)
	}
	if shots != 4 {
		t.Fatalf("screenshotObserver=%d want 4\n%s", shots, body)
	}
	timings := asObject(t, readFactsMap(t, out)["timings"], "timings")
	probe := asObject(t, timings["probe"], "probe")
	if int(asFloat(t, probe["frames"], "probe.frames")) != 6 {
		t.Fatalf("probe %+v", probe)
	}
	shot := asObject(t, timings["screenshot"], "screenshot")
	if int(asFloat(t, shot["frames"], "screenshot.frames")) != 4 {
		t.Fatalf("screenshot %+v", shot)
	}
	r, err := NewRenderer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	pr, _, err := r.probeTimed(4)
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	_, above, _, _, err := r.captureLayers(4)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	boxes := make([]slotBox, len(pr.Geometry))
	for i, g := range pr.Geometry {
		boxes[i] = snapSlot(g)
	}
	third := 0
	for n, shotPNG := range composed {
		if n < 40 {
			continue
		}
		ref, ok := frames[n]
		if !ok {
			t.Fatalf("missing frames shot %d", n)
		}
		d5Check(t, "still-geo", "synth", ref.png, shotPNG, boxes, above)
		third++
	}
	if third == 0 {
		t.Fatal("no k+2 composed frames")
	}
}

func TestTrackHideMidChunkIsFrames(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := withStatic(replaceFit(layerFillHTML, "fill"), "true")
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.4,
		Render:   scene.RenderCfg{W: 320, H: 180, FPS: 10},
		Sources:  map[string]Source{"cam": {File: "cam.mp4"}},
		Tracks:   map[string]Track{"cam": {Source: "cam", OnEnd: "hide", Segments: []Segment{{From: 0, To: 0.2, Rate: 1}}}},
		Timeline: []Event{{At: 0, Layout: "single", Slots: map[string]string{"center": "cam"}}},
	})
	writeCodedMP4(t, filepath.Join(p.Dir, "cam.mp4"), 320, 180, 10, 1)
	if err := os.WriteFile(filepath.Join(p.Dir, "film.html"), []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Templates = map[string]scene.TemplateRef{"film": {Entry: "film.html"}}
	ref := p.Presentations["show"]
	ref.Template = "film"
	p.Presentations["show"] = ref
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	tr := plan.Tracks["cam"]
	if tr.OnEnd != "hide" || tr.Duration != 0.2 {
		t.Fatalf("track %+v duration=%v", tr.Track, tr.Duration)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var log bytes.Buffer
	auto := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "auto.mp4"), &log)
	})
	if bytes.Contains(log.Bytes(), []byte("layered\n")) {
		t.Fatal(log.String())
	}
	useTempCache(t)
	resetSeams()
	layerMode = "layered"
	err = Render(ctx, plan, filepath.Join(t.TempDir(), "forced.mp4"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "track not visible") {
		t.Fatalf("want track not visible, got %v", err)
	}
	useTempCache(t)
	resetSeams()
	layerMode = "frames"
	frames := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "frames.mp4"), io.Discard)
	})
	hidden := 0
	for n, shot := range auto {
		if n < 2 {
			continue
		}
		ref, ok := frames[n]
		if !ok {
			t.Fatalf("missing frames shot %d", n)
		}
		if err := samePixels(shot.png, ref.png); err != nil {
			t.Fatalf("n=%d after hide: %v", n, err)
		}
		hidden++
	}
	if hidden < 1 {
		t.Fatal("no frames after hide")
	}
}

func TestStaticLastProbeCSSAnimationUsesFrames(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#222}[data-slot]{position:absolute;overflow:hidden}@keyframes spin{to{transform:rotate(360deg)}}#mark{position:absolute;left:4px;top:4px;animation:spin 1s infinite}</style><script>
window.backstageTemplate={version:1,static:true,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  let h='<div data-slot="center" data-fit="fill" style="left:10%;top:15%;width:80%;height:65%"></div>';
  if(c.localTime>=0.25) h='<div id="mark">x</div>'+h;
  document.body.innerHTML=h;
};
</script>`
	plan := layerSlotPlan(t, 320, 180, 10, 0.4, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out := filepath.Join(t.TempDir(), "out.mp4")
	var log bytes.Buffer
	if err := Render(ctx, plan, out, &log); err != nil {
		t.Fatal(err)
	}
	body := log.String()
	if bytes.Contains(log.Bytes(), []byte("layered\n")) {
		t.Fatal(body)
	}
	if !strings.Contains(body, ">> chunk-0 static template single: css animation; using frames") {
		t.Fatal(body)
	}
	if !strings.Contains(body, "static-violations=1") {
		t.Fatal(body)
	}
}

func TestStaticListDoesNotMarkVisualScene(t *testing.T) {
	requireRenderTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Run("list", func(t *testing.T) {
		useTempCache(t)
		resetSeams()
		defer resetSeams()
		plan := visualStaticScenePlan(t, "['single']")
		r, err := NewRenderer(ctx, plan)
		if err != nil {
			t.Fatal(err)
		}
		static := r.eventStatic(0)
		r.Close()
		if static {
			t.Fatal("visual scene matched static list")
		}
		out := filepath.Join(t.TempDir(), "list.mp4")
		if err = Render(ctx, plan, out, io.Discard); err != nil {
			t.Fatal(err)
		}
		timings := asObject(t, readFactsMap(t, out)["timings"], "timings")
		probe := asObject(t, timings["probe"], "probe")
		if int(asFloat(t, probe["frames"], "probe.frames")) != plan.Frames {
			t.Fatalf("b7 probe %+v want %d", probe, plan.Frames)
		}
	})
	t.Run("true", func(t *testing.T) {
		useTempCache(t)
		resetSeams()
		defer resetSeams()
		plan := visualStaticScenePlan(t, "true")
		r, err := NewRenderer(ctx, plan)
		if err != nil {
			t.Fatal(err)
		}
		static := r.eventStatic(0)
		r.Close()
		if !static {
			t.Fatal("static:true should mark the visual scene")
		}
		out := filepath.Join(t.TempDir(), "true.mp4")
		if err = Render(ctx, plan, out, io.Discard); err != nil {
			t.Fatal(err)
		}
		timings := asObject(t, readFactsMap(t, out)["timings"], "timings")
		probe := asObject(t, timings["probe"], "probe")
		if int(asFloat(t, probe["frames"], "probe.frames")) != 2 {
			t.Fatalf("static probe %+v", probe)
		}
	})
}

func visualStaticScenePlan(t *testing.T, staticDecl string) *Plan {
	t.Helper()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.4,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 10},
		Timeline: []Event{{Scene: "explain"}},
	})
	html := `<!doctype html><meta charset="utf-8"><script>
window.backstageTemplate={version:1,static:` + staticDecl + `,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){document.body.innerHTML='<div id="x">hi</div>';};
</script>`
	if err := os.WriteFile(filepath.Join(p.Dir, "visual.html"), []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestStaticUnknownLayoutInitializeError(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := withStatic(replaceFit(layerFillHTML, "fill"), "['nope']")
	plan := layerSlotPlan(t, 160, 90, 10, 0.4, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, err := NewRenderer(ctx, plan)
	if r != nil {
		r.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("want unknown layout nope, got %v", err)
	}
}

func TestStaticWrongTypeInitializeError(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := withStatic(replaceFit(layerFillHTML, "fill"), "false")
	plan := layerSlotPlan(t, 160, 90, 10, 0.4, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, err := NewRenderer(ctx, plan)
	if r != nil {
		r.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "static must be true or an array of layout names") {
		t.Fatalf("want type error, got %v", err)
	}
}

func TestStaticNonStringListInitializeError(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := withStatic(replaceFit(layerFillHTML, "fill"), "[3]")
	plan := layerSlotPlan(t, 160, 90, 10, 0.4, "single", html, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, err := NewRenderer(ctx, plan)
	if r != nil {
		r.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "static must be true or an array of layout names") {
		t.Fatalf("want type error, got %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "unknown layout") {
		t.Fatalf("non-string must not be unknown layout: %v", err)
	}
}

func TestStaticLayeredGuardIsError(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#222}[data-slot]{position:absolute;overflow:hidden}@keyframes spin{to{transform:rotate(360deg)}}#mark{position:absolute;left:4px;top:4px;animation:spin 1s infinite}</style><script>
window.backstageTemplate={version:1,static:true,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  document.body.innerHTML='<div id="mark">x</div><div data-slot="center" data-fit="fill" style="left:10%;top:15%;width:80%;height:65%"></div>';
};
</script>`
	plan := layerSlotPlan(t, 320, 180, 10, 0.4, "single", html, nil)
	layerMode = "layered"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "layered: css animation") {
		t.Fatalf("want layered guard error, got %v", err)
	}
}

func TestStaticVisualSceneWarning(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.4,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 10},
		Timeline: []Event{{Scene: "explain"}},
	})
	html := `<!doctype html><meta charset="utf-8"><style>@keyframes spin{to{transform:rotate(360deg)}}#mark{position:absolute;animation:spin 1s infinite}</style><script>
window.backstageTemplate={version:1,static:true,layouts:{},captionSlots:[]};
window.render=async function(c){document.body.innerHTML='<div id="mark">x</div>';};
</script>`
	if err := os.WriteFile(filepath.Join(p.Dir, "visual.html"), []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out := filepath.Join(t.TempDir(), "out.mp4")
	var log bytes.Buffer
	if err = Render(ctx, plan, out, &log); err != nil {
		t.Fatal(err)
	}
	body := log.String()
	if !strings.Contains(body, ">> chunk-0 static scene explain: css animation; using frames") {
		t.Fatal(body)
	}
	if strings.Contains(body, "static template") {
		t.Fatal(body)
	}
}
