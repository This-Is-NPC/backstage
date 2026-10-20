package presentation

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestCameraCompilesAnchorsAndRecordedRegions(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	region := Region{X: 0.5, W: 0.5, H: 1}
	if err := facts.Write(facts.Path(clip), facts.Facts{Steps: []facts.Step{{ID: "open", Start: 2, End: 4, Region: &region}}}); err != nil {
		t.Fatal(err)
	}
	p := &Plan{Project: &scene.Project{Dir: dir}, Inputs: map[string]string{}, Document: Document{Duration: 5}, Tracks: map[string]CompiledTrack{"demo": {Track: Track{Start: 1, Segments: []Segment{{From: 0, To: 4, Rate: 2}}}, Media: Media{Path: clip}}}}
	var camera Camera
	if err := json.Unmarshal([]byte(`{"keyframes":[{"at":{"step":"open"},"step":"open"}]}`), &camera); err != nil {
		t.Fatal(err)
	}
	c, err := p.compileCamera("demo", camera)
	if err != nil || c.Keyframes[0].At != 2 || c.Keyframes[0].Region != region || c.Keyframes[0].Ease != "cut" {
		t.Fatalf("camera=%+v err=%v", c, err)
	}
	if err := json.Unmarshal([]byte(`"follow"`), &camera); err != nil {
		t.Fatal(err)
	}
	c, err = p.compileCamera("demo", camera)
	if err != nil || len(c.Keyframes) != 1 || c.Keyframes[0].At != 2 {
		t.Fatalf("follow=%+v err=%v", c, err)
	}
	if r := c.region(1); r != (Region{W: 1, H: 1}) {
		t.Fatal("follow invented a region before its first action", r)
	}
}

func TestCameraRejectsInvalidRegionsAndMotion(t *testing.T) {
	p := &Plan{Document: Document{Duration: 5}, Tracks: map[string]CompiledTrack{"demo": {}}}
	for _, keys := range [][]CameraKeyframe{
		{{Region: Region{X: -0.1, W: 1, H: 1}}},
		{{Region: Region{X: 0.5, W: 1, H: 1}}},
		{{Region: Region{W: 0.01, H: 0.01}}},
		{{Region: Region{W: 1, H: 1}}, {Region: Region{W: 1, H: 1}}},
		{{Region: Region{W: 1, H: 1}}, {At: 2, Region: Region{W: 0.5, H: 1}, Ease: "linear"}},
	} {
		if _, err := p.compileCamera("demo", Camera{Keyframes: keys}); err == nil {
			t.Fatalf("accepted %+v", keys)
		}
	}
	var c Camera
	if err := json.Unmarshal([]byte(`{"keyframes":[{"at":0,"region":{"w":1,"h":1},"typo":true}]}`), &c); err == nil {
		t.Fatal("accepted unknown camera field")
	}
}

func TestCameraEasingAndCuts(t *testing.T) {
	c := Camera{Keyframes: []CameraKeyframe{{Region: Region{W: 1, H: 1}, Ease: "cut"}, {At: 2, Region: Region{X: 0.5, Y: 0.5, W: 0.5, H: 0.5}, Ease: "linear"}}}
	if r := c.region(1); r != (Region{X: 0.25, Y: 0.25, W: 0.75, H: 0.75}) {
		t.Fatal(r)
	}
	c.Keyframes[1].Ease = "ease-out"
	if r := c.region(1); math.Abs(r.X-0.4375) > 1e-9 {
		t.Fatal(r)
	}
	c.Keyframes[1].Ease = "cut"
	if r := c.region(1); r != c.Keyframes[0].Region {
		t.Fatal(r)
	}
}

func TestCameraLayeredMatchesFramesAndSeek(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	c := Camera{Keyframes: []CameraKeyframe{{Region: Region{W: 1, H: 1}}, {At: 1, Region: Region{X: 0.25, Y: 0.25, W: 0.5, H: 0.5}, Ease: "ease-in-out"}, {At: 2.2, Region: Region{X: 0.5, Y: 0.5, W: 0.5, H: 0.5}, Ease: "linear"}, {At: 2.5, Region: Region{X: 0.5, Y: 0.75, W: 0.5, H: 0.25}}}}
	project := writeFixture(t, Document{Version: 1, Duration: 3, Render: scene.RenderCfg{W: 320, H: 180, FPS: 10}, Sources: map[string]Source{"clip": {File: "clip.mp4"}}, Tracks: map[string]Track{"demo": {Source: "clip", Camera: c}}, Timeline: []Event{{Layout: "single", Slots: map[string]string{"center": "demo"}}}})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := run(ctx, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "nullsrc=size=320x180:rate=10:duration=2,geq=lum='40+X*0.4+Y*0.2':cb=100:cr=140", "-c:v", "libx264", "-pix_fmt", "yuv420p", filepath.Join(project.Dir, "clip.mp4")); err != nil {
		t.Fatal(err)
	}
	p, err := Load(project, "show")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.Project.Render.Workers = 1
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
			t.Fatal("camera lost the layered path")
		}
		return out
	}
	for _, from := range []float64{0, 0.4, 2.2} {
		ref, out := render("frames", from), render("auto", from)
		assertStaticFadePTS(t, out, int(math.Round((3-from)*10)), 10)
		assertStaticFadeQuality(t, ref, out)
	}
	// An event override must also change segment keys, without preparing a
	// second copy of the source track.
	cache, err := openRenderCache(ctx, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	s := newSegmentCache(cache, p, "browser", "")
	ch := planChunks(p, 0, p.Frames)[0]
	a, err := s.cacheKey(fullRenderOpts(p), ch)
	if err != nil {
		t.Fatal(err)
	}
	p.Document.Timeline[0].Camera = map[string]Camera{"demo": {Keyframes: []CameraKeyframe{{Region: Region{W: 0.5, H: 0.5}}}}}
	b, err := s.cacheKey(fullRenderOpts(p), ch)
	if err != nil || a == b {
		t.Fatalf("camera override reused segment: %v", err)
	}
}
