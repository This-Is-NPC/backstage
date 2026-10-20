package presentation

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestTimeReferencesUseRecordedStepsAndTrackCuts(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	if err := facts.Write(facts.Path(clip), facts.Facts{Steps: []facts.Step{{ID: "open", Action: "keys", Start: 4, End: 6}}}); err != nil {
		t.Fatal(err)
	}
	p := &Plan{Project: &scene.Project{Dir: dir}, Inputs: map[string]string{}, Document: Document{Duration: 10}, Tracks: map[string]CompiledTrack{"demo": {Track: Track{Start: 1, Segments: []Segment{{From: 2, To: 8, Rate: 2}}}, Media: Media{Path: clip}}}}
	at, err := p.resolveTime(&TimeReference{Step: "open", Track: "demo", Offset: 0.5})
	if err != nil || at != 2.5 {
		t.Fatalf("at=%v err=%v", at, err)
	}
	at, err = p.resolveTime(&TimeReference{Step: "open", Track: "demo", Edge: "end"})
	if err != nil || at != 3 {
		t.Fatalf("end=%v err=%v", at, err)
	}
	contiguous := p.Tracks["demo"]
	contiguous.Segments = []Segment{{From: 2, To: 6, Rate: 2}, {From: 6, To: 8, Rate: 1}}
	p.Tracks["demo"] = contiguous
	if at, err := p.resolveTime(&TimeReference{Step: "open", Track: "demo", Edge: "end"}); err != nil || at != 3 {
		t.Fatalf("boundary end=%v err=%v", at, err)
	}
	if _, err = p.resolveTime(&TimeReference{Step: "missing", Track: "demo"}); err == nil {
		t.Fatal("accepted missing step")
	}
	tr := p.Tracks["demo"]
	tr.Segments = append(tr.Segments, tr.Segments[0])
	p.Tracks["demo"] = tr
	if _, err = p.resolveTime(&TimeReference{Step: "open", Track: "demo"}); err == nil {
		t.Fatal("accepted ambiguous repeated step")
	}
}

func TestReferenceDecodingPreservesStrictFields(t *testing.T) {
	for _, body := range []string{`{"at":{"step":"open","typo":1},"layout":"single"}`, `{"at":{"step":"open","cue":"cue"},"layout":"single"}`, `{"at":0,"typo":true}`, `{"at":{"step":"open"},"typo":true}`} {
		var e Event
		if err := json.Unmarshal([]byte(body), &e); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
	var e Event
	if err := json.Unmarshal([]byte(`{"at":{"step":"open","offset":0.5},"layout":"single"}`), &e); err != nil || e.atRef == nil {
		t.Fatalf("reference: %+v %v", e, err)
	}
}

func TestCardsEnforceReadingTimeAndThemeMinimums(t *testing.T) {
	for _, card := range []Card{{Type: "title", Title: "Too many words to read in one second"}, {Type: "cta", Title: "Try"}, {Type: "unknown", Title: "Title"}, {Type: "badge", Title: "New", Strike: true}} {
		p := writeFixture(t, Document{Version: 1, Duration: 1, Timeline: []Event{{Card: &card}}})
		if _, err := Load(p, "show"); err == nil {
			t.Fatalf("accepted %+v", card)
		}
	}
	p := writeFixture(t, Document{Version: 1, Duration: 4, Theme: &Theme{MinFont: 50, TextSize: 20}, Timeline: []Event{{Card: &Card{Type: "title", Title: "Hello"}}}})
	if _, err := Load(p, "show"); err == nil {
		t.Fatal("accepted undersized theme font")
	}
}

func TestGraphicsRenderMatchesFramesAndSeeks(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	project := writeFixture(t, Document{Version: 1, Duration: 4, Render: scene.RenderCfg{W: 320, H: 180, FPS: 10}, Theme: &Theme{MinFont: 12, TitleSize: 32, TextSize: 16, CaptionSize: 16}, Timeline: []Event{{Card: &Card{Type: "statement", Title: "Old workflow", Strike: true}}, {At: 2, Card: &Card{Type: "badge", Title: "New", Entrance: Motion{Effect: "scale"}}}}, Captions: []Caption{{Scene: "explain", Cue: "cue", Clock: "presentation", At: 0.5, Duration: 1.5, Slot: "subtitle", Entrance: Motion{Effect: "type", Duration: 0.3}}}})
	plan, err := Load(project, "show")
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	plan.Project.Render.Workers = 1
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	render := func(mode string) string {
		t.Helper()
		useTempCache(t)
		layerMode = mode
		out := filepath.Join(t.TempDir(), mode+".mp4")
		var log bytes.Buffer
		if err := Render(ctx, plan, out, &log); err != nil {
			t.Fatalf("%s: %v\n%s", mode, err, log.String())
		}
		if mode == "auto" && !strings.Contains(log.String(), " layered\n") {
			t.Fatal("stable graphics did not use layered rendering")
		}
		return out
	}
	ref, out := render("frames"), render("auto")
	assertStaticFadePTS(t, out, plan.Frames, plan.FPS)
	assertStaticFadeQuality(t, ref, out)
	preview := filepath.Join(t.TempDir(), "preview.mp4")
	if err := writePreview(ctx, plan, preview, os.Stdout, PreviewOpts{From: 0.5, To: 2.5, Scale: 1}); err != nil {
		t.Fatal(err)
	}
	assertStaticFadePTS(t, preview, 20, 10)
}

func TestLintRejectsUndersizedTemplateCaptions(t *testing.T) {
	requireRenderTest(t)
	p := writeFixture(t, Document{Version: 1, Duration: 2, Render: scene.RenderCfg{W: 320, H: 180, FPS: 10}, Timeline: []Event{{Layout: "single"}}, Captions: []Caption{{Scene: "explain", Cue: "cue", Clock: "presentation", At: 0, Duration: 2, Slot: "subtitle"}}})
	template := filepath.Join(p.Dir, "small.html")
	body := `<div data-caption-slot="subtitle" style="font-size:12px"></div><script>window.backstageTemplate={version:1,layouts:{single:[]}};window.render=async()=>{};</script>`
	if err := os.WriteFile(template, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Templates = map[string]scene.TemplateRef{"small": {Entry: "small.html"}}
	p.Presentations["show"] = scene.PresentationRef{File: "show.json", Template: "small"}
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := Lint(ctx, plan); err == nil || !strings.Contains(err.Error(), "12px") {
		t.Fatalf("lint error: %v", err)
	}
}
