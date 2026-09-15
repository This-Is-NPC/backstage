package presentation

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func resetSeams() {
	prepareSerial = false
	screenshotOptimize = true
	ffv1GOP = 1
	screenshotObserver = nil
	observeRender = nil
	prepareOne = prepareTrack
}

func TestResolveRenderThreadsDefaults(t *testing.T) {
	n := runtime.NumCPU()
	if n < 1 {
		n = 1
	}
	prep, filter, enc := resolveRenderThreads(scene.RenderThreads{}, 3)
	w := 3
	if w > n {
		w = n
	}
	wantPrep := n / w
	if wantPrep < 1 {
		wantPrep = 1
	}
	if prep != wantPrep || filter != 1 || enc != n {
		t.Fatalf("defaults prepare=%d filter=%d encode=%d want %d 1 %d", prep, filter, enc, wantPrep, n)
	}
	prep, filter, enc = resolveRenderThreads(scene.RenderThreads{Prepare: 7, Filter: 3, Encode: 5}, 8)
	if prep != 7 || filter != 3 || enc != 5 {
		t.Fatalf("configured %+v", []int{prep, filter, enc})
	}
}

func TestPrepareTrackSetsGOPOne(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required")
	}
	resetSeams()
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	if err := run(context.Background(), "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=blue:s=160x90:d=1:r=10", "-pix_fmt", "yuv420p", clip); err != nil {
		t.Fatal(err)
	}
	tr := CompiledTrack{Track: Track{Segments: []Segment{{From: 0, To: 0.4, Rate: 1}}}, Media: Media{Path: clip, Duration: 1, HasVideo: true}}
	_, args, err := prepareTrack(context.Background(), tr, dir, "track-0", 10, 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-g 1") {
		t.Fatalf("prepare missing -g 1: %s", joined)
	}
	if err := everyFrameKeyframe(filepath.Join(dir, "track-0.mkv")); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedTrackOrderIsSortedKeys(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required")
	}
	resetSeams()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.3,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 10},
		Sources:  map[string]Source{"z": {File: "clip.mp4"}, "a": {File: "clip.mp4"}, "m": {File: "clip.mp4"}},
		Tracks:   map[string]Track{"z": {Source: "z"}, "a": {Source: "a"}, "m": {Source: "m"}},
		Timeline: []Event{{At: 0, Layout: "single", Slots: map[string]string{"center": "a"}}},
	})
	writeTinyMedia(t, p.Dir)
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	var saw renderNote
	observeRender = func(n renderNote) { saw = n }
	defer resetSeams()
	if _, _, err = prepareTracks(context.Background(), plan, t.TempDir(), io.Discard, nil); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "track-0.mkv", "m": "track-1.mkv", "z": "track-2.mkv"}
	for id, name := range want {
		if filepath.Base(saw.PreparedPaths[id]) != name {
			t.Fatalf("decoder order: %s -> %s want %s (%v)", id, saw.PreparedPaths[id], name, saw.PreparedPaths)
		}
	}
}

func TestPrepareErrorCancelsOthers(t *testing.T) {
	resetSeams()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.3,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 10},
		Sources:  map[string]Source{"bad": {File: "clip.mp4"}, "slow": {File: "clip.mp4"}},
		Tracks:   map[string]Track{"bad": {Source: "bad"}, "slow": {Source: "slow"}},
		Timeline: []Event{{At: 0, Layout: "single", Slots: map[string]string{"center": "bad"}}},
	})
	writeTinyMedia(t, p.Dir)
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 2)
	prepareOne = func(ctx context.Context, _ CompiledTrack, _, id string, _, _, _, _ int) (string, []string, error) {
		started <- id
		if strings.HasPrefix(id, "track-0") {
			return "", nil, fmt.Errorf("boom")
		}
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(8 * time.Second):
			return "", nil, fmt.Errorf("slow prepare was not cancelled")
		}
	}
	defer resetSeams()
	startedAt := time.Now()
	_, _, err = prepareTracks(context.Background(), plan, t.TempDir(), io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want first error, got %v", err)
	}
	if time.Since(startedAt) > time.Second {
		t.Fatalf("slow worker was not cancelled: %v", time.Since(startedAt))
	}
}

func TestPrepareCancelKillsFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required")
	}
	resetSeams()
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	if err := run(context.Background(), "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=1280x720:rate=30:duration=30", "-pix_fmt", "yuv420p", clip); err != nil {
		t.Fatal(err)
	}
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 8,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 10},
		Sources:  map[string]Source{"cam": {File: "clip.mp4"}},
		Tracks:   map[string]Track{"cam": {Source: "cam"}},
		Timeline: []Event{{At: 0, Layout: "single", Slots: map[string]string{"center": "cam"}}},
	})
	if err := os.WriteFile(filepath.Join(p.Dir, "clip.mp4"), mustRead(t, clip), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(t.TempDir(), "work")
	if err = os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, _, err := prepareTracks(ctx, plan, work, io.Discard, nil); done <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		running, _ := exec.Command("pgrep", "-af", "ffmpeg").Output()
		if bytes.Contains(running, []byte(work)) {
			break
		}
		select {
		case err = <-done:
			t.Fatalf("prepare finished before ffmpeg was observed: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("ffmpeg never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("prepare ignored cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prepare hung after cancel")
	}
	out, _ := exec.Command("pgrep", "-af", "ffmpeg").Output()
	if bytes.Contains(out, []byte(work)) {
		t.Fatalf("ffmpeg still running after cancel:\n%s", out)
	}
}

func TestFFV1GOPOneMatchesLegacyPixels(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required")
	}
	resetSeams()
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	if err := run(context.Background(), "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=10:duration=0.4", "-pix_fmt", "yuv420p", clip); err != nil {
		t.Fatal(err)
	}
	tr := CompiledTrack{Track: Track{Segments: []Segment{{From: 0, To: 0.4, Rate: 1}}}, Media: Media{Path: clip, Duration: 0.4, HasVideo: true}}
	oldPath, _, err := prepareTrack(context.Background(), tr, dir, "old", 10, 1, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	newPath, _, err := prepareTrack(context.Background(), tr, dir, "new", 10, 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = sameDecodedFrames(context.Background(), oldPath, newPath, 4); err != nil {
		t.Fatal(err)
	}
}

func TestScreenshotUsesOptimizeForSpeed(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	var saw *bool
	v := false
	saw = &v
	screenshotObserver = func(optimize bool) { *saw = optimize }
	p := writeFixture(t, Document{Version: 1, Duration: 0.2, Render: scene.RenderCfg{W: 160, H: 90, FPS: 10}, Timeline: []Event{{Scene: "explain"}}})
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
	if err = Check(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if !*saw {
		t.Fatal("CaptureScreenshot ran without optimizeForSpeed")
	}
}

func TestEncodeThreadsHonorsConfig(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	var saw renderNote
	observeRender = func(n renderNote) {
		if n.EncoderArgs != nil {
			saw = n
		}
	}
	plan := timedPlan(t)
	plan.Project.Render.Threads.Encode = 3
	out := filepath.Join(t.TempDir(), "show.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := Render(ctx, plan, out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if saw.EncodeThreads != 3 || !argsHaveThreads(saw.EncoderArgs, 3) {
		t.Fatalf("encoder ignored config: threads=%d args=%v", saw.EncodeThreads, saw.EncoderArgs)
	}
}

func TestComposedPixelsMatchLegacySeams(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	plan := multiTrackPlan(t)
	prepareSerial, ffv1GOP = true, 0
	oldPrep, err := decodedTrackFrames(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	prepareSerial, ffv1GOP = false, 1
	newPrep, err := decodedTrackFrames(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = sameFrameMaps(oldPrep, newPrep); err != nil {
		t.Fatal(err)
	}
	shots, err := composedShotPairs(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(shots) != plan.Frames {
		t.Fatalf("frames %d want %d", len(shots), plan.Frames)
	}
	mid := false
	for i, pair := range shots {
		if pair.at > 0.2 && pair.at < 0.35 {
			mid = true
		}
		if err = samePixels(pair.plain, pair.fast); err != nil {
			t.Fatalf("frame %d t=%v: %v", i, pair.at, err)
		}
	}
	if !mid {
		t.Fatal("plan had no mid-transition frame")
	}
}

func TestH264EncodeThreadsMeetPSNR(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	plan := timedPlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dir := t.TempDir()
	fast := filepath.Join(dir, "fast.mp4")
	slow := filepath.Join(dir, "slow.mp4")
	if err := Render(ctx, plan, fast, io.Discard); err != nil {
		t.Fatal(err)
	}
	plan.Project.Render.Threads.Encode = 2
	if err := Render(ctx, plan, slow, io.Discard); err != nil {
		t.Fatal(err)
	}
	minY, avgY, err := videoPSNR(slow, fast)
	if err != nil {
		t.Fatal(err)
	}
	if minY < 40 || avgY < 45 {
		t.Fatalf("PSNR-Y min=%.2f avg=%.2f", minY, avgY)
	}
	if err = exportMatchesPlan(fast, plan); err != nil {
		t.Fatal(err)
	}
}

func multiTrackPlan(t *testing.T) *Plan {
	t.Helper()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 0.4,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 10},
		Sources:  map[string]Source{"cam": {File: "clip.mp4"}, "aux": {File: "clip.mp4"}},
		Tracks:   map[string]Track{"cam": {Source: "cam"}, "aux": {Source: "aux"}},
		Timeline: []Event{
			{At: 0, Layout: "two-screens", Slots: map[string]string{"left": "cam", "right": "aux"}},
			{At: 0.2, Layout: "single", Slots: map[string]string{"center": "cam"}, Transition: Transition{Effect: "fade", Duration: 0.15}},
		},
	})
	writeTinyMedia(t, p.Dir)
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

type shotPair struct {
	at          float64
	plain, fast []byte
}

func decodedTrackFrames(ctx context.Context, p *Plan) (map[string][][]byte, error) {
	work, err := os.MkdirTemp("", "backstage-tracks-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	paths, _, err := prepareTracks(ctx, p, work, io.Discard, nil)
	if err != nil {
		return nil, err
	}
	out := map[string][][]byte{}
	for _, id := range sortedKeys(p.Tracks) {
		d, e := newDecoder(ctx, paths[id], 0, p.FPS)
		if e != nil {
			return nil, e
		}
		var frames [][]byte
		for n := 0; n < p.Frames; n++ {
			b, e := d.get(n)
			if e != nil {
				d.close()
				return nil, e
			}
			frames = append(frames, append([]byte(nil), b...))
		}
		d.close()
		out[id] = frames
	}
	return out, nil
}

func sameFrameMaps(a, b map[string][][]byte) error {
	if len(a) != len(b) {
		return fmt.Errorf("tracks %d vs %d", len(a), len(b))
	}
	for id, frames := range a {
		other, ok := b[id]
		if !ok || len(other) != len(frames) {
			return fmt.Errorf("track %s frames", id)
		}
		for i := range frames {
			if err := samePixels(frames[i], other[i]); err != nil {
				return fmt.Errorf("track %s frame %d: %w", id, i, err)
			}
		}
	}
	return nil
}

func composedShotPairs(ctx context.Context, p *Plan) ([]shotPair, error) {
	work, err := os.MkdirTemp("", "backstage-compose-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	paths, _, err := prepareTracks(ctx, p, work, io.Discard, nil)
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
		screenshotOptimize = false
		plain, e := r.frame(at, frames)
		if e != nil {
			return nil, e
		}
		screenshotOptimize = true
		fast, e := r.frame(at, frames)
		if e != nil {
			return nil, e
		}
		shots = append(shots, shotPair{at: at, plain: plain, fast: fast})
	}
	return shots, nil
}

func samePixels(a, b []byte) error {
	if bytes.Equal(a, b) {
		return nil
	}
	ia, err := png.Decode(bytes.NewReader(a))
	if err != nil {
		return err
	}
	ib, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return err
	}
	if ia.Bounds() != ib.Bounds() {
		return fmt.Errorf("bounds %v vs %v", ia.Bounds(), ib.Bounds())
	}
	for y := ia.Bounds().Min.Y; y < ia.Bounds().Max.Y; y++ {
		for x := ia.Bounds().Min.X; x < ia.Bounds().Max.X; x++ {
			if ia.At(x, y) != ib.At(x, y) {
				return fmt.Errorf("pixel %d,%d", x, y)
			}
		}
	}
	return nil
}

func sameDecodedFrames(ctx context.Context, a, b string, frames int) error {
	da, err := newDecoder(ctx, a, 0, 10)
	if err != nil {
		return err
	}
	defer da.close()
	db, err := newDecoder(ctx, b, 0, 10)
	if err != nil {
		return err
	}
	defer db.close()
	for i := 0; i < frames; i++ {
		fa, err := da.get(i)
		if err != nil {
			return err
		}
		fb, err := db.get(i)
		if err != nil {
			return err
		}
		if err = samePixels(fa, fb); err != nil {
			return fmt.Errorf("decoded frame %d: %w", i, err)
		}
	}
	return nil
}

func everyFrameKeyframe(path string) error {
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v", "-show_entries", "frame=key_frame", "-of", "csv=p=0", path).Output()
	if err != nil {
		return err
	}
	lines := strings.Fields(string(out))
	if len(lines) == 0 {
		return fmt.Errorf("no frames")
	}
	for i, line := range lines {
		if strings.TrimSpace(line) != "1" {
			return fmt.Errorf("frame %d is not a seek point: %q", i, line)
		}
	}
	return nil
}

func argsHaveThreads(args []string, n int) bool {
	want := strconv.Itoa(n)
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-threads" && args[i+1] == want {
			return true
		}
	}
	return false
}

func videoPSNR(ref, dist string) (minY, avgY float64, err error) {
	cmd := exec.Command("ffmpeg", "-v", "info", "-i", dist, "-i", ref, "-lavfi", "[0:v][1:v]psnr", "-f", "null", "-")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, 0, fmt.Errorf("psnr: %w: %s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "PSNR") || !strings.Contains(line, "y:") {
			continue
		}
		minY, avgY, err = parsePSNRLine(line)
		if err == nil {
			return minY, avgY, nil
		}
	}
	return 0, 0, fmt.Errorf("psnr line not found: %s", out)
}

func parsePSNRLine(line string) (minY, avgY float64, err error) {
	fields := strings.Fields(line)
	for _, f := range fields {
		if after, ok := strings.CutPrefix(f, "y:"); ok {
			avgY, err = strconv.ParseFloat(strings.TrimSuffix(after, ","), 64)
			if err != nil {
				return 0, 0, err
			}
		}
		if after, ok := strings.CutPrefix(f, "min:"); ok {
			minY, err = strconv.ParseFloat(after, 64)
			if err != nil {
				return 0, 0, err
			}
		}
	}
	if avgY == 0 && minY == 0 {
		return 0, 0, fmt.Errorf("parse %q", line)
	}
	return minY, avgY, nil
}

func exportMatchesPlan(path string, p *Plan) error {
	m, err := probe(path)
	if err != nil {
		return err
	}
	if !m.HasVideo || m.Duration < p.Document.Duration-0.1 || m.Duration > p.Document.Duration+0.1 {
		return fmt.Errorf("duration %+v want %v", m, p.Document.Duration)
	}
	if len(p.AudioParts) > 0 && !m.HasAudio {
		return fmt.Errorf("missing audio")
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0", "-show_entries", "stream=nb_read_frames", "-of", "csv=p=0", path).Output()
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return err
	}
	if n != p.Frames {
		return fmt.Errorf("frames %d want %d", n, p.Frames)
	}
	return nil
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
