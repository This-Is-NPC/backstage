package presentation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestThreeEventManifestListsMarks(t *testing.T) {
	requireRenderTest(t)
	root := useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := threeEventPlan(t, "fade")
	if plan.Template == "" {
		t.Fatal("template not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "cold.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "segments")
	metas, err := filepath.Glob(filepath.Join(dir, "*.meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) == 0 {
		t.Fatal("no segment meta")
	}
	found := map[string]string{}
	for _, path := range metas {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m entryMeta
		if err = json.Unmarshal(b, &m); err != nil || m.Files == nil {
			t.Fatalf("meta %s: %s", path, b)
		}
		for rel, sum := range *m.Files {
			found[rel] = sum
		}
	}
	for _, name := range []string{"mark-one.png", "mark-two.png", "mark-three.png"} {
		if found[name] == "" {
			t.Errorf("missing %s in manifests: %v", name, found)
		}
	}
	if found["mark-one.png"] == found["mark-two.png"] || found["mark-two.png"] == found["mark-three.png"] {
		t.Fatalf("mark hashes must differ: %v", found)
	}
}

func TestSegmentEntryValidRequiresManifest(t *testing.T) {
	c := openTestCache(t)
	s := newSegmentCache(c, &Plan{Project: &scene.Project{Dir: t.TempDir()}}, "Chromium")
	dir := filepath.Join(c.root, "segments")
	key := strings.Repeat("ab", 32)
	dest := filepath.Join(dir, key+".mp4")
	if err := os.WriteFile(dest, []byte("mp4-bytes"), 0o444); err != nil {
		t.Fatal(err)
	}
	if s.entryValid(dest) {
		t.Fatal("missing meta must be invalid")
	}
	if err := os.WriteFile(filepath.Join(dir, key+".meta.json"), []byte(`{"last-used":1,"size":9}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if s.entryValid(dest) {
		t.Fatal("meta without files must be invalid")
	}
	empty := map[string]string{}
	if err := writeMeta(dir, key, 9, &empty); err != nil {
		t.Fatal(err)
	}
	if !s.entryValid(dest) {
		t.Fatal("empty files map must be valid")
	}
}

func TestSegmentMissingAssetHashInvalidates(t *testing.T) {
	c := openTestCache(t)
	proj := t.TempDir()
	s := newSegmentCache(c, &Plan{Project: &scene.Project{Dir: proj}}, "Chromium")
	dir := filepath.Join(c.root, "segments")
	key := strings.Repeat("ef", 32)
	dest := filepath.Join(dir, key+".mp4")
	if err := os.WriteFile(dest, []byte("mp4-bytes"), 0o444); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"later.css": ""}
	if err := writeMeta(dir, key, 9, &files); err != nil {
		t.Fatal(err)
	}
	if !s.entryValid(dest) {
		t.Fatal("absent stylesheet must stay valid")
	}
	if err := os.WriteFile(filepath.Join(proj, "later.css"), []byte("body{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s.entryValid(dest) {
		t.Fatal("created stylesheet must invalidate")
	}
}

func TestPruneSegmentsLRUKeepsLock(t *testing.T) {
	c := openTestCache(t)
	dir := filepath.Join(c.root, "segments")
	key := strings.Repeat("cd", 32)
	if err := os.WriteFile(filepath.Join(dir, key+".mp4"), []byte("segment"), 0o444); err != nil {
		t.Fatal(err)
	}
	empty := map[string]string{}
	if err := writeMeta(dir, key, 7, &empty); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(dir, key+".lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	rep, err := PruneRenderCache(context.Background(), c.root, CachePruneOptions{MaxSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Removed) != 1 || rep.Removed[0] != filepath.Join("segments", key+".mp4") {
		t.Fatalf("removed %+v", rep)
	}
	if _, err = os.Stat(lock); err != nil {
		t.Fatal("prune deleted the segments lock")
	}
}

func TestPreviewInteriorChunksHit(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := sixSecondVisualPlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "full.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := writePreview(ctx, plan, filepath.Join(t.TempDir(), "part.mp4"), &log, PreviewOpts{From: 0, To: 5, Scale: 1}); err != nil {
		t.Fatal(err)
	}
	hits, misses := countChunkCache(log.String())
	if hits != 2 || misses != 1 {
		t.Fatalf("preview hits=%d misses=%d log=\n%s", hits, misses, log.String())
	}
}

func TestSegmentColdWarmIdentical(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	for _, effect := range []string{"fade", "morph"} {
		t.Run(effect, func(t *testing.T) {
			useTempCache(t)
			plan := threeEventPlan(t, effect)
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
			defer cancel()
			cold := filepath.Join(t.TempDir(), "cold.mp4")
			warm := filepath.Join(t.TempDir(), "warm.mp4")
			var coldLog, warmLog bytes.Buffer
			if err := Render(ctx, plan, cold, &coldLog); err != nil {
				t.Fatal(err)
			}
			if err := Render(ctx, plan, warm, &warmLog); err != nil {
				t.Fatal(err)
			}
			hits, misses := countChunkCache(warmLog.String())
			if misses != 0 || hits != 4 {
				t.Fatalf("warm hits=%d misses=%d\n%s", hits, misses, warmLog.String())
			}
			assertChunkKeyframes(t, warm, chunkStarts(plan), plan.FPS)
			if n := videoPackets(t, warm); n != plan.Frames {
				t.Fatalf("frames %d want %d", n, plan.Frames)
			}
			coldPNG := decodeVideoPNGs(t, cold, plan.FPS, plan.Frames)
			warmPNG := decodeVideoPNGs(t, warm, plan.FPS, plan.Frames)
			if len(coldPNG) != len(warmPNG) {
				t.Fatalf("frames %d %d", len(coldPNG), len(warmPNG))
			}
			for i := range coldPNG {
				if err := samePixels(coldPNG[i], warmPNG[i]); err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
			}
			lag, corr := peakXCorr(pcmMono(t, cold), pcmMono(t, warm), mixSampleRate/200)
			if absInt(lag) > mixSampleRate/200 || corr < 0.95 {
				t.Fatalf("xcorr lag=%d corr=%.3f", lag, corr)
			}
			fa, fb := readFactsMap(t, cold), readFactsMap(t, warm)
			ia, _ := json.Marshal(fa["inputs"])
			ib, _ := json.Marshal(fb["inputs"])
			if !bytes.Equal(ia, ib) {
				t.Fatalf("inputs\n%s\n%s", ia, ib)
			}
		})
	}
}

func TestSegmentInvalidationTable(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	for _, effect := range []string{"fade", "morph"} {
		t.Run(effect, func(t *testing.T) {
			cases := []struct {
				name string
				mut  func(*testing.T, *Plan)
				want [4]bool // true = hit
			}{
				{"event1-entry", func(t *testing.T, p *Plan) { mutateEvent1Entry(t, p, effect) }, [4]bool{true, false, false, false}},
				{"event1-layout", func(t *testing.T, p *Plan) { p.Document.Timeline[1].Layout = event1OtherLayout(effect) }, [4]bool{true, false, false, false}},
				{"event1-slot", func(t *testing.T, p *Plan) { p.Document.Timeline[1].Slots = map[string]string{"center": "cam"} }, [4]bool{true, false, false, false}},
				{"event1-narration", func(t *testing.T, p *Plan) { mutateEvent1Narration(t, p, effect) }, [4]bool{true, true, true, true}},
				{"global-parameter", func(t *testing.T, p *Plan) { p.Document.Parameters["title"] = "Other" }, [4]bool{false, false, false, false}},
				{"event1-track", func(t *testing.T, p *Plan) { swapEvent1Track(t, p) }, [4]bool{true, false, false, false}},
				{"event2-asset", func(t *testing.T, p *Plan) { mutateEvent2Asset(t, p, effect) }, [4]bool{true, true, true, false}},
				{"event0-layout", func(t *testing.T, p *Plan) { p.Document.Timeline[0].Layout = event1OtherLayout(effect) }, [4]bool{false, false, true, true}},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					if tc.name == "event1-narration" {
						t.Skip("layout events have no scene narration in the chunk key")
					}
					useTempCache(t)
					plan := threeEventPlan(t, effect)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
					defer cancel()
					if err := Render(ctx, plan, filepath.Join(t.TempDir(), "cold.mp4"), io.Discard); err != nil {
						t.Fatal(err)
					}
					tc.mut(t, plan)
					var log bytes.Buffer
					if err := Render(ctx, plan, filepath.Join(t.TempDir(), "next.mp4"), &log); err != nil {
						t.Fatal(err)
					}
					got := chunkHits(log.String(), 4)
					if got != tc.want {
						t.Fatalf("hits %v want %v\n%s", got, tc.want, log.String())
					}
				})
			}
		})
	}
}

func TestVisualEventEntryAndNarration(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	t.Run("entry", func(t *testing.T) {
		useTempCache(t)
		plan := threeVisualPlan(t)
		if err := Render(ctx, plan, filepath.Join(t.TempDir(), "cold.mp4"), io.Discard); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(plan.Project.Dir, "e1.html"), []byte(eventVisualHTML+"<!--changed-->"), 0o600); err != nil {
			t.Fatal(err)
		}
		var log bytes.Buffer
		if err := Render(ctx, plan, filepath.Join(t.TempDir(), "next.mp4"), &log); err != nil {
			t.Fatal(err)
		}
		if chunkHits(log.String(), 4) != [4]bool{true, false, false, false} {
			t.Fatal(log.String())
		}
	})
	t.Run("narration", func(t *testing.T) {
		useTempCache(t)
		plan := threeVisualPlan(t)
		if err := Render(ctx, plan, filepath.Join(t.TempDir(), "cold.mp4"), io.Discard); err != nil {
			t.Fatal(err)
		}
		plan.Scenes["e1"].Narration.Cues[0].Text = "changed"
		var log bytes.Buffer
		if err := Render(ctx, plan, filepath.Join(t.TempDir(), "next.mp4"), &log); err != nil {
			t.Fatal(err)
		}
		if chunkHits(log.String(), 4) != [4]bool{true, false, false, false} {
			t.Fatal(log.String())
		}
	})
}

func threeVisualPlan(t *testing.T) *Plan {
	t.Helper()
	doc := Document{
		Version:  1,
		Duration: 8,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 10},
		Timeline: []Event{
			{At: 0, Scene: "e0"},
			{At: 2, Scene: "e1", Transition: Transition{Effect: "fade", Duration: 0.5}},
			{At: 6, Scene: "e2", Transition: Transition{Effect: "fade", Duration: 0.5}},
		},
	}
	p := writeFixture(t, doc)
	for _, name := range []string{"e0.html", "e1.html"} {
		if err := os.WriteFile(filepath.Join(p.Dir, name), []byte(eventVisualHTML), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(p.Dir, "e2.html"), []byte(event2VisualHTML), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTinyPNG(t, filepath.Join(p.Dir, "mark.png"), 9)
	for id, entry := range map[string]string{"e0": "e0.html", "e1": "e1.html", "e2": "e2.html"} {
		narr := ""
		if id == "e1" {
			narr = `,"narration":{"cues":[{"id":"bye","start":0,"end":1,"text":"bye"}]}`
		}
		if err := os.WriteFile(filepath.Join(p.Dir, "scenes", id+".json"), []byte(`{"type":"visual","entry":"`+entry+`","duration":10`+narr+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestSegmentManifestAssetChange(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := threeEventPlan(t, "fade")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "cold.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	mutateEvent2Asset(t, plan, "fade")
	var log bytes.Buffer
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "next.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if chunkHits(log.String(), 4) != [4]bool{true, true, true, false} {
		t.Fatal(log.String())
	}
}

func TestSegmentFullCacheSkipsEncode(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := threeEventPlan(t, "fade")
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "cold.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	before := descendantSet(os.Getpid())
	var cmds []cmdLine
	observeCommand = func(name string, args []string) {
		cmds = append(cmds, cmdLine{name, append([]string(nil), args...)})
	}
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "warm.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, c := range cmds {
		if isEncodeCommand(c.name, c.args) || isDecoderCommand(c.args) || isChromiumSession(c.name, c.args) {
			t.Fatalf("warm encode/session: %s %v", c.name, c.args)
		}
	}
	waitTreeIdle(t, before)
}

func TestSegmentInterruptLeavesNoMP4(t *testing.T) {
	requireRenderTest(t)
	root := useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := longVisualPlan(t)
	plan.Project.Render.Workers = 2
	failAtFrame = func(chunk, n int) error {
		if chunk == 0 && n == 0 {
			return context.Canceled
		}
		return nil
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "keep.mp4")
	if err := os.WriteFile(out, []byte("prior mp4"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_ = Render(ctx, plan, out, io.Discard)
	if got := listExt(t, filepath.Join(root, "segments"), ".mp4"); len(got) != 0 {
		t.Fatalf("partial mp4: %v", got)
	}
	b, err := os.ReadFile(out)
	if err != nil || string(b) != "prior mp4" {
		t.Fatal("replaced output", err)
	}
	if leftover := leftoverWork(dir); len(leftover) != 0 {
		t.Fatalf("work left: %v", leftover)
	}
}

func TestRendererReuseSamePixels(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := threeEventPlan(t, "fade")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	n := len(planChunks(plan, 0, plan.Frames))
	plan.Project.Render.Workers = 1
	one := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "one.mp4"), io.Discard)
	})
	resetSeams()
	useTempCache(t)
	plan.Project.Render.Workers = n
	many := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "many.mp4"), io.Discard)
	})
	if got, want := shotIndexes(many), shotIndexes(one); !indexesEqual(got, want) {
		t.Fatalf("n=%v want %v", got, want)
	}
	for i, shot := range many {
		if err := samePixels(shot.png, one[i].png); err != nil {
			t.Fatalf("n=%d: %v", i, err)
		}
	}
}

func TestThreeEventCodesAtBoundaries(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	plan := threeEventPlan(t, "fade")
	starts := chunkStarts(plan)
	maxW, err := resolveWorkers(0, len(starts))
	if err != nil {
		t.Fatal(err)
	}
	workers := []int{1, 2}
	if maxW > 2 {
		workers = append(workers, maxW)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	for _, w := range workers {
		t.Run("workers-"+strconv.Itoa(w), func(t *testing.T) {
			useTempCache(t)
			plan.Project.Render.Workers = w
			out := filepath.Join(t.TempDir(), "coded.mp4")
			if err := Render(ctx, plan, out, io.Discard); err != nil {
				t.Fatal(err)
			}
			pngs := decodeVideoPNGs(t, out, plan.FPS, plan.Frames)
			for _, n := range append(starts, plan.Frames-1) {
				if inTransition(plan, n) {
					continue
				}
				got, err := readFrameCode(pngs[n])
				if err != nil {
					t.Fatal(err)
				}
				if got != n {
					t.Fatalf("frame %d code %d", n, got)
				}
			}
			assertChunkKeyframes(t, out, starts, plan.FPS)
		})
	}
}

type cmdLine struct {
	name string
	args []string
}

func countChunkCache(log string) (hits, misses int) {
	for i := 0; i < 32; i++ {
		id := strconv.Itoa(i)
		if strings.Contains(log, ">> chunk-"+id+" cached\n") {
			hits++
		}
		if strings.Contains(log, ">> chunk-"+id+" running\n") {
			misses++
		}
	}
	return hits, misses
}

func chunkHits(log string, n int) [4]bool {
	var out [4]bool
	for i := 0; i < n && i < 4; i++ {
		out[i] = strings.Contains(log, ">> chunk-"+strconv.Itoa(i)+" cached\n")
	}
	return out
}

func isEncodeCommand(name string, args []string) bool {
	return filepath.Base(name) == "ffmpeg" && containsArg(args, "libx264")
}

func isDecoderCommand(args []string) bool {
	return containsArg(args, "png") && containsArg(args, "image2pipe")
}

func isChromiumSession(name string, args []string) bool {
	base := filepath.Base(name)
	switch base {
	case "chromium", "chromium-browser", "google-chrome", "chrome":
	default:
		return false
	}
	if len(args) == 1 && args[0] == "--version" {
		return false
	}
	return true
}

func inTransition(p *Plan, n int) bool {
	t := float64(n) / float64(p.FPS)
	for i, e := range p.Document.Timeline {
		if i == 0 || e.Transition.Duration <= 0 {
			continue
		}
		if t >= e.At && t < e.At+e.Transition.Duration {
			return true
		}
	}
	return false
}

func writeTinyPNG(t *testing.T, path string, seed byte) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = seed
		img.Pix[i+1] = 255 - seed
		img.Pix[i+2] = seed ^ 0x5a
		img.Pix[i+3] = 255
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

const eventVisualHTML = `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#808080}[data-slot]{position:absolute;inset:0;overflow:hidden}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center'],alt:['center'],'two-screens':['left','right']},captionSlots:['subtitle']};
window.render=async function(c){
  if(c.layout==='two-screens'){document.body.innerHTML='<div data-slot="left" data-fit="fill"></div><div data-slot="right" data-fit="fill"></div><div data-caption-slot="subtitle"></div>';}
  else{document.body.innerHTML='<div data-slot="center" data-fit="fill"></div><div data-caption-slot="subtitle"></div>';}
};
</script>`

const event2VisualHTML = `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#808080}[data-slot]{position:absolute;inset:0;overflow:hidden}</style><img src="mark.png" width="2" height="2"><script>
window.backstageTemplate={version:1,layouts:{single:['center'],'two-screens':['left','right']},captionSlots:['subtitle']};
window.render=async function(c){
  document.body.innerHTML='<div data-slot="center" data-fit="fill"></div><div data-caption-slot="subtitle"></div>';
};
</script>`

const morphTemplateHTML = `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#808080}[data-slot]{position:absolute;inset:0;overflow:hidden}</style><script>
window.backstageTemplate={version:1,layouts:{one:['center'],two:['center'],three:['center']},captionSlots:['subtitle']};
window.render=async function(c){
  document.body.innerHTML='<div data-slot="center" data-fit="fill"></div><div data-caption-slot="subtitle"></div>';
  const img=new Image(); img.src='mark-'+c.layout+'.png'; document.body.append(img); await img.decode();
};
</script>`

func sixSecondVisualPlan(t *testing.T) *Plan {
	t.Helper()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 6,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 10},
		Timeline: []Event{{Scene: "explain"}},
	})
	writeVisual(t, p.Dir)
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func threeEventPlan(t *testing.T, effect string) *Plan {
	t.Helper()
	doc := Document{
		Version:    1,
		Duration:   8,
		Render:     scene.RenderCfg{W: 320, H: 180, FPS: 10},
		Parameters: map[string]any{"title": "Demo"},
		Sources:    map[string]Source{"cam": {File: "coded.mp4"}, "aux": {File: "coded.mp4"}},
		Tracks: map[string]Track{
			"cam":  {Source: "cam"},
			"aux":  {Source: "aux", Start: 0.1},
			"feat": {Source: "cam"},
		},
		Audio: []Audio{{ID: "noise", File: "noise.wav", From: 0, To: 8}},
		Captions: []Caption{
			{Scene: "lines", Cue: "bye", Clock: "presentation", At: 1.8, Duration: 0.6, Slot: "subtitle"},
			{Scene: "lines", Cue: "hi", Clock: "presentation", At: 2.2, Duration: 1, Slot: "subtitle"},
		},
		Timeline: []Event{
			{At: 0, Layout: "one", Slots: map[string]string{"center": "cam"}},
			{At: 2, Layout: "two", Slots: map[string]string{"center": "feat"}, Transition: Transition{Effect: effect, Duration: 0.5}},
			{At: 6, Layout: "three", Slots: map[string]string{"center": "cam"}, Transition: Transition{Effect: effect, Duration: 0.5}},
		},
	}
	p := writeFixture(t, doc)
	writeCodedMP4(t, filepath.Join(p.Dir, "coded.mp4"), 320, 180, 10, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "aevalsrc=sin(2*PI*(200+1800*t)*t):s=48000:d=8", "-ac", "2", "-c:a", "pcm_s16le", filepath.Join(p.Dir, "noise.wav")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, "template.html"), []byte(morphTemplateHTML), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTinyPNG(t, filepath.Join(p.Dir, "mark-one.png"), 1)
	writeTinyPNG(t, filepath.Join(p.Dir, "mark-two.png"), 2)
	writeTinyPNG(t, filepath.Join(p.Dir, "mark-three.png"), 3)
	if err := os.WriteFile(filepath.Join(p.Dir, "scenes", "lines.json"), []byte(`{"type":"visual","entry":"template.html","duration":10,"narration":{"cues":[{"id":"bye","start":0,"end":1,"text":"bye"},{"id":"hi","start":1,"end":2,"text":"hi"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Templates = map[string]scene.TemplateRef{"show": {Entry: "template.html"}}
	ref := p.Presentations["show"]
	ref.Template = "show"
	p.Presentations["show"] = ref
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func event1OtherLayout(string) string { return "three" }

func mutateEvent1Entry(t *testing.T, p *Plan, _ string) {
	t.Helper()
	writeTinyPNG(t, filepath.Join(p.Project.Dir, "mark-two.png"), 80)
}

func mutateEvent1Narration(t *testing.T, p *Plan, _ string) {
	t.Helper()
	if p.Scenes["lines"] == nil || len(p.Scenes["lines"].Narration.Cues) == 0 {
		return
	}
	p.Scenes["lines"].Narration.Cues[0].Text = "changed"
}

func mutateEvent2Asset(t *testing.T, p *Plan, _ string) {
	t.Helper()
	writeTinyPNG(t, filepath.Join(p.Project.Dir, "mark-three.png"), 99)
}

func swapEvent1Track(t *testing.T, p *Plan) {
	t.Helper()
	other := filepath.Join(p.Project.Dir, "other.mp4")
	writeCodedMP4(t, other, 320, 180, 10, 7)
	tr := p.Tracks["feat"]
	tr.Media.Path = other
	p.Tracks["feat"] = tr
}

const missingStylesheetHTML = `<!doctype html><meta charset="utf-8"><link rel="stylesheet" href="later.css"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#123456}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){document.body.innerHTML='';};
</script>`

const markedVisualHTML = `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#222}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center']},captionSlots:[]};
window.render=async function(c){
  const img=new Image(); img.src='mark.png'; document.body.append(img); await img.decode();
};
</script>`

func oneSecondVisualPlan(t *testing.T, html string) *Plan {
	t.Helper()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 1,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 10},
		Timeline: []Event{{Scene: "explain"}},
	})
	if err := os.WriteFile(filepath.Join(p.Dir, "visual.html"), []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestMissingStylesheetThenCreateMissesChunk(t *testing.T) {
	requireRenderTest(t)
	root := useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := oneSecondVisualPlan(t, missingStylesheetHTML)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "cold.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	found := false
	metas, err := filepath.Glob(filepath.Join(root, "segments", "*.meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range metas {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m entryMeta
		if json.Unmarshal(b, &m) != nil || m.Files == nil {
			continue
		}
		if sum, ok := (*m.Files)["later.css"]; ok && sum == "" {
			found = true
		}
	}
	if !found {
		t.Fatal("later.css must be in the manifest as an empty hash")
	}
	if err := os.WriteFile(filepath.Join(plan.Project.Dir, "later.css"), []byte("html,body{background:#ff0000}"), 0o600); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "next.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), ">> chunk-0 cached\n") {
		t.Fatalf("creating later.css must miss chunk 0\n%s", log.String())
	}
	if !strings.Contains(log.String(), ">> chunk-0 running\n") {
		t.Fatalf("expected miss\n%s", log.String())
	}
}

func TestSingleChunkOutputIsNotCacheLink(t *testing.T) {
	requireRenderTest(t)
	root := useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := oneSecondVisualPlan(t, missingStylesheetHTML)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out := filepath.Join(t.TempDir(), "out.mp4")
	if err := Render(ctx, plan, out, io.Discard); err != nil {
		t.Fatal(err)
	}
	entries, err := filepath.Glob(filepath.Join(root, "segments", "*.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("cache entries %v", entries)
	}
	outInfo, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	cacheInfo, err := os.Stat(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	outSt := outInfo.Sys().(*syscall.Stat_t)
	cacheSt := cacheInfo.Sys().(*syscall.Stat_t)
	if outInfo.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", outInfo.Mode().Perm())
	}
	if outSt.Nlink != 1 {
		t.Fatalf("nlink %d", outSt.Nlink)
	}
	if outSt.Ino == cacheSt.Ino {
		t.Fatal("output shares the cache inode")
	}
}

func decoderSeekArgs(cmds []cmdLine) []string {
	var out []string
	for _, c := range cmds {
		if !isDecoderCommand(c.args) {
			continue
		}
		for i, a := range c.args {
			if a == "-ss" && i+1 < len(c.args) {
				out = append(out, c.args[i+1])
			}
		}
	}
	return out
}

func TestDecoderReopensAcrossCacheGap(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := codedLongPlan(t)
	plan.Project.Render.Workers = 1
	layerMode = "frames"
	chunks := planChunks(plan, 0, plan.Frames)
	if len(chunks) != 3 {
		t.Fatalf("chunks=%d %+v", len(chunks), chunks)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if err := writePreview(ctx, plan, filepath.Join(t.TempDir(), "mid.mp4"), io.Discard, PreviewOpts{From: 2, To: 4, Scale: 1}); err != nil {
		t.Fatal(err)
	}
	var cmds []cmdLine
	observeCommand = func(name string, args []string) {
		cmds = append(cmds, cmdLine{name, append([]string(nil), args...)})
	}
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "full.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	seeks := decoderSeekArgs(cmds)
	want := decoderSeekTime(chunks[2].First, plan.FPS)
	if len(seeks) != 1 || seeks[0] != want {
		t.Fatalf("gap seeks %v want [%s]", seeks, want)
	}

	useTempCache(t)
	cmds = nil
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "cold.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := decoderSeekArgs(cmds); len(got) != 0 {
		t.Fatalf("contiguous seeks %v", got)
	}
}

func TestManifestHashErrorClaimsChunk(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := oneSecondVisualPlan(t, markedVisualHTML)
	writeTinyPNG(t, filepath.Join(plan.Project.Dir, "mark.png"), 7)
	observeFrame = func(n int, _ float64, _ []byte) {
		if n == plan.Frames-1 {
			_ = os.Remove(filepath.Join(plan.Project.Dir, "mark.png"))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "chunk 0:") {
		t.Fatalf("err=%v", err)
	}
}

func TestPublishFailRendersOnce(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	publishCacheFile = func(string, string) error {
		return errors.New("publish boom")
	}
	plan := oneSecondVisualPlan(t, missingStylesheetHTML)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var log bytes.Buffer
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(log.String(), ">> chunk-0 running\n"); n != 1 {
		t.Fatalf("running count %d\n%s", n, log.String())
	}
	if n := strings.Count(log.String(), ">> chunk-0 ok\n"); n != 1 {
		t.Fatalf("ok count %d\n%s", n, log.String())
	}
}

func containsID(ids []string, id string) bool {
	for _, s := range ids {
		if s == id {
			return true
		}
	}
	return false
}

func TestHideTrackClosesDecoder(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := hideTrackPlan(t)
	plan.Project.Render.Workers = 1
	layerMode = "frames"
	if n := len(planChunks(plan, 0, plan.Frames)); n < 2 {
		t.Fatalf("chunks=%d", n)
	}
	got := map[int][]string{}
	observeChunkDecoders = func(index int, ids []string) {
		got[index] = append([]string(nil), ids...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if !containsID(got[1], "cam") {
		t.Fatalf("chunk 1 missing cam: %v", got[1])
	}
	if containsID(got[1], "feat") {
		t.Fatalf("chunk 1 still has feat: %v", got[1])
	}
	if !containsID(got[0], "feat") || !containsID(got[0], "cam") {
		t.Fatalf("chunk 0 decoders %v", got[0])
	}
}

func hideTrackPlan(t *testing.T) *Plan {
	t.Helper()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 4,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 10},
		Sources:  map[string]Source{"cam": {File: "coded.mp4"}},
		Tracks: map[string]Track{
			"cam":  {Source: "cam"},
			"feat": {Source: "cam", OnEnd: "hide", Segments: []Segment{{From: 0, To: 2, Rate: 1}}},
		},
		Timeline: []Event{{At: 0, Layout: "two-screens", Slots: map[string]string{"left": "cam", "right": "feat"}}},
	})
	writeCodedMP4(t, filepath.Join(p.Dir, "coded.mp4"), 160, 90, 10, 4)
	if err := os.WriteFile(filepath.Join(p.Dir, "full.html"), []byte(`<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#808080}[data-slot]{position:absolute;overflow:hidden}</style><script>
window.backstageTemplate={version:1,layouts:{single:['center'],'two-screens':['left','right']},captionSlots:[]};
window.render=async function(c){
  if(c.layout==='two-screens'){document.body.innerHTML='<div data-slot="left" data-fit="fill" style="left:0;top:0;width:50%;height:100%"></div><div data-slot="right" data-fit="fill" style="left:50%;top:0;width:50%;height:100%"></div>';}
  else{document.body.innerHTML='<div data-slot="center" data-fit="fill" style="inset:0"></div>';}
};
</script>`), 0o600); err != nil {
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
