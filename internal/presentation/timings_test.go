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

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func requireRenderTest(t *testing.T) {
	t.Helper()
	if os.Getenv("BACKSTAGE_RENDER_TEST") != "1" {
		t.Skip("set BACKSTAGE_RENDER_TEST=1 with Chromium and FFmpeg installed")
	}
}

func TestHistogramMeanAndP95(t *testing.T) {
	var h msHist
	for range 20 {
		h.add(5 * time.Millisecond)
	}
	for range 80 {
		h.add(10 * time.Millisecond)
	}
	got := h.snapshot()
	if got.Frames != 100 {
		t.Fatalf("frames=%d", got.Frames)
	}
	if got.Seconds != 0.9 {
		t.Fatalf("seconds=%v", got.Seconds)
	}
	if got.MeanSeconds != 0.009 {
		t.Fatalf("mean=%v", got.MeanSeconds)
	}
	if got.MaxSeconds != 0.01 {
		t.Fatalf("max=%v", got.MaxSeconds)
	}
	if got.P95Seconds != 0.011 {
		t.Fatalf("p95=%v want upper edge of 10ms bucket", got.P95Seconds)
	}
}

func TestHistogramMergeSumsCounts(t *testing.T) {
	var a, b msHist
	a.add(5 * time.Millisecond)
	a.add(5 * time.Millisecond)
	b.add(10 * time.Millisecond)
	a.merge(b)
	got := a.snapshot()
	if got.Frames != 3 {
		t.Fatalf("frames=%d", got.Frames)
	}
	if got.MaxSeconds != 0.01 {
		t.Fatalf("max=%v", got.MaxSeconds)
	}
	if got.Seconds != 0.02 {
		t.Fatalf("seconds=%v", got.Seconds)
	}
}

func TestHistogramOverflowReportsMax(t *testing.T) {
	var h msHist
	h.add(1500 * time.Millisecond)
	h.add(2000 * time.Millisecond)
	h.add(1800 * time.Millisecond)
	got := h.snapshot()
	if got.MaxSeconds != 2 {
		t.Fatalf("max=%v", got.MaxSeconds)
	}
	if got.P95Seconds != got.MaxSeconds {
		t.Fatalf("overflow p95=%v want observed max %v, not a bucket edge", got.P95Seconds, got.MaxSeconds)
	}
	if got.P95Seconds <= 1 {
		t.Fatalf("overflow p95=%v is a bucket border, not the max", got.P95Seconds)
	}
}

func writeTinyMedia(t *testing.T, dir string) {
	t.Helper()
	ctx := context.Background()
	if err := run(ctx, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=blue:s=160x90:d=1:r=10", "-pix_fmt", "yuv420p", filepath.Join(dir, "clip.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "aevalsrc=sin(2*PI*(200+1800*t)*t):s=48000:d=1", "-ac", "2", "-c:a", "pcm_s16le", filepath.Join(dir, "tone.wav")); err != nil {
		t.Fatal(err)
	}
}

func timedPlan(t *testing.T) *Plan {
	t.Helper()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.4,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 10},
		Sources:  map[string]Source{"cam": {File: "clip.mp4"}},
		Tracks:   map[string]Track{"cam": {Source: "cam", Start: 0}},
		Timeline: []Event{{At: 0, Layout: "single", Slots: map[string]string{"center": "cam"}}},
		Audio:    []Audio{{ID: "music", File: "tone.wav", From: 0, To: 0.4}},
	})
	writeTinyMedia(t, p.Dir)
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func readFactsMap(t *testing.T, out string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(strings.TrimSuffix(out, filepath.Ext(out)) + ".facts.json")
	if err != nil {
		t.Fatal(err)
	}
	var facts map[string]any
	if err = json.Unmarshal(b, &facts); err != nil {
		t.Fatal(err)
	}
	return facts
}

func asObject(t *testing.T, v any, name string) map[string]any {
	t.Helper()
	obj, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s: %T", name, v)
	}
	return obj
}

func asFloat(t *testing.T, v any, name string) float64 {
	t.Helper()
	n, ok := v.(float64)
	if !ok {
		t.Fatalf("%s: %T %v", name, v, v)
	}
	return n
}

func TestRenderRecordsTimingsAndBytes(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	plan := timedPlan(t)
	out := filepath.Join(t.TempDir(), "show.mp4")
	var log bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := Render(ctx, plan, out, &log); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log.Bytes(), []byte(">> render timings:")) {
		t.Fatalf("missing timings line:\n%s", log.String())
	}
	facts := readFactsMap(t, out)
	for _, key := range []string{"version", "presentation", "configuration", "render", "inputs", "tools", "timings"} {
		if _, ok := facts[key]; !ok {
			t.Fatalf("facts missing %s", key)
		}
	}
	if asFloat(t, facts["version"], "version") != 1 {
		t.Fatal(facts["version"])
	}
	if _, ok := facts["render"].(map[string]any); !ok {
		t.Fatalf("render: %T", facts["render"])
	}
	timings := asObject(t, facts["timings"], "timings")
	for _, key := range []string{
		"renderer-start-seconds", "decoder-start-seconds", "prepare-track-seconds", "audio-seconds", "audio-part-seconds",
		"decode", "transfer", "draw", "screenshot", "encode-write",
		"encode-seconds", "mux-seconds", "metadata-seconds", "total-seconds", "workers",
		"track-bytes", "audio-part-bytes", "mix-wav-bytes", "video-mp4-bytes", "final-mp4-bytes",
		"decoded-png-bytes", "screenshot-bytes", "cache-hits", "cache-misses",
	} {
		if _, ok := timings[key]; !ok {
			t.Fatalf("timings missing %s", key)
		}
	}
	if int(asFloat(t, timings["workers"], "workers")) < 1 {
		t.Fatal("workers")
	}
	chunks, ok := timings["chunk-seconds"].([]any)
	if !ok || len(chunks) < 1 {
		t.Fatalf("chunk-seconds: %T %v", timings["chunk-seconds"], timings["chunk-seconds"])
	}
	if asFloat(t, timings["final-mp4-bytes"], "final-mp4-bytes") <= 0 {
		t.Fatal("final-mp4-bytes")
	}
	if asFloat(t, timings["video-mp4-bytes"], "video-mp4-bytes") <= 0 {
		t.Fatal("video-mp4-bytes")
	}
	if asFloat(t, timings["decoded-png-bytes"], "decoded-png-bytes") <= 0 {
		t.Fatal("decoded-png-bytes")
	}
	if asFloat(t, timings["screenshot-bytes"], "screenshot-bytes") <= 0 {
		t.Fatal("screenshot-bytes")
	}
	tracks := asObject(t, timings["prepare-track-seconds"], "prepare-track-seconds")
	if _, ok := tracks["cam"]; !ok {
		t.Fatal(tracks)
	}
	sizes := asObject(t, timings["track-bytes"], "track-bytes")
	if asFloat(t, sizes["cam"], "track-bytes.cam") <= 0 {
		t.Fatal(sizes)
	}
	parts, ok := timings["audio-part-seconds"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("audio-part-seconds: %T %v", timings["audio-part-seconds"], timings["audio-part-seconds"])
	}
	part := asObject(t, parts[0], "audio-part")
	if part["id"] != "music" {
		t.Fatal(part)
	}
	for _, key := range []string{"decode", "transfer", "draw", "screenshot", "encode-write"} {
		phase := asObject(t, timings[key], key)
		for _, field := range []string{"seconds", "mean-seconds", "max-seconds", "p95-seconds", "frames"} {
			if _, ok := phase[field]; !ok {
				t.Fatalf("%s missing %s", key, field)
			}
		}
		if int(asFloat(t, phase["frames"], key+".frames")) != plan.Frames {
			t.Fatalf("%s frames=%v want %d", key, phase["frames"], plan.Frames)
		}
	}
}

func TestDrawTimedMatchesDrawScreenshots(t *testing.T) {
	requireRenderTest(t)
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 1,
		Render:   scene.RenderCfg{W: 320, H: 180, FPS: 10},
		Timeline: []Event{
			{At: 0, Scene: "explain"},
			{At: 0.4, Scene: "explain", Transition: Transition{Effect: "fade", Duration: 0.4}},
		},
	})
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	r, err := NewRenderer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	mid := false
	for n := 0; n < plan.Frames; n++ {
		at := float64(n) / float64(plan.FPS)
		if at > 0.4 && at < 0.8 {
			mid = true
		}
		plain, err := r.frame(at, nil)
		if err != nil {
			t.Fatal(err)
		}
		timed, _, err := r.frameTimed(at, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(plain, timed) {
			t.Fatalf("frame %d t=%v: draw and drawTimed screenshots differ", n, at)
		}
	}
	if !mid {
		t.Fatal("plan had no mid-transition frame")
	}
}
