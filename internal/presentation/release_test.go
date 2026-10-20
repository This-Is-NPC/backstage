package presentation

import (
	"context"
	"io"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func releaseDocument() Document {
	return Document{Version: 1, Duration: 12, Render: scene.RenderCfg{W: 320, H: 180, FPS: 10}, Theme: &Theme{MinFont: 12, TitleSize: 32, TextSize: 16, CaptionSize: 16}, Sources: map[string]Source{"demo": {File: "demo.mp4"}}, Tracks: map[string]Track{"demo": {Source: "demo"}}, Release: &Release{Statements: []string{"Before"}, Title: Card{Title: "Update"}, Demos: []ReleaseDemo{{Track: "demo", Duration: 3, Title: "Demo"}}, CTA: Card{Title: "Try it", URL: "example.com"}}}
}

func TestReleaseBuildsTimelineAndStartsDemoAtItsCut(t *testing.T) {
	p := &Plan{Document: releaseDocument(), FPS: 10, Tracks: map[string]CompiledTrack{"demo": {Track: Track{Source: "demo"}, Duration: 3}}}
	if err := p.compileRelease(); err != nil {
		t.Fatal(err)
	}
	if len(p.Document.Timeline) != 4 || p.Document.Timeline[0].Card.Type != "statement" || p.Document.Timeline[1].Card.Type != "title" || p.Document.Timeline[2].Layout != "release" || p.Document.Timeline[3].Card.Type != "cta" {
		t.Fatal(p.Document.Timeline)
	}
	if p.Tracks["demo"].Start != p.Document.Timeline[2].At {
		t.Fatal("demo source ran during the cards")
	}
	if err := p.validateGraphics(); err != nil {
		t.Fatal(err)
	}
	p.Document.Timeline = nil
	p.Document.Duration = 4
	if err := p.compileRelease(); err == nil {
		t.Fatal("accepted a release with no time to read the CTA")
	}
}

func TestReframeKeepsClockAndUsesEvenDimensions(t *testing.T) {
	for _, aspect := range []string{"16:9", "9:16", "1:1"} {
		p := &Plan{Width: 1280, Height: 720, FPS: 30, Frames: 300}
		if err := p.Reframe(aspect); err != nil {
			t.Fatal(err)
		}
		if p.Width%2 != 0 || p.Height%2 != 0 || p.FPS != 30 || p.Frames != 300 {
			t.Fatalf("%s: %+v", aspect, p)
		}
		want := map[string]float64{"16:9": 16.0 / 9, "9:16": 9.0 / 16, "1:1": 1}[aspect]
		if math.Abs(float64(p.Width)/float64(p.Height)-want) > 0.005 {
			t.Fatalf("%s: %dx%d", aspect, p.Width, p.Height)
		}
	}
}

func TestReleaseRendersAllAspectRatios(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	project := writeFixture(t, releaseDocument())
	writeCodedMP4(t, filepath.Join(project.Dir, "demo.mp4"), 320, 180, 10, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, aspect := range []string{"16:9", "9:16", "1:1"} {
		t.Run(aspect, func(t *testing.T) {
			useTempCache(t)
			p, err := Load(project, "show")
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			p.Project.Render.Workers = 1
			if err := p.Reframe(aspect); err != nil {
				t.Fatal(err)
			}
			if err := Lint(ctx, p); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "release.mp4")
			if err := Render(ctx, p, out, io.Discard); err != nil {
				t.Fatal(err)
			}
			assertStaticFadePTS(t, out, p.Frames, p.FPS)
			w, h, err := ffprobeSize(ctx, out)
			if err != nil || w != p.Width || h != p.Height {
				t.Fatalf("size=%dx%d err=%v", w, h, err)
			}
		})
	}
}
