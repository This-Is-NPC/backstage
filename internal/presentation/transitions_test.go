package presentation

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestTransitionValidation(t *testing.T) {
	for _, tr := range []Transition{{Effect: "spin"}, {Effect: "fade", Direction: "left"}, {Effect: "push", Direction: "diagonal"}} {
		if tr.validate() == nil {
			t.Fatalf("accepted %+v", tr)
		}
	}
	for _, effect := range []string{"slide", "push", "wipe", "zoom", "blur", "fade", "morph"} {
		if err := (Transition{Effect: effect}).validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStaticTransitionsMatchFramesAndSeek(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	for _, effect := range []string{"push", "wipe"} {
		for _, direction := range []string{"left", "right", "up", "down"} {
			t.Run(effect+"-"+direction, func(t *testing.T) {
				project := writeFixture(t, Document{Version: 1, Duration: 3, Render: scene.RenderCfg{W: 320, H: 180, FPS: 10}, Theme: &Theme{MinFont: 12, TitleSize: 36, TextSize: 20, CaptionSize: 16}, Timeline: []Event{{Card: &Card{Type: "title", Title: "Before", Entrance: Motion{Effect: "none"}}}, {At: 1.5, Transition: Transition{Effect: effect, Duration: 0.5, Direction: direction}, Card: &Card{Type: "title", Title: "After", Entrance: Motion{Effect: "none"}}}}})
				plan, err := Load(project, "show")
				if err != nil {
					t.Fatal(err)
				}
				defer plan.Close()
				plan.Project.Render.Workers = 1
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				render := func(mode string, from float64) string {
					t.Helper()
					useTempCache(t)
					layerMode = mode
					out := filepath.Join(t.TempDir(), mode+".mp4")
					var log bytes.Buffer
					if err := writePreview(ctx, plan, out, &log, PreviewOpts{From: from, To: 2.5, Scale: 1}); err != nil {
						t.Fatalf("%v\n%s", err, log.String())
					}
					if mode == "auto" && !strings.Contains(log.String(), " layered\n") {
						t.Fatal("missing static fast path")
					}
					return out
				}
				ref, out := render("frames", 1.5), render("auto", 1.5)
				assertStaticFadePTS(t, out, 10, 10)
				assertStaticFadeQuality(t, ref, out)
				ref, out = render("frames", 1.7), render("auto", 1.7)
				assertStaticFadePTS(t, out, 8, 10)
				assertStaticFadeQuality(t, ref, out)
			})
		}
	}
}

func TestFrameTransitionsRenderAndSeek(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	for _, effect := range []string{"slide", "zoom", "blur"} {
		t.Run(effect, func(t *testing.T) {
			p := staticFadePlan(t, 10, 0.5)
			p.Document.Timeline[1].Transition.Effect = effect
			p.Project.Render.Workers = 1
			useTempCache(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			out := filepath.Join(t.TempDir(), effect+".mp4")
			if err := writePreview(ctx, p, out, io.Discard, PreviewOpts{From: p.Document.Timeline[1].At + 0.2, To: p.Document.Duration, Scale: 1}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
