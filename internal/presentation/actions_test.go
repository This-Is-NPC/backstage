package presentation

import (
	"bytes"
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestActionOverlaysMapCutsAndDisableMissingPointer(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	if err := facts.Write(facts.Path(clip), facts.Facts{Steps: []facts.Step{{ID: "open", Action: "keys", Start: 2, End: 4, Keys: []string{"ctrl+s"}}}}); err != nil {
		t.Fatal(err)
	}
	p := &Plan{Project: &scene.Project{Dir: dir}, Inputs: map[string]string{}, Document: Document{Duration: 5, Actions: []ActionOverlay{{Track: "demo", Keys: true, Cursor: true}}}, Tracks: map[string]CompiledTrack{"demo": {Track: Track{Start: 1, Segments: []Segment{{To: 4, Rate: 2}, {From: 2, To: 4, Rate: 1}}}, Media: Media{Path: clip}}}}
	if err := p.compileActions(); err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 2 || p.Actions[0].At != 2 || p.Actions[0].End != 3 || p.Actions[1].At != 3 || p.Actions[1].End != 5 || p.Actions[0].Pointer != nil || len(p.Actions[0].Keys) != 1 {
		t.Fatalf("actions=%+v", p.Actions)
	}
	p.Document.Actions[0].Highlight = "outline"
	p.Document.Actions[0].Step = "open"
	if err := p.compileActions(); err == nil {
		t.Fatal("invented a step region")
	}
}

func TestPointerTraceMapsRepeatedCutsAndRejectsInvalidEvidence(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	recorded := facts.Facts{PointerTrace: []facts.PointerSample{{At: 0, Pointer: &facts.Pointer{X: .1, Y: .2}}, {At: .05, Pointer: &facts.Pointer{X: .3, Y: .4, Click: true}}, {At: .1}, {At: .15, Pointer: &facts.Pointer{X: .7, Y: .8}}}}
	if err := facts.Write(facts.Path(clip), recorded); err != nil {
		t.Fatal(err)
	}
	p := &Plan{Project: &scene.Project{Dir: dir}, Inputs: map[string]string{}, Document: Document{Duration: 3, Actions: []ActionOverlay{{Track: "demo", Cursor: true}}}, Tracks: map[string]CompiledTrack{"demo": {Track: Track{Start: 1, Segments: []Segment{{To: .15, Rate: 2}, {From: .05, To: .15, Rate: .5}}}, Media: Media{Path: clip, Duration: .15}}}}
	if err := p.compileActions(); err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 2 || p.Actions[0].At != 1 || p.Actions[0].End != 1.075 || p.Actions[0].Rate != 2 || p.Actions[1].SourceStart != .05 || p.Actions[1].Rate != .5 {
		t.Fatalf("actions=%+v", p.Actions)
	}
	recorded.PointerTrace[2].At = .04
	if err := facts.Write(facts.Path(clip), recorded); err != nil {
		t.Fatal(err)
	}
	if err := p.compileActions(); err == nil {
		t.Fatal("accepted an unordered trace")
	}
}

func TestPointerTraceInterpolationGapsAndClickSeek(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	p := layerSlotPlan(t, 320, 180, 20, 1, "single", strings.Replace(replaceFit(layerFillHTML, "contain"), "version:1,", "version:1,static:true,", 1), nil)
	defer p.Close()
	p.Actions = []ActionSpan{{Track: "cam", At: 0, End: 1, Rate: 1, Trace: []facts.PointerSample{
		{At: 0, Pointer: &facts.Pointer{X: .2, Y: .5}},
		{At: .1, Pointer: &facts.Pointer{X: .8, Y: .5, Click: true}},
		{At: .2, Pointer: &facts.Pointer{X: .8, Y: .5}},
		{At: .25},
		{At: .4, Pointer: &facts.Pointer{X: .4, Y: .5}},
		{At: .5, Pointer: &facts.Pointer{X: .6, Y: .5}},
	}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	r, err := NewRenderer(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, check := range []struct {
		at         float64
		count      int
		x, opacity float64
	}{{.05, 1, 160, 1}, {.15, 1, 222.4, 1 - .05/.35}, {.3, 0, 0, 0}, {.45, 1, 160, 1}, {.8, 0, 0, 0}} {
		if _, err := r.frame(check.at, nil); err != nil {
			t.Fatal(err)
		}
		var got struct {
			Count      int
			X, Opacity float64
		}
		if err := r.evalValue("eval", `(()=>{const e=document.querySelector('.action-pointer');if(!e)return {Count:0};const r=e.getBoundingClientRect();return {Count:1,X:r.x+r.width/2,Opacity:Number(e.style.opacity)}})()`, &got); err != nil {
			t.Fatal(err)
		}
		if got.Count != check.count || got.Count > 0 && (math.Abs(got.X-check.x) > 2 || math.Abs(got.Opacity-check.opacity) > .01) {
			t.Fatalf("at %v: %+v, want %+v", check.at, got, check)
		}
	}
	// A moving trace must select animated alpha, including a direct seek.
	useTempCache(t)
	var log bytes.Buffer
	if err := writePreview(ctx, p, filepath.Join(t.TempDir(), "trace.mp4"), &log, PreviewOpts{From: .05, To: .8, Scale: 1}); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), " layered\n") {
		t.Fatal("trace lost layered composition")
	}
}

func TestActionGraphicsMatchFramesAndSeek(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	html := strings.Replace(replaceFit(layerFillHTML, "contain"), "version:1,", "version:1,static:true,", 1)
	p := layerSlotPlan(t, 320, 180, 10, 3, "single", html, nil)
	defer p.Close()
	p.Document.Theme = &Theme{MinFont: 12, TitleSize: 32, TextSize: 16, CaptionSize: 16}
	region := Region{X: 0.25, Y: 0.25, W: 0.5, H: 0.5}
	if err := facts.Write(facts.Path(p.Tracks["cam"].Media.Path), facts.Facts{Steps: []facts.Step{{ID: "save", Action: "keys", Start: 0.5, End: 2.5, Keys: []string{"ctrl+s"}, Region: &region, Pointer: &facts.Pointer{X: 0.5, Y: 0.5, Click: true}}}}); err != nil {
		t.Fatal(err)
	}
	p.Document.Actions = []ActionOverlay{{Track: "cam", Keys: true, Highlight: "spotlight", Cursor: true}}
	if err := p.compileActions(); err != nil {
		t.Fatal(err)
	}
	p.Project.Render.Workers = 1
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	render := func(mode string, from float64) string {
		t.Helper()
		useTempCache(t)
		layerMode = mode
		out := filepath.Join(t.TempDir(), mode+".mp4")
		var log bytes.Buffer
		if err := writePreview(ctx, p, out, &log, PreviewOpts{From: from, To: 3, Scale: 1}); err != nil {
			t.Fatalf("%v\n%s", err, log.String())
		}
		if mode == "auto" && !strings.Contains(log.String(), " layered\n") {
			t.Fatal("stable action graphics lost layered rendering")
		}
		return out
	}
	for _, from := range []float64{0, 0.7} {
		ref, out := render("frames", from), render("auto", from)
		assertStaticFadeQuality(t, ref, out)
		assertStaticFadePTS(t, out, int(math.Round((3-from)*10)), 10)
	}
	// The recorded position must be projected through the same camera crop as
	// the source image, including when seeking directly into the action.
	tr := p.Tracks["cam"]
	tr.Camera = Camera{Keyframes: []CameraKeyframe{{Region: region, Ease: "cut"}}}
	p.Tracks["cam"] = tr
	r, err := NewRenderer(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.frame(1, nil); err != nil {
		t.Fatal(err)
	}
	var position struct{ X, Y float64 }
	if err := r.evalValue("eval", `(()=>{const r=document.querySelector('.action-pointer').getBoundingClientRect();return {X:r.x+r.width/2,Y:r.y+r.height/2}})()`, &position); err != nil {
		t.Fatal(err)
	}
	if position.X < 150 || position.X > 170 || position.Y < 80 || position.Y > 100 {
		t.Fatalf("pointer moved away from source center: %+v", position)
	}
}
