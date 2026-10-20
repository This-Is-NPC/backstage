package presentation

import (
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

func TestAnimatedOverlayMatchesFrames(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	p := animatedTrackPlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	for iteration := 0; iteration < 3; iteration++ {
		modes := []string{"frames", "auto"}
		if iteration%2 == 1 {
			modes = []string{"auto", "frames"}
		}
		outputs := map[string]string{}
		for _, mode := range modes {
			useTempCache(t)
			layerMode = mode
			out := filepath.Join(t.TempDir(), mode+".mp4")
			outputs[mode] = out
			started := time.Now()
			if err := Render(ctx, p, out, nil); err != nil {
				t.Fatal(err)
			}
			t.Logf("iteration=%d mode=%s seconds=%.6f", iteration, mode, time.Since(started).Seconds())
			if mode == "auto" {
				body, err := os.ReadFile(facts.Path(out))
				if err != nil {
					t.Fatal(err)
				}
				var f struct {
					Timings renderTimings `json:"timings"`
				}
				if err := json.Unmarshal(body, &f); err != nil {
					t.Fatal(err)
				}
				if f.Timings.AnimatedOverlayFrames != 60 || f.Timings.DecodedPNGBytes != 0 {
					t.Fatalf("animation used video screenshots: %+v", f.Timings)
				}
			}
		}
		assertStaticFadePTS(t, outputs["auto"], 60, 30)
		minY, avgY, err := videoPSNR(outputs["frames"], outputs["auto"])
		if err != nil || minY < 45 {
			t.Fatalf("PSNR min=%.2f avg=%.2f err=%v", minY, avgY, err)
		}
		t.Logf("PSNR-Y min=%.2f avg=%.2f", minY, avgY)
	}
}

func animatedTrackPlan(t *testing.T) *Plan {
	t.Helper()
	html := strings.Replace(replaceFit(layerFillHTML, "fill"), "version:1,", "version:1,static:true,", 1)
	p := layerSlotPlan(t, 1280, 720, 30, 2, "single", html, nil)
	t.Cleanup(func() { _ = p.Close() })
	p.Project.Render.Workers = 1
	p.Project.Render.Threads = scene.RenderThreads{Prepare: 4, Filter: 1, Encode: 4}
	p.Text = []TextSpan{{At: 0, End: 2, Text: "A simpler workflow", Slot: "subtitle", FontSize: 32, Entrance: Motion{Effect: "rise", Duration: 2, Ease: "ease-out"}}}
	return p
}

func TestAnimatedOverlayCancellationPreservesExport(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	p := animatedTrackPlan(t)
	useTempCache(t)
	out := filepath.Join(t.TempDir(), "keep.mp4")
	if err := os.WriteFile(out, []byte("prior export"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shots := 0
	screenshotObserver = func(bool) {
		shots++
		if shots == 3 {
			cancel()
		}
	}
	if err := Render(ctx, p, out, nil); err == nil {
		t.Fatal("cancelled animation succeeded")
	}
	if body, err := os.ReadFile(out); err != nil || string(body) != "prior export" {
		t.Fatalf("replaced prior export: %v", err)
	}
	if work := leftoverWork(filepath.Dir(out)); len(work) > 0 {
		t.Fatalf("animation left temporary files: %v", work)
	}
}
