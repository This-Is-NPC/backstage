package presentation

import (
	"bytes"
	"context"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestRealPresentation(t *testing.T) {
	if os.Getenv("BACKSTAGE_RENDER_TEST") != "1" {
		t.Skip("set BACKSTAGE_RENDER_TEST=1 with Chromium and FFmpeg installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	p, err := scene.LoadProject("../../examples/presentation/backstage.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"complete", "short"} {
		plan, err := Load(p, name)
		if err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), name+".mp4")
		if err = Render(ctx, plan, out, os.Stdout); err != nil {
			t.Fatal(err)
		}
		// Inspect the rendered pixels near the destination, not just duration:
		// this catches a timeline ending before the three-second arrow completes.
		tail, extractErr := command(ctx, "ffmpeg", "-v", "error", "-ss", num(plan.Document.Duration-2/float64(plan.FPS)), "-i", out, "-frames:v", "1", "-f", "image2pipe", "-c:v", "png", "-").Output()
		if extractErr != nil {
			t.Fatal(extractErr)
		}
		frame, decodeErr := png.Decode(bytes.NewReader(tail))
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		red, green, blue, _ := frame.At(plan.Width*1200/1920, plan.Height*490/1080).RGBA()
		if red>>8 < 90 || green>>8 < 120 || blue>>8 < 200 {
			t.Fatalf("final arrow is incomplete: pixel RGB %d,%d,%d", red>>8, green>>8, blue>>8)
		}
		m, err := probe(out)
		if err != nil {
			t.Fatal(err)
		}
		if !m.HasVideo || !m.HasAudio || m.Duration < plan.Document.Duration-0.1 || m.Duration > plan.Document.Duration+0.1 {
			t.Fatalf("unexpected export: %+v", m)
		}
	}
}

func TestVisualSeekAndSandbox(t *testing.T) {
	if os.Getenv("BACKSTAGE_RENDER_TEST") != "1" {
		t.Skip("set BACKSTAGE_RENDER_TEST=1")
	}
	p := writeFixture(t, Document{Version: 1, Duration: 2, Render: scene.RenderCfg{W: 320, H: 180, FPS: 12}, Timeline: []Event{{Scene: "explain"}}})
	html, err := web.ReadFile("web/visual.html")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(p.Dir, "visual.html"), html, 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := NewRenderer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	first, err := r.frame(0.75, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.frame(1.5, nil); err != nil {
		t.Fatal(err)
	}
	again, err := r.frame(0.75, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, again) {
		t.Fatal("seeking changed frame pixels")
	}
	resp, err := http.Get(r.url + "asset/backstage.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatal("served non-asset project file")
	}
}

func TestCancelledRenderPreservesExistingOutput(t *testing.T) {
	if os.Getenv("BACKSTAGE_RENDER_TEST") != "1" {
		t.Skip("set BACKSTAGE_RENDER_TEST=1")
	}
	p := writeFixture(t, Document{Version: 1, Duration: 2, Render: scene.RenderCfg{W: 320, H: 180, FPS: 12}, Timeline: []Event{{Scene: "explain"}}})
	if err := os.WriteFile(filepath.Join(p.Dir, "visual.html"), []byte(`<script>window.backstageTemplate={version:1};window.render=async()=>new Promise(()=>{});</script>`), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "existing.mp4")
	if err = os.WriteFile(out, []byte("keep existing output"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = Render(ctx, plan, out, nil); err == nil {
		t.Fatal("render ignored cancellation")
	}
	b, err := os.ReadFile(out)
	if err != nil || string(b) != "keep existing output" {
		t.Fatal("replaced original output", err)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatal("left render intermediates", files)
	}
}
