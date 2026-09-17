package presentation

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

type frameShot struct {
	t   float64
	png []byte
}

func collectShots(t *testing.T, fn func() error) map[int]frameShot {
	t.Helper()
	prev := observeFrame
	out := map[int]frameShot{}
	observeFrame = func(n int, at float64, png []byte) {
		out[n] = frameShot{t: at, png: append([]byte(nil), png...)}
	}
	t.Cleanup(func() { observeFrame = prev })
	if err := fn(); err != nil {
		t.Fatal(err)
	}
	return out
}

func writeVisual(t *testing.T, dir string) {
	t.Helper()
	html, err := web.ReadFile("web/visual.html")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "visual.html"), html, 0o600); err != nil {
		t.Fatal(err)
	}
}

func captionSwapPlan(t *testing.T) *Plan {
	t.Helper()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.4,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 10},
		Timeline: []Event{{At: 0, Scene: "explain"}},
		Captions: []Caption{
			{Scene: "explain", Cue: "one", Clock: "presentation", At: 0, Duration: 0.2, Slot: "subtitle"},
			{Scene: "explain", Cue: "two", Clock: "presentation", At: 0.2, Duration: 0.2, Slot: "subtitle"},
		},
	})
	if err := os.WriteFile(filepath.Join(p.Dir, "scenes", "explain.json"), []byte(`{"type":"visual","entry":"visual.html","duration":10,"narration":{"cues":[{"id":"one","start":0,"end":1,"text":"ONE"},{"id":"two","start":1,"end":2,"text":"TWO"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeVisual(t, p.Dir)
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func freezeTailPlan(t *testing.T) *Plan {
	t.Helper()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.6,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 10},
		Sources:  map[string]Source{"cam": {File: "clip.mp4"}},
		Tracks:   map[string]Track{"cam": {Source: "cam", Segments: []Segment{{From: 0, To: 0.25, Rate: 1}}}},
		Timeline: []Event{{At: 0, Layout: "single", Slots: map[string]string{"center": "cam"}}},
		Audio:    []Audio{{ID: "music", File: "tone.wav", From: 0, To: 0.6}},
	})
	writeTinyMedia(t, p.Dir)
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func oddScalePlan(t *testing.T) *Plan {
	t.Helper()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.2,
		Render:   scene.RenderCfg{W: 162, H: 90, FPS: 10},
		Timeline: []Event{{At: 0, Scene: "explain"}},
	})
	writeVisual(t, p.Dir)
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestPreviewIntervalExactPixels(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cases := []struct {
		name     string
		plan     *Plan
		from, to float64
	}{
		{"fade", multiTrackPlan(t), 0.22, 0.35},
		{"caption", captionSwapPlan(t), 0.2, 0.35},
		{"freeze", freezeTailPlan(t), 0.4, 0.6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			full := collectShots(t, func() error {
				return Render(ctx, c.plan, filepath.Join(t.TempDir(), "full.mp4"), io.Discard)
			})
			opts := PreviewOpts{From: c.from, To: c.to, Scale: 1}
			ro, err := renderOptsFromPreview(c.plan, opts)
			if err != nil {
				t.Fatal(err)
			}
			var log bytes.Buffer
			part := collectShots(t, func() error {
				return writePreview(ctx, c.plan, filepath.Join(t.TempDir(), "part.mp4"), &log, opts)
			})
			if got, want := shotIndexes(part), frameRange(ro.First, ro.End); !indexesEqual(got, want) {
				t.Fatalf("captured n=%v want %v", got, want)
			}
			if !strings.Contains(log.String(), fmt.Sprintf(">> render 0/%d frames", ro.End-ro.First)) {
				t.Fatal(log.String())
			}
			if !strings.Contains(log.String(), "decoder-start=") || strings.Contains(log.String(), ">> decoder-start") {
				t.Fatal(log.String())
			}
			for n, shot := range part {
				ref, ok := full[n]
				if !ok {
					t.Fatalf("missing full frame %d", n)
				}
				if shot.t != float64(n)/float64(c.plan.FPS) {
					t.Fatalf("t=%v want absolute %v", shot.t, float64(n)/float64(c.plan.FPS))
				}
				if shot.t == float64(n-ro.First)/float64(c.plan.FPS) && ro.First != 0 {
					t.Fatalf("relative clock at n=%d first=%d", n, ro.First)
				}
				if err = samePixels(shot.png, ref.png); err != nil {
					t.Fatalf("n=%d t=%v: %v", n, shot.t, err)
				}
			}
		})
	}
}

func TestPreviewIntervalExportAndAudio(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	plan := freezeTailPlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	full := filepath.Join(t.TempDir(), "full.mp4")
	if err := Render(ctx, plan, full, io.Discard); err != nil {
		t.Fatal(err)
	}
	opts := PreviewOpts{From: 0.4, To: 0.6, Scale: 1}
	ro, err := renderOptsFromPreview(plan, opts)
	if err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(t.TempDir(), "part.mp4")
	if err = writePreview(ctx, plan, part, io.Discard, opts); err != nil {
		t.Fatal(err)
	}
	assertIntervalVideo(t, part, ro, plan.FPS)
	ref := filepath.Join(t.TempDir(), "ref.mp4")
	if err = extractInterval(ctx, full, ref, ro.First, ro.End); err != nil {
		t.Fatal(err)
	}
	minY, avgY, err := videoPSNR(ref, part)
	if err != nil {
		t.Fatal(err)
	}
	if minY < 40 || avgY < 45 {
		t.Fatalf("PSNR-Y min=%.2f avg=%.2f", minY, avgY)
	}
	facts := readFactsMap(t, part)
	if _, ok := facts["from"]; ok {
		t.Fatal("interval field on facts")
	}
	render := asObject(t, facts["render"], "render")
	if int(asFloat(t, render["frames"], "frames")) != plan.Frames {
		t.Fatal(render["frames"])
	}
	if _, ok := render["scale"]; ok {
		t.Fatal(render)
	}
	timings := asObject(t, facts["timings"], "timings")
	if _, ok := timings["decoder-start-seconds"]; !ok {
		t.Fatal("missing decoder-start-seconds")
	}
	if _, ok := timings["from"]; ok {
		t.Fatal(timings)
	}
	cache, err := openRenderCache(ctx, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	mix, _, err := plan.mix(ctx, t.TempDir(), cache)
	if err != nil {
		t.Fatal(err)
	}
	const startSample, endSample = 19200, 28800
	if ro.First != 4 || ro.End != 6 || plan.FPS != 10 {
		t.Fatalf("literals assume first=4 end=6 fps=10, got %d %d %d", ro.First, ro.End, plan.FPS)
	}
	mixPCM := pcmMono(t, mix)
	partPCM := pcmMono(t, part)
	refPCM := mixPCM[startSample:endSample]
	lag, corr := peakXCorr(refPCM, partPCM, mixSampleRate/200)
	if math.Abs(float64(lag)) > float64(mixSampleRate)/200 || corr < 0.95 {
		t.Fatalf("interval xcorr lag=%d corr=%.3f", lag, corr)
	}
	fullPCM := pcmMono(t, full)
	lag, corr = peakXCorr(mixPCM, fullPCM, mixSampleRate/200)
	if math.Abs(float64(lag)) > float64(mixSampleRate)/200 || corr < 0.95 {
		t.Fatalf("full xcorr lag=%d corr=%.3f", lag, corr)
	}
}

func TestPreviewSecondWriteIsCacheHit(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	plan := timedPlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	opts := PreviewOpts{From: 0, To: plan.Document.Duration, Scale: 1}
	if err := writePreview(ctx, plan, filepath.Join(t.TempDir(), "a.mp4"), io.Discard, opts); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := writePreview(ctx, plan, filepath.Join(t.TempDir(), "b.mp4"), &log, opts); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`cache-misses=0\b`).Match(log.Bytes()) {
		t.Fatal(log.String())
	}
}

func TestDraftScaleKeepsFullClipPNG(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	plan := oddScalePlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, err := NewRenderer(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	r.scale = 0.5
	shot, err := r.frame(0, nil)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(shot))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 81 || img.Bounds().Dy() != 45 {
		t.Fatalf("PNG %dx%d want 81x45 (full clip, not even shrink)", img.Bounds().Dx(), img.Bounds().Dy())
	}
	var saw renderNote
	prev := observeRender
	observeRender = func(n renderNote) {
		if n.EncoderArgs != nil {
			saw = n
		}
	}
	t.Cleanup(func() { observeRender = prev })
	out := filepath.Join(t.TempDir(), "draft.mp4")
	if err = writePreview(ctx, plan, out, io.Discard, PreviewOpts{From: 0, To: plan.Document.Duration, Scale: 0.5}); err != nil {
		t.Fatal(err)
	}
	if !containsArg(saw.EncoderArgs, draftCropFilter()) {
		t.Fatalf("encoder missing crop: %v", saw.EncoderArgs)
	}
	w, h := videoSize(t, out)
	if w != 80 || h != 44 {
		t.Fatalf("MP4 %dx%d want 80x44", w, h)
	}
	n := videoPackets(t, out)
	if n != plan.Frames {
		t.Fatalf("frames %d want %d", n, plan.Frames)
	}
}

func TestScaleOneKeepsTodayEncodeArgs(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	plan := timedPlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var saw renderNote
	prev := observeRender
	observeRender = func(n renderNote) {
		if n.EncoderArgs != nil {
			saw = n
		}
	}
	t.Cleanup(func() { observeRender = prev })
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "full.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if containsArg(saw.EncoderArgs, "-vf") || containsArg(saw.EncoderArgs, draftCropFilter()) {
		t.Fatalf("scale 1 added crop: %v", saw.EncoderArgs)
	}
}

func TestCodedFrameInterval(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	plan := codedFramePlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	opts := PreviewOpts{From: 0.25, To: 0.75, Scale: 1}
	ro, err := renderOptsFromPreview(plan, opts)
	if err != nil {
		t.Fatal(err)
	}
	if ro.First != 3 || ro.End != 9 || plan.FPS != 12 {
		t.Fatalf("literals assume first=3 end=9 fps=12, got %+v fps=%d", ro, plan.FPS)
	}
	full := filepath.Join(t.TempDir(), "full.mp4")
	part := filepath.Join(t.TempDir(), "part.mp4")
	shots := collectShots(t, func() error {
		return writePreview(ctx, plan, part, io.Discard, opts)
	})
	if got, want := shotIndexes(shots), frameRange(ro.First, ro.End); !indexesEqual(got, want) {
		t.Fatalf("captured n=%v want %v", got, want)
	}
	wantCode := map[int]int{3: 3, 4: 4, 5: 5, 6: 6, 7: 7, 8: 8}
	for n := ro.First; n < ro.End; n++ {
		got, err := readFrameCode(shots[n].png)
		if err != nil {
			t.Fatal(err)
		}
		want, ok := wantCode[n]
		if !ok || got != want {
			t.Fatalf("composed n=%d code %d want %d", n, got, want)
		}
	}
	partPNGs := decodeVideoPNGs(t, part, plan.FPS, ro.End-ro.First)
	for i, png := range partPNGs {
		n := ro.First + i
		got, err := readFrameCode(png)
		if err != nil {
			t.Fatal(err)
		}
		if got != wantCode[n] {
			t.Fatalf("interval mp4 i=%d n=%d code %d want %d", i, n, got, wantCode[n])
		}
	}
	if err = Render(ctx, plan, full, io.Discard); err != nil {
		t.Fatal(err)
	}
	ref := filepath.Join(t.TempDir(), "ref.mp4")
	if err = extractInterval(ctx, full, ref, ro.First, ro.End); err != nil {
		t.Fatal(err)
	}
	refPNGs := decodeVideoPNGs(t, ref, plan.FPS, ro.End-ro.First)
	for i, png := range refPNGs {
		n := ro.First + i
		got, err := readFrameCode(png)
		if err != nil {
			t.Fatal(err)
		}
		if got != wantCode[n] {
			t.Fatalf("export select i=%d n=%d code %d want %d", i, n, got, wantCode[n])
		}
	}
	if !strings.Contains(strings.Join(intervalSelectArgs(ro.First, ro.End), " "), "between(n,3,8)") {
		t.Fatal(intervalSelectArgs(ro.First, ro.End))
	}
	minY, avgY, err := videoPSNR(ref, part)
	if err != nil {
		t.Fatal(err)
	}
	if minY < 40 || avgY < 45 {
		t.Fatalf("PSNR-Y min=%.2f avg=%.2f", minY, avgY)
	}
	cache, err := openRenderCache(ctx, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	mix, _, err := plan.mix(ctx, t.TempDir(), cache)
	if err != nil {
		t.Fatal(err)
	}
	const startSample, endSample = 12000, 36000
	lag, corr := peakXCorr(pcmMono(t, mix)[startSample:endSample], pcmMono(t, part), mixSampleRate/200)
	if math.Abs(float64(lag)) > float64(mixSampleRate)/200 || corr < 0.95 {
		t.Fatalf("interval xcorr lag=%d corr=%.3f", lag, corr)
	}
}

func TestFullRenderMuxHasNoAtrim(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	plan := timedPlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var mux []string
	prev := observeRender
	observeRender = func(n renderNote) {
		if n.MuxArgs != nil {
			mux = n.MuxArgs
		}
	}
	t.Cleanup(func() { observeRender = prev })
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "full.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(mux, " ")
	if len(mux) == 0 {
		t.Fatal("mux args not recorded")
	}
	if containsArg(mux, "-filter_complex") || strings.Contains(joined, "atrim") {
		t.Fatalf("full render used interval mux: %v", mux)
	}
	if !containsArg(mux, "-t") || mux[indexOf(mux, "-t")+1] != num(plan.Document.Duration) {
		t.Fatalf("full render -t: %v", mux)
	}
}

func TestCountPacketsOnlyPastEndRender(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Cleanup(func() { observeCountPackets = nil })
	var mid []string
	observeCountPackets = func(path string) { mid = append(mid, path) }
	if err := writePreview(ctx, codedFramePlan(t), filepath.Join(t.TempDir(), "mid.mp4"), io.Discard, PreviewOpts{From: 0.25, To: 0.75, Scale: 1}); err != nil {
		t.Fatal(err)
	}
	if len(mid) != 0 {
		t.Fatalf("mid-track counted packets: %v", mid)
	}
	var last []string
	observeCountPackets = func(path string) { last = append(last, path) }
	if err := writePreview(ctx, codedFramePlan(t), filepath.Join(t.TempDir(), "last.mp4"), io.Discard, PreviewOpts{From: 11.0 / 12.0, To: 1, Scale: 1}); err != nil {
		t.Fatal(err)
	}
	if len(last) != 0 {
		t.Fatalf("last real frame counted packets: %v", last)
	}
	var past []string
	observeCountPackets = func(path string) { past = append(past, path) }
	if err := writePreview(ctx, freezeTailPlan(t), filepath.Join(t.TempDir(), "past.mp4"), io.Discard, PreviewOpts{From: 0.4, To: 0.6, Scale: 1}); err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 {
		t.Fatalf("past end countPackets=%d want 1", len(past))
	}
}

func TestResampleSeekPastPreparedEnd(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	opts := PreviewOpts{From: 1.0 / 3.0, To: 0.4, Scale: 1}
	for _, onEnd := range []string{"freeze", "hide"} {
		t.Run(onEnd, func(t *testing.T) {
			plan := resampleTailPlan(t, onEnd)
			if plan.FPS != 60 {
				t.Fatalf("fps %d", plan.FPS)
			}
			ro, err := renderOptsFromPreview(plan, opts)
			if err != nil {
				t.Fatal(err)
			}
			if ro.First != 20 {
				t.Fatalf("first=%d want 20", ro.First)
			}
			full := collectShots(t, func() error {
				return Render(ctx, plan, filepath.Join(t.TempDir(), "full.mp4"), io.Discard)
			})
			var nPack int
			observeCountPackets = func(string) { nPack++ }
			t.Cleanup(func() { observeCountPackets = nil })
			part := collectShots(t, func() error {
				return writePreview(ctx, plan, filepath.Join(t.TempDir(), "part.mp4"), io.Discard, opts)
			})
			if nPack != 1 {
				t.Fatalf("countPackets=%d want 1", nPack)
			}
			if got, want := shotIndexes(part), frameRange(ro.First, ro.End); !indexesEqual(got, want) {
				t.Fatalf("captured n=%v want %v", got, want)
			}
			for n, shot := range part {
				ref, ok := full[n]
				if !ok {
					t.Fatalf("missing full frame %d", n)
				}
				if err = samePixels(shot.png, ref.png); err != nil {
					t.Fatalf("n=%d: %v", n, err)
				}
			}
		})
	}
}

const fullBleedTemplate = `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#808080}[data-slot=center]{position:absolute;inset:0;overflow:hidden}</style><script>window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:['subtitle']};window.render=async function(c){document.body.innerHTML='<div data-slot="center" data-fit="fill"></div>';};</script>`

func resampleTailPlan(t *testing.T, onEnd string) *Plan {
	t.Helper()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.45,
		Render:   scene.RenderCfg{W: 320, H: 180, FPS: 60},
		Sources:  map[string]Source{"cam": {File: "coded.mp4"}},
		Tracks:   map[string]Track{"cam": {Source: "cam", OnEnd: onEnd, Segments: []Segment{{From: 0.13, To: 0.5, Rate: 1}}}},
		Timeline: []Event{{At: 0, Layout: "single", Slots: map[string]string{"center": "cam"}}},
	})
	writeCodedMP4(t, filepath.Join(p.Dir, "coded.mp4"), 320, 180, 24, 1)
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
	return plan
}

func codedFramePlan(t *testing.T) *Plan {
	t.Helper()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 1,
		Render:   scene.RenderCfg{W: 320, H: 180, FPS: 12},
		Sources:  map[string]Source{"cam": {File: "coded.mp4"}},
		Tracks:   map[string]Track{"cam": {Source: "cam"}},
		Timeline: []Event{{At: 0, Layout: "single", Slots: map[string]string{"center": "cam"}}},
		Audio:    []Audio{{ID: "noise", File: "noise.wav", From: 0, To: 1}},
	})
	writeCodedMP4(t, filepath.Join(p.Dir, "coded.mp4"), 320, 180, 12, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "aevalsrc=sin(2*PI*(200+1800*t)*t):s=48000:d=1", "-ac", "2", "-c:a", "pcm_s16le", filepath.Join(p.Dir, "noise.wav")); err != nil {
		t.Fatal(err)
	}
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
	return plan
}

func shotIndexes(shots map[int]frameShot) []int {
	out := make([]int, 0, len(shots))
	for n := range shots {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

func frameRange(first, end int) []int {
	out := make([]int, 0, end-first)
	for n := first; n < end; n++ {
		out = append(out, n)
	}
	return out
}

func indexesEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertIntervalVideo(t *testing.T, path string, ro renderOpts, fps int) {
	t.Helper()
	n := videoPackets(t, path)
	if n != ro.End-ro.First {
		t.Fatalf("frames %d want %d", n, ro.End-ro.First)
	}
	m, err := probe(path)
	if err != nil {
		t.Fatal(err)
	}
	want := intervalDuration(ro.First, ro.End, fps)
	if m.Duration < want-1/float64(fps) || m.Duration > want+1/float64(fps) {
		t.Fatalf("duration %v want %v", m.Duration, want)
	}
	pts := firstVideoPTS(t, path)
	if pts > 0.02 {
		t.Fatalf("first pts %v", pts)
	}
}

func extractInterval(ctx context.Context, src, dst string, first, end int) error {
	args := []string{"-v", "error", "-y", "-i", src}
	args = append(args, intervalSelectArgs(first, end)...)
	args = append(args, "-an", "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p", dst)
	return run(ctx, "ffmpeg", args...)
}

func videoPackets(t *testing.T, path string) int {
	t.Helper()
	n, err := countPackets(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func videoSize(t *testing.T, path string) (int, int) {
	t.Helper()
	b, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimSpace(string(b)), ",")
	if len(parts) != 2 {
		t.Fatal(string(b))
	}
	w, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	h, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	return w, h
}

func firstVideoPTS(t *testing.T, path string) float64 {
	t.Helper()
	b, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=pts_time", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Split(strings.TrimSpace(string(b)), "\n")[0]
	v, err := strconv.ParseFloat(strings.TrimSpace(line), 64)
	if err != nil {
		t.Fatal(err, string(b))
	}
	return v
}

func pcmMono(t *testing.T, path string) []float64 {
	t.Helper()
	raw := rawPCM(t, path)
	out := make([]float64, 0, len(raw)/4)
	for i := 0; i+3 < len(raw); i += 4 {
		l := int16(binary.LittleEndian.Uint16(raw[i : i+2]))
		r := int16(binary.LittleEndian.Uint16(raw[i+2 : i+4]))
		out = append(out, float64(int32(l)+int32(r))/2/32768)
	}
	return out
}

func peakXCorr(ref, sig []float64, maxLag int) (int, float64) {
	if len(ref) == 0 || len(sig) == 0 {
		return 0, 0
	}
	bestLag, best := 0, -2.0
	for lag := -maxLag; lag <= maxLag; lag++ {
		var dot, a2, b2 float64
		n := 0
		for i := 0; i < len(ref); i++ {
			j := i + lag
			if j < 0 || j >= len(sig) {
				continue
			}
			dot += ref[i] * sig[j]
			a2 += ref[i] * ref[i]
			b2 += sig[j] * sig[j]
			n++
		}
		if n == 0 || a2 == 0 || b2 == 0 {
			continue
		}
		corr := dot / math.Sqrt(a2*b2)
		if corr > best {
			best, bestLag = corr, lag
		}
	}
	return bestLag, best
}
