package presentation

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestStaticFadeWithoutTracksMatchesFrames(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	for _, tc := range []struct {
		name     string
		fps      int
		fade     float64
		reversed bool
	}{
		{"short", 10, 0.4, false},
		{"one-second", 30, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := staticFadePlan(t, tc.fps, tc.fade)
			plan.Project.Render.Workers = 1
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			var log bytes.Buffer
			times := map[string]time.Duration{}
			paths := map[string]string{}
			render := func(mode string) {
				layerMode = mode
				useTempCache(t)
				path := filepath.Join(t.TempDir(), mode+".mp4")
				start := time.Now()
				output := io.Writer(io.Discard)
				if mode == "auto" {
					output = &log
				}
				if err := Render(ctx, plan, path, output); err != nil {
					t.Fatal(err)
				}
				times[mode] = time.Since(start)
				paths[mode] = path
			}
			order := []string{"frames", "auto"}
			if tc.reversed {
				order[0], order[1] = order[1], order[0]
			}
			for _, mode := range order {
				render(mode)
			}
			t.Logf("cold render frames=%s auto=%s", times["frames"], times["auto"])
			if tc.name == "one-second" {
				for _, mode := range []string{"frames", "auto"} {
					timings := asObject(t, readFactsMap(t, paths[mode])["timings"], "timings")
					t.Logf("%s chunk-seconds=%v", mode, timings["chunk-seconds"])
				}
			}
			for _, ch := range planChunks(plan, 0, plan.Frames) {
				if chunkTransition(plan, ch) && !strings.Contains(log.String(), fmt.Sprintf(">> chunk-%d layered\n", ch.Index)) {
					t.Fatalf("static fade chunk %d did not use layered rendering:\n%s", ch.Index, log.String())
				}
			}
			assertStaticFadePTS(t, paths["frames"], plan.Frames, plan.FPS)
			assertStaticFadePTS(t, paths["auto"], plan.Frames, plan.FPS)
			assertStaticFadeQuality(t, paths["frames"], paths["auto"])
		})
	}
}

func TestStaticFadePreviewOffsetMatchesFrames(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	plan := staticFadePlan(t, 30, 1)
	plan.Project.Render.Workers = 1
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	opts := PreviewOpts{From: 0.6, To: 1.1, Scale: 1}
	layerMode = "frames"
	useTempCache(t)
	ref := filepath.Join(t.TempDir(), "frames.mp4")
	if err := writePreview(ctx, plan, ref, io.Discard, opts); err != nil {
		t.Fatal(err)
	}
	layerMode = "auto"
	useTempCache(t)
	out := filepath.Join(t.TempDir(), "auto.mp4")
	var log bytes.Buffer
	if err := writePreview(ctx, plan, out, &log, opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), " layered\n") {
		t.Fatalf("preview did not use layered rendering:\n%s", log.String())
	}
	assertStaticFadePTS(t, ref, 15, plan.FPS)
	assertStaticFadePTS(t, out, 15, plan.FPS)
	assertStaticFadeQuality(t, ref, out)
}

func staticFadeChunk(t *testing.T, plan *Plan) chunkRange {
	t.Helper()
	for _, ch := range planChunks(plan, 0, plan.Frames) {
		if chunkTransition(plan, ch) {
			return ch
		}
	}
	t.Fatal("no fade chunk")
	return chunkRange{}
}

func TestStaticFadeCacheReuse(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := staticFadePlan(t, 10, 0.4)
	plan.Project.Render.Workers = 1
	fade := staticFadeChunk(t, plan)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var log bytes.Buffer
	cold := filepath.Join(t.TempDir(), "cold.mp4")
	if err := Render(ctx, plan, cold, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), fmt.Sprintf(">> chunk-%d layered\n", fade.Index)) {
		t.Fatalf("cold render did not use layered fade:\n%s", log.String())
	}
	log.Reset()
	warm := filepath.Join(t.TempDir(), "warm.mp4")
	if err := Render(ctx, plan, warm, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), fmt.Sprintf(">> chunk-%d cached\n", fade.Index)) {
		t.Fatalf("warm render missed fade cache:\n%s", log.String())
	}
	assertStaticFadePTS(t, warm, plan.Frames, plan.FPS)
	assertStaticFadeQuality(t, cold, warm)
}

func TestStaticFadeCancelLeavesPriorMP4(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	plan := staticFadePlan(t, 10, 0.4)
	plan.Project.Render.Workers = 1
	fade := staticFadeChunk(t, plan)
	out := filepath.Join(t.TempDir(), "keep.mp4")
	if err := os.WriteFile(out, []byte("prior mp4"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cancelled bool
	observeComposedFrame = func(n int, _ float64, _ []byte) {
		if n >= fade.First && n < fade.End {
			cancelled = true
			cancel()
		}
	}
	before := descendantSet(os.Getpid())
	if err := Render(ctx, plan, out, io.Discard); err == nil {
		t.Fatal("expected cancel")
	}
	if !cancelled {
		t.Fatal("fade chunk was not composed")
	}
	if b, err := os.ReadFile(out); err != nil || string(b) != "prior mp4" {
		t.Fatal("replaced output", err)
	}
	waitTreeIdle(t, before)
	if leftover := leftoverWork(filepath.Dir(out)); len(leftover) > 0 {
		t.Fatalf("work remains %v", leftover)
	}
}

func TestStaticFadeFFmpegErrorFailsRender(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	real, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found")
	}
	writeFFmpegShim(t, `case "$*" in *blend=*) echo fade-blend-fail >&2; exit 7;; esac
exec `+strconv.Quote(real)+` "$@"`)
	plan := staticFadePlan(t, 10, 0.4)
	plan.Project.Render.Workers = 1
	out := filepath.Join(t.TempDir(), "keep.mp4")
	if err := os.WriteFile(out, []byte("prior mp4"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	err = Render(ctx, plan, out, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "fade-blend-fail") {
		t.Fatalf("err=%v", err)
	}
	if b, err := os.ReadFile(out); err != nil || string(b) != "prior mp4" {
		t.Fatal("replaced output", err)
	}
	if leftover := leftoverWork(filepath.Dir(out)); len(leftover) > 0 {
		t.Fatalf("work remains %v", leftover)
	}
}

func assertStaticFadeQuality(t *testing.T, ref, out string) {
	t.Helper()
	minY, avgY, err := videoPSNR(ref, out)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("PSNR-Y min=%.2f avg=%.2f", minY, avgY)
	if minY < 40 || avgY < 45 {
		t.Fatalf("PSNR-Y min=%.2f avg=%.2f", minY, avgY)
	}
}

func TestDynamicFadeUsesFrames(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	project, err := scene.LoadProject("../../examples/presentation/backstage.json")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Load(project, "short")
	if err != nil {
		t.Fatal(err)
	}
	plan.Project.Render.Workers = 1
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var log bytes.Buffer
	if err := writePreview(ctx, plan, filepath.Join(t.TempDir(), "dynamic.mp4"), &log, PreviewOpts{From: 2, To: 2.5, Scale: 1}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), " layered\n") {
		t.Fatalf("dynamic fade used layered rendering:\n%s", log.String())
	}
}

func assertStaticFadePTS(t *testing.T, path string, frames, fps int) {
	t.Helper()
	packets := readVideoPackets(t, path)
	if len(packets) != frames {
		t.Fatalf("%s frames=%d want=%d", filepath.Base(path), len(packets), frames)
	}
	for i, packet := range packets {
		want := float64(i) / float64(fps)
		if absFloat(packet.pts-want) > 1e-4 {
			t.Fatalf("%s PTS[%d]=%g want=%g", filepath.Base(path), i, packet.pts, want)
		}
	}
}

func staticFadePlan(t *testing.T, fps int, fade float64) *Plan {
	t.Helper()
	project := writeFixture(t, Document{
		Version:  1,
		Duration: 0.8 + fade,
		Render:   scene.RenderCfg{W: 160, H: 90, FPS: fps},
		Timeline: []Event{
			{At: 0, Scene: "old"},
			{At: 0.4, Scene: "new", Transition: Transition{Effect: "fade", Duration: fade}},
		},
	})
	for _, s := range []struct{ name, color, x string }{
		{"old", "#bf3434", "8"},
		{"new", "#3485bf", "80"},
	} {
		html := `<!doctype html><meta charset="utf-8"><style>html,body{margin:0;width:100%;height:100%;overflow:hidden;background:#222}#card{position:absolute;top:15px;left:` + s.x + `px;width:72px;height:60px;background:` + s.color + `}</style><div id="card"></div><script>window.backstageTemplate={version:1,static:true};window.render=async function(){};</script>`
		if err := os.WriteFile(filepath.Join(project.Dir, s.name+".html"), []byte(html), 0o600); err != nil {
			t.Fatal(err)
		}
		meta := fmt.Sprintf(`{"type":"visual","entry":"%s.html","duration":2}`, s.name)
		if err := os.WriteFile(filepath.Join(project.Dir, "scenes", s.name+".json"), []byte(meta), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := Load(project, "show")
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
