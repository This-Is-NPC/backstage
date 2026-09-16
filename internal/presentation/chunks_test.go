package presentation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestChunkComposedPixelsMatchSerial(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cases := []struct {
		name   string
		plan   *Plan
		chunks [][2]int
	}{
		{"fade", multiTrackPlan(t), [][2]int{{0, 3}, {3, 4}}},
		{"caption", captionSwapPlan(t), [][2]int{{0, 2}, {2, 4}}},
		{"freeze", freezeTailPlan(t), [][2]int{{0, 4}, {4, 6}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resetSeams()
			c.plan.Project.Render.Workers = 1
			serial := collectShots(t, func() error {
				return Render(ctx, c.plan, filepath.Join(t.TempDir(), "serial.mp4"), io.Discard)
			})
			resetSeams()
			c.plan.Project.Render.Workers = len(c.chunks)
			testChunks = c.chunks
			part := collectShots(t, func() error {
				return Render(ctx, c.plan, filepath.Join(t.TempDir(), "part.mp4"), io.Discard)
			})
			if got, want := shotIndexes(part), shotIndexes(serial); !indexesEqual(got, want) {
				t.Fatalf("n=%v want %v", got, want)
			}
			for n, shot := range part {
				if err := samePixels(shot.png, serial[n].png); err != nil {
					t.Fatalf("n=%d: %v", n, err)
				}
			}
		})
	}
}

func TestChunkCodedExport(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := codedFramePlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	serial := filepath.Join(t.TempDir(), "serial.mp4")
	plan.Project.Render.Workers = 1
	if err := Render(ctx, plan, serial, io.Discard); err != nil {
		t.Fatal(err)
	}
	minChunkFramesFn = func(int) int { return 1 }
	maxW, err := resolveWorkers(0, plan.Frames, plan.FPS)
	if err != nil {
		t.Fatal(err)
	}
	minChunkFramesFn = minChunkFrames
	workers := []int{2, 3}
	if maxW > 3 {
		workers = append(workers, maxW)
	}
	for _, w := range workers {
		t.Run(fmt.Sprintf("workers-%d", w), func(t *testing.T) {
			resetSeams()
			plan.Project.Render.Workers = w
			out := filepath.Join(t.TempDir(), "join.mp4")
			if err := Render(ctx, plan, out, io.Discard); err != nil {
				t.Fatal(err)
			}
			assertCodedExport(t, out, plan)
			if w != 2 {
				return
			}
			minY, avgY, err := videoPSNR(serial, out)
			if err != nil {
				t.Fatal(err)
			}
			if minY < 40 || avgY < 45 {
				t.Fatalf("PSNR-Y min=%.2f avg=%.2f", minY, avgY)
			}
			lag, corr := peakXCorr(pcmMono(t, serial), pcmMono(t, out), mixSampleRate/200)
			if absInt(lag) > mixSampleRate/200 || corr < 0.95 {
				t.Fatalf("xcorr lag=%d corr=%.3f", lag, corr)
			}
		})
	}
	t.Run("boundary", func(t *testing.T) {
		resetSeams()
		plan.Project.Render.Workers = 2
		testChunks = [][2]int{{0, 5}, {5, plan.Frames}}
		out := filepath.Join(t.TempDir(), "bound.mp4")
		if err := Render(ctx, plan, out, io.Discard); err != nil {
			t.Fatal(err)
		}
		assertCodedExport(t, out, plan)
		assertChunkKeyframes(t, out, []int{0, 5}, plan.FPS)
	})
}

func TestOneWorkerFFmpegArgs(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := timedPlan(t)
	plan.Project.Render.Workers = 1
	var cmds [][]string
	observeCommand = func(name string, args []string) {
		if name == "ffmpeg" {
			cmds = append(cmds, append([]string(nil), args...))
		}
	}
	var notes []renderNote
	observeRender = func(n renderNote) {
		notes = append(notes, n)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "one.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, 0, len(cmds))
	var encoder []string
	for _, args := range cmds {
		k := ffmpegKind(args)
		if k != "other" {
			kinds = append(kinds, k)
		}
		if k == "encoder" {
			encoder = args
		}
	}
	mixAt, decAt, encAt := indexOfKind(kinds, "mix"), indexOfKind(kinds, "decoder"), indexOfKind(kinds, "encoder")
	if mixAt < 0 || decAt < 0 || encAt < 0 {
		t.Fatalf("kinds %v", kinds)
	}
	if mixAt > decAt || mixAt > encAt {
		t.Fatalf("mix must run in shared prepare, before the chunk decoder/encoder: %v", kinds)
	}
	if encoder == nil {
		t.Fatal("missing encoder")
	}
	threads := strconv.Itoa(runtimeEncodeThreads(plan))
	want := []string{"-v", "error", "-y", "-f", "image2pipe", "-framerate", strconv.Itoa(plan.FPS), "-i", "-", "-an", "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p", "-threads", threads, "-video_track_timescale", strconv.Itoa(plan.FPS)}
	got := encoder[:len(encoder)-1]
	if !stringSliceEqual(got, want) {
		t.Fatalf("encoder args:\n got %v\nwant %v <out>", got, want)
	}
	if !strings.HasSuffix(encoder[len(encoder)-1], "video.mp4") {
		t.Fatalf("encoder out %s", encoder[len(encoder)-1])
	}
	for _, n := range notes {
		if len(n.ConcatArgs) > 0 {
			t.Fatalf("one worker must not concat: %v", n.ConcatArgs)
		}
	}
}

func TestChunkEncoderArgsMatch(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := timedPlan(t)
	plan.Project.Render.Workers = 2
	var encoders [][]string
	observeRender = func(n renderNote) {
		if n.EncoderArgs != nil {
			encoders = append(encoders, append([]string(nil), n.EncoderArgs...))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "join.mp4"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(encoders) != 2 {
		t.Fatalf("encoders %d", len(encoders))
	}
	ts := "-video_track_timescale"
	for i, args := range encoders {
		if !containsArg(args, ts) || args[indexOf(args, ts)+1] != strconv.Itoa(plan.FPS) {
			t.Fatalf("encoder %d missing timescale: %v", i, args)
		}
		if i == 0 {
			continue
		}
		if !stringSliceEqual(args[:len(args)-1], encoders[0][:len(encoders[0])-1]) {
			t.Fatalf("encoder args differ except path:\n 0=%v\n %d=%v", encoders[0], i, args)
		}
		if args[len(args)-1] == encoders[0][len(encoders[0])-1] {
			t.Fatal("chunk outputs must differ")
		}
	}
}

func TestMergeChunkResults(t *testing.T) {
	var a, b chunkResult
	a.index, b.index = 0, 1
	a.rendererStart, b.rendererStart = 0.1, 0.25
	a.decoderStart, b.decoderStart = 0.25, 0.25
	a.encode, b.encode = 1.2, 3.4
	a.wall, b.wall = 4, 5
	a.decodedPNG, b.decodedPNG = 10, 20
	a.screenshotBytes, b.screenshotBytes = 3, 4
	a.decode.add(5 * time.Millisecond)
	a.decode.add(5 * time.Millisecond)
	b.decode.add(10 * time.Millisecond)
	a.transfer.add(1 * time.Millisecond)
	b.transfer.add(2 * time.Millisecond)
	a.draw.add(3 * time.Millisecond)
	b.draw.add(4 * time.Millisecond)
	a.shot.add(6 * time.Millisecond)
	b.shot.add(7 * time.Millisecond)
	a.write.add(8 * time.Millisecond)
	b.write.add(9 * time.Millisecond)
	a.files = map[string]string{"x": "/x"}
	b.files = map[string]string{"y": "/y"}
	var timings renderTimings
	files := mergeChunkResults([]chunkResult{a, b}, &timings)
	if timings.EncodeSeconds != 3.4 {
		t.Fatalf("encode=%v want max 3.4", timings.EncodeSeconds)
	}
	if timings.RendererStartSeconds != 0.35 || timings.DecoderStartSeconds != 0.5 {
		t.Fatalf("starts renderer=%v decoder=%v", timings.RendererStartSeconds, timings.DecoderStartSeconds)
	}
	if len(timings.ChunkSeconds) != 2 || timings.ChunkSeconds[0].ID != "chunk-0" || timings.ChunkSeconds[1].ID != "chunk-1" {
		t.Fatalf("chunk-seconds %+v", timings.ChunkSeconds)
	}
	if timings.ChunkSeconds[0].Seconds != 4 || timings.ChunkSeconds[1].Seconds != 5 {
		t.Fatal(timings.ChunkSeconds)
	}
	if timings.Decode.Frames != 3 || timings.Decode.Seconds != 0.02 {
		t.Fatalf("decode %+v", timings.Decode)
	}
	if timings.Transfer.Frames != 2 || timings.Transfer.Seconds != 0.003 {
		t.Fatalf("transfer %+v", timings.Transfer)
	}
	if timings.Draw.Frames != 2 || timings.Draw.Seconds != 0.007 {
		t.Fatalf("draw %+v", timings.Draw)
	}
	if timings.Screenshot.Frames != 2 || timings.Screenshot.Seconds != 0.013 {
		t.Fatalf("screenshot %+v", timings.Screenshot)
	}
	if timings.EncodeWrite.Frames != 2 || timings.EncodeWrite.Seconds != 0.017 {
		t.Fatalf("encode-write %+v", timings.EncodeWrite)
	}
	if timings.DecodedPNGBytes != 30 || timings.ScreenshotBytes != 7 {
		t.Fatalf("bytes png=%d shot=%d", timings.DecodedPNGBytes, timings.ScreenshotBytes)
	}
	if files["x"] != "/x" || files["y"] != "/y" {
		t.Fatal(files)
	}
}

func TestChunkFailureCancelsOthers(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := longVisualPlan(t)
	plan.Project.Render.Workers = 2
	failAtFrame = func(chunk, n int) error {
		if chunk == 0 && n == 0 {
			return fmt.Errorf("boom")
		}
		return nil
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "keep.mp4")
	if err := os.WriteFile(out, []byte("prior mp4"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := descendantSet(os.Getpid())
	var log bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	err := Render(ctx, plan, out, &log)
	if err == nil || !strings.Contains(err.Error(), "chunk 0:") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err=%v", err)
	}
	text := log.String()
	if !strings.Contains(text, ">> chunk-0 failed") || !strings.Contains(text, ">> chunk-1 interrupted") {
		t.Fatal(text)
	}
	if strings.Contains(text, ">> chunk-1 failed") || strings.Contains(text, ">> chunk-0 interrupted") {
		t.Fatal(text)
	}
	if strings.Contains(text, "job.progress") || strings.Contains(text, `"jsonrpc"`) {
		t.Fatal(text)
	}
	b, err := os.ReadFile(out)
	if err != nil || string(b) != "prior mp4" {
		t.Fatal("replaced output", err)
	}
	if leftover := leftoverWork(dir); len(leftover) != 0 {
		t.Fatalf("work left: %v", leftover)
	}
	waitTreeIdle(t, before)
}

func TestChunkFirstFailureOwnsError(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := longVisualPlan(t)
	plan.Project.Render.Workers = 2
	failAtFrame = func(chunk, n int) error {
		if n == 0 {
			return fmt.Errorf("boom-%d", chunk)
		}
		return nil
	}
	var log bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log)
	if err == nil {
		t.Fatal("expected error")
	}
	text := log.String()
	failed := 0
	owner := -1
	for i := 0; i < 2; i++ {
		if strings.Contains(text, fmt.Sprintf(">> chunk-%d failed\n", i)) {
			failed++
			owner = i
		}
	}
	if failed != 1 || owner < 0 {
		t.Fatalf("failed count=%d owner=%d log=%s", failed, owner, text)
	}
	want := fmt.Sprintf("chunk %d: boom-%d", owner, owner)
	if err.Error() != want {
		t.Fatalf("err=%q want %q", err, want)
	}
	other := 1 - owner
	if !strings.Contains(text, fmt.Sprintf(">> chunk-%d interrupted\n", other)) {
		t.Fatal(text)
	}
}

func TestFirstFailClaimOnce(t *testing.T) {
	var f firstFail
	if !f.claim(1, fmt.Errorf("chrome: %w", context.Canceled)) {
		t.Fatal("first claim")
	}
	if f.claim(0, errors.New("other")) {
		t.Fatal("second claim")
	}
	if f.idx != 1 || !errors.Is(f.err, context.Canceled) || f.err.Error() != "chrome: context canceled" {
		t.Fatalf("idx=%d err=%v", f.idx, f.err)
	}
}

func TestChunkFailureWrapsCanceled(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := longVisualPlan(t)
	plan.Project.Render.Workers = 2
	failAtFrame = func(chunk, n int) error {
		if n == 0 {
			return fmt.Errorf("chrome: %w", context.Canceled)
		}
		return nil
	}
	var log bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	err := Render(ctx, plan, filepath.Join(t.TempDir(), "out.mp4"), &log)
	if err == nil {
		t.Fatal("expected error")
	}
	text := log.String()
	failed := 0
	owner := -1
	for i := 0; i < 2; i++ {
		if strings.Contains(text, fmt.Sprintf(">> chunk-%d failed\n", i)) {
			failed++
			owner = i
		}
	}
	if failed != 1 || owner < 0 {
		t.Fatalf("failed count=%d owner=%d log=%s", failed, owner, text)
	}
	want := fmt.Sprintf("chunk %d: chrome: context canceled", owner)
	if err.Error() != want {
		t.Fatalf("err=%q want %q", err, want)
	}
	if strings.Contains(text, fmt.Sprintf(">> chunk-%d interrupted\n", owner)) {
		t.Fatal(text)
	}
}

func TestChunkCancelInterruptsAll(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := longVisualPlan(t)
	plan.Project.Render.Workers = 2
	dir := t.TempDir()
	out := filepath.Join(dir, "keep.mp4")
	if err := os.WriteFile(out, []byte("prior mp4"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := descendantSet(os.Getpid())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var seen atomic.Int32
	observeFrame = func(int, float64, []byte) {
		if seen.Add(1) >= 3 {
			cancel()
		}
	}
	var log bytes.Buffer
	err := Render(ctx, plan, out, &log)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	text := log.String()
	if strings.Contains(text, " failed\n") || !strings.Contains(text, " interrupted\n") {
		t.Fatal(text)
	}
	b, err := os.ReadFile(out)
	if err != nil || string(b) != "prior mp4" {
		t.Fatal("replaced output", err)
	}
	if leftover := leftoverWork(dir); len(leftover) != 0 {
		t.Fatalf("work left: %v", leftover)
	}
	waitTreeIdle(t, before)
}

func TestPreviewIntervalUsesChunks(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := freezeTailPlan(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	plan.Project.Render.Workers = 1
	full := collectShots(t, func() error {
		return Render(ctx, plan, filepath.Join(t.TempDir(), "full.mp4"), io.Discard)
	})
	resetSeams()
	plan.Project.Render.Workers = 2
	var log bytes.Buffer
	part := collectShots(t, func() error {
		return writePreview(ctx, plan, filepath.Join(t.TempDir(), "part.mp4"), &log, PreviewOpts{From: 0, To: plan.Document.Duration, Scale: 1})
	})
	if !strings.Contains(log.String(), ">> chunk-0") || !strings.Contains(log.String(), ">> chunk-1") {
		t.Fatal(log.String())
	}
	if got, want := shotIndexes(part), shotIndexes(full); !indexesEqual(got, want) {
		t.Fatalf("n=%v want %v", got, want)
	}
	for n, shot := range part {
		if err := samePixels(shot.png, full[n].png); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
	}
}

func longVisualPlan(t *testing.T) *Plan {
	t.Helper()
	p := writeFixture(t, Document{
		Version:  1,
		Duration: 2,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: 12},
		Timeline: []Event{{Scene: "explain"}},
	})
	writeVisual(t, p.Dir)
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func assertCodedExport(t *testing.T, path string, plan *Plan) {
	t.Helper()
	n := videoPackets(t, path)
	if n != plan.Frames {
		t.Fatalf("frames %d want %d", n, plan.Frames)
	}
	m, err := probe(path)
	if err != nil {
		t.Fatal(err)
	}
	want := plan.Document.Duration
	if m.Duration < want-1/float64(plan.FPS) || m.Duration > want+1/float64(plan.FPS) {
		t.Fatalf("duration %v want %v", m.Duration, want)
	}
	pkts := readVideoPackets(t, path)
	if len(pkts) != plan.Frames {
		t.Fatalf("packets %d want %d", len(pkts), plan.Frames)
	}
	step := 1 / float64(plan.FPS)
	for i, p := range pkts {
		wantPTS := float64(i) * step
		if absFloat(p.pts-wantPTS) > 1e-4 {
			t.Fatalf("pts[%d]=%v want %v", i, p.pts, wantPTS)
		}
		if i > 0 && p.pts <= pkts[i-1].pts {
			t.Fatalf("pts not increasing at %d", i)
		}
	}
	if !pkts[0].key {
		t.Fatal("first packet is not a keyframe")
	}
	pngs := decodeVideoPNGs(t, path, plan.FPS, plan.Frames)
	for i, b := range pngs {
		got, err := readFrameCode(b)
		if err != nil {
			t.Fatal(err)
		}
		if got != i {
			t.Fatalf("code i=%d got %d", i, got)
		}
	}
}

func assertChunkKeyframes(t *testing.T, path string, starts []int, fps int) {
	t.Helper()
	pkts := readVideoPackets(t, path)
	for _, n := range starts {
		if n < 0 || n >= len(pkts) {
			t.Fatalf("start %d packets %d", n, len(pkts))
		}
		if !pkts[n].key {
			t.Fatalf("frame %d is not a keyframe", n)
		}
	}
}

type videoPkt struct {
	pts float64
	key bool
}

func readVideoPackets(t *testing.T, path string) []videoPkt {
	t.Helper()
	b, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_frames", "-show_entries", "frame=pts_time,key_frame", "-of", "json", path).Output()
	if err != nil {
		t.Fatal(err, string(b))
	}
	var v struct {
		Frames []struct {
			PtsTime  string `json:"pts_time"`
			KeyFrame int    `json:"key_frame"`
		} `json:"frames"`
	}
	if err = json.Unmarshal(b, &v); err != nil {
		t.Fatal(err, string(b))
	}
	out := make([]videoPkt, 0, len(v.Frames))
	for i, f := range v.Frames {
		pts, err := strconv.ParseFloat(f.PtsTime, 64)
		if err != nil {
			t.Fatalf("frame %d pts %q: %v", i, f.PtsTime, err)
		}
		out = append(out, videoPkt{pts: pts, key: f.KeyFrame == 1})
	}
	return out
}

func ffmpegKind(args []string) string {
	if len(args) == 0 {
		return "other"
	}
	last := args[len(args)-1]
	switch {
	case containsArg(args, "ffv1"):
		return "prepare"
	case strings.HasSuffix(last, "audio-0.wav"):
		return "mix-part"
	case strings.HasSuffix(last, "mix.wav"):
		return "mix"
	case containsArg(args, "png") && containsArg(args, "image2pipe"):
		return "decoder"
	case containsArg(args, "libx264") && containsArg(args, "image2pipe"):
		return "encoder"
	case containsArg(args, "aac"):
		return "mux"
	default:
		return "other"
	}
}

func indexOfKind(kinds []string, want string) int {
	for i, k := range kinds {
		if k == want {
			return i
		}
	}
	return -1
}

func runtimeEncodeThreads(p *Plan) int {
	_, _, enc := resolveRenderThreads(planThreadCfg(p), len(p.Tracks))
	return chunkEncodeThreads(enc, 1)
}

func stringSliceEqual(a, b []string) bool {
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

func leftoverWork(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{err.Error()}
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".backstage-render-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func descendantSet(root int) map[int]bool {
	out := map[int]bool{}
	for _, pid := range descendantsMatching(root, "chromium", "chrome", "ffmpeg", "ffprobe") {
		out[pid] = true
	}
	return out
}

func waitTreeIdle(t *testing.T, before map[int]bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var extra []int
		for _, pid := range descendantsMatching(os.Getpid(), "chromium", "chrome", "ffmpeg", "ffprobe") {
			if before[pid] {
				continue
			}
			if err := syscall.Kill(pid, 0); err == nil {
				extra = append(extra, pid)
			}
		}
		if len(extra) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("leftover pids %v", extra)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func commOf(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func childrenOf(pid int) []int {
	var out []int
	tasks, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
	if err != nil {
		return nil
	}
	for _, t := range tasks {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/children", pid, t.Name()))
		if err != nil {
			continue
		}
		for _, f := range strings.Fields(string(b)) {
			n, err := strconv.Atoi(f)
			if err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}

func descendantsMatching(root int, names ...string) []int {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	seen := map[int]bool{}
	var out []int
	var walk func(int)
	walk = func(pid int) {
		if seen[pid] {
			return
		}
		seen[pid] = true
		if pid != root && want[commOf(pid)] {
			out = append(out, pid)
		}
		for _, c := range childrenOf(pid) {
			walk(c)
		}
	}
	walk(root)
	return out
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func absFloat(n float64) float64 {
	if n < 0 {
		return -n
	}
	return n
}
