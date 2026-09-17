package presentation

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestWarmAndColdComposedFramesMatch(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	plan := multiTrackPlan(t)
	cold, err := composedShots(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	warm, err := composedShots(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(cold) != len(warm) || len(cold) != plan.Frames {
		t.Fatalf("frames %d %d want %d", len(cold), len(warm), plan.Frames)
	}
	mid := false
	for i := range cold {
		if cold[i].at > 0.2 && cold[i].at < 0.35 {
			mid = true
		}
		if err = samePixels(cold[i].plain, warm[i].plain); err != nil {
			t.Fatalf("frame %d t=%v: %v", i, cold[i].at, err)
		}
	}
	if !mid {
		t.Fatal("plan had no mid-transition frame")
	}
}

func TestFactsInputsUnchangedByCache(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	plan := timedPlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	a := filepath.Join(t.TempDir(), "a.mp4")
	b := filepath.Join(t.TempDir(), "b.mp4")
	if err := Render(ctx, plan, a, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := Render(ctx, plan, b, io.Discard); err != nil {
		t.Fatal(err)
	}
	fa := readFactsMap(t, a)
	fb := readFactsMap(t, b)
	ia, _ := json.Marshal(fa["inputs"])
	ib, _ := json.Marshal(fb["inputs"])
	if !bytes.Equal(ia, ib) {
		t.Fatalf("inputs changed\n%s\n%s", ia, ib)
	}
	ta := asObject(t, fa["timings"], "timings")
	tb := asObject(t, fb["timings"], "timings")
	cold := asObject(t, ta["cache-misses"], "cold misses")
	warm := asObject(t, tb["cache-hits"], "warm hits")
	if asFloat(t, cold["tracks"], "tracks")+asFloat(t, cold["audio"], "audio") < 1 {
		t.Fatalf("cold should miss: %v", ta)
	}
	if asFloat(t, warm["tracks"], "hits")+asFloat(t, warm["audio"], "hits") < 1 {
		t.Fatalf("warm should hit: %v", tb)
	}
}

func TestWarmRenderDoesNotMutateCacheEntry(t *testing.T) {
	requireRenderTest(t)
	root := useTempCache(t)
	plan := timedPlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "cold.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for _, kind := range []string{"tracks", "audio", "segments"} {
		ext := ".mkv"
		if kind == "audio" {
			ext = ".wav"
		}
		if kind == "segments" {
			ext = ".mp4"
		}
		for _, name := range listExt(t, filepath.Join(root, kind), ext) {
			before[filepath.Join(kind, name)] = fileSHA(t, filepath.Join(root, kind, name))
		}
	}
	if len(before) == 0 {
		t.Fatal("no cache entries after cold render")
	}
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "warm.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	for rel, sum := range before {
		got := fileSHA(t, filepath.Join(root, rel))
		if got != sum {
			t.Fatalf("cache entry %s changed after warm render (including mux)", rel)
		}
	}
}

func TestCacheHitsOnSecondPrepareTracks(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required")
	}
	c := openTestCache(t)
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.3,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 10},
		Sources:  map[string]Source{"cam": {File: "clip.mp4"}},
		Tracks:   map[string]Track{"cam": {Source: "cam"}},
		Timeline: []Event{{At: 0, Layout: "single", Slots: map[string]string{"center": "cam"}}},
	})
	writeTinyMedia(t, p.Dir)
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = prepareTracks(context.Background(), plan, t.TempDir(), io.Discard, c); err != nil {
		t.Fatal(err)
	}
	if _, _, err = prepareTracks(context.Background(), plan, t.TempDir(), io.Discard, c); err != nil {
		t.Fatal(err)
	}
	if c.hits.Tracks != 1 {
		t.Fatalf("second prepare should hit: %+v", c.hits)
	}
}

func composedShots(ctx context.Context, p *Plan) ([]shotPair, error) {
	c, err := openRenderCache(ctx, io.Discard)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	work, err := os.MkdirTemp("", "backstage-compose-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	paths, _, err := prepareTracks(ctx, p, work, io.Discard, c)
	if err != nil {
		return nil, err
	}
	r, err := NewRenderer(ctx, p)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	decoders := map[string]*decoder{}
	defer func() {
		for _, d := range decoders {
			d.close()
		}
	}()
	for _, id := range sortedKeys(p.Tracks) {
		d, e := newDecoder(ctx, paths[id], 0, p.FPS)
		if e != nil {
			return nil, e
		}
		decoders[id] = d
	}
	var shots []shotPair
	for n := 0; n < p.Frames; n++ {
		at := float64(n) / float64(p.FPS)
		frames := map[string][]byte{}
		for _, id := range sortedKeys(p.Tracks) {
			tr := p.Tracks[id]
			local := at - tr.Start
			if local < 0 || (local >= tr.Duration && tr.OnEnd == "hide") {
				continue
			}
			index := int(math.Floor(local*float64(p.FPS) + 1e-8))
			b, e := decoders[id].get(index)
			if e != nil {
				return nil, e
			}
			frames[id] = b
		}
		shot, e := r.frame(at, frames)
		if e != nil {
			return nil, e
		}
		shots = append(shots, shotPair{at: at, plain: shot, fast: shot})
	}
	return shots, nil
}
