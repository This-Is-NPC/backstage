package presentation

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestCompositionResourcesResolveWithoutMutatingScene(t *testing.T) {
	p := &scene.Project{Dir: t.TempDir()}
	c := &scene.Composition{Width: 160, Height: 90, Stylesheets: []string{"style.css"}, Nodes: []scene.VisualNode{{Children: []scene.VisualNode{{Tag: "img", Attributes: map[string]string{"src": "logo.png", "alt": "Logo"}}}}}}
	resolved, err := compositionAssets(p, "http://localhost/token/event-0/", c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(resolved.Stylesheets[0], "/asset/style.css") || !strings.HasSuffix(resolved.Nodes[0].Children[0].Attributes["src"], "/asset/logo.png") {
		t.Fatalf("unresolved resources: %+v", resolved)
	}
	if c.Stylesheets[0] != "style.css" || c.Nodes[0].Children[0].Attributes["src"] != "logo.png" {
		t.Fatal("mutated authored scene")
	}
	c.Nodes[0].Children[0].Attributes["src"] = "../outside.png"
	if _, err = compositionAssets(p, "http://localhost/", c); err == nil {
		t.Fatal("accepted escaped image")
	}
	if embeddedWebHashes()["composition.html"] == "" {
		t.Fatal("composition renderer missing from cache and facts hashes")
	}
	c.Nodes[0].Children[0].Attributes["src"] = "logo.png"
	cache := &segmentCache{plan: &Plan{Scenes: map[string]*scene.Scene{"visual": {Composition: c}}}}
	before, err := cache.eventEntryHash(Event{Scene: "visual"})
	if err != nil {
		t.Fatal(err)
	}
	c.Nodes[0].Children[0].Attributes["alt"] = "Updated logo"
	after, err := cache.eventEntryHash(Event{Scene: "visual"})
	if err != nil || before == after {
		t.Fatalf("changed scene data reused a cache entry: %v", err)
	}
}

func TestCompositionRendersVisibilityMotionAndBackwardSeeks(t *testing.T) {
	requireRenderTest(t)
	p := writeFixture(t, Document{Version: 1, Duration: 2, Render: scene.RenderCfg{W: 160, H: 90, FPS: 10}, Timeline: []Event{{Scene: "explain"}}})
	if err := os.WriteFile(filepath.Join(p.Dir, "style.css"), []byte("body{background:#000}.moving{background:var(--surface)}"), 0600); err != nil {
		t.Fatal(err)
	}
	im := image.NewRGBA(image.Rect(0, 0, 1, 1))
	im.Set(0, 0, color.RGBA{R: 255, A: 255})
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, im); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, "red.png"), imageData.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	c := &scene.Composition{Width: 160, Height: 90, Stylesheets: []string{"style.css"}, Nodes: []scene.VisualNode{{Tag: "img", Start: 0.2, End: 1.2, Attributes: map[string]string{"src": "red.png", "class": "moving"}, Style: map[string]string{"position": "absolute", "top": "10px", "left": "0px", "width": "20px", "height": "20px", "--surface": "red"}, Animate: []scene.VisualAnimation{{Property: "transform", Keyframes: []scene.VisualKeyframe{{At: 0.2, Value: "translateX(0px)"}, {At: 1.2, Value: "translateX(100px)"}}}}}, {Tag: "svg", Attributes: map[string]string{"width": "160", "height": "90"}, Children: []scene.VisualNode{{Tag: "circle", Attributes: map[string]string{"cx": "120", "cy": "60", "r": "10", "fill": "#00ff00"}}}}}}
	body, err := json.Marshal(scene.Scene{Type: "visual", Duration: 2, Composition: c})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(p.Dir, "scenes/explain.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	r, err := NewRenderer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	shot := func(at float64) []byte {
		t.Helper()
		b, e := r.frame(at, nil)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	assertPixel := func(b []byte, x, y int, want color.RGBA) {
		t.Helper()
		im, e := png.Decode(bytes.NewReader(b))
		if e != nil {
			t.Fatal(e)
		}
		if got := color.RGBAModel.Convert(im.At(x, y)).(color.RGBA); got != want {
			t.Fatalf("pixel %d,%d: got %v want %v", x, y, got, want)
		}
	}
	black := color.RGBA{A: 255}
	red := color.RGBA{R: 255, A: 255}
	assertPixel(shot(0.1), 10, 20, black)
	middle := shot(0.7)
	assertPixel(middle, 60, 20, red)
	assertPixel(middle, 10, 20, black)
	assertPixel(middle, 120, 60, color.RGBA{G: 255, A: 255})
	assertPixel(shot(1.2), 110, 20, black)
	if err = samePixels(middle, shot(0.7)); err != nil {
		t.Fatalf("backward seek changed pixels: %v", err)
	}
	files := r.filesForEvents(0)
	if files["style.css"] == "" || files["red.png"] == "" {
		t.Fatalf("resources absent from cache manifest: %v", files)
	}
}
