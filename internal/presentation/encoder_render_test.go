package presentation

import (
	"bytes"
	"context"
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

func requireVAAPI(t *testing.T, p *Plan) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := p.encoder().Probe(ctx, p.Width, p.Height, p.FPS); err != nil {
		t.Skip(err)
	}
}

func TestVAAPIEncoderMatchesLibx264(t *testing.T) {
	requireRenderTest(t)
	resetSeams()
	defer resetSeams()
	project, err := scene.LoadProject("../../examples/presentation/backstage.json")
	if err != nil {
		t.Fatal(err)
	}
	complete, err := Load(project, "complete")
	if err != nil {
		t.Fatal(err)
	}
	fade := staticFadePlan(t, 30, 1)
	fade.Width, fade.Height = 320, 180
	for name, plan := range map[string]*Plan{"complete": complete, "static-fade": fade} {
		t.Run(name, func(t *testing.T) {
			plan.Project.Render.Workers = 2
			plan.Project.Render.Encoder = "vaapi"
			defer func() { plan.Project.Render.Encoder = "" }()
			requireVAAPI(t, plan)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			var args [][]string
			observeMu.Lock()
			observeRender = func(n renderNote) {
				if n.EncoderArgs != nil {
					args = append(args, n.EncoderArgs)
				}
			}
			observeMu.Unlock()
			render := func(encoder string) (string, string) {
				plan.Project.Render.Encoder = encoder
				useTempCache(t)
				out := filepath.Join(t.TempDir(), encoder+".mp4")
				var log bytes.Buffer
				if err := Render(ctx, plan, out, &log); err != nil {
					t.Fatal(err)
				}
				return out, log.String()
			}
			ref, _ := render("libx264")
			args = nil
			out, log := render("vaapi")
			if !strings.Contains(log, " layered\n") {
				t.Fatalf("no layered chunk used the GPU encoder:\n%s", log)
			}
			if len(args) == 0 {
				t.Fatal("no encoder ran")
			}
			for _, a := range args {
				if !containsArg(a, "h264_vaapi") || containsArg(a, "libx264") {
					t.Fatalf("chunk encoder %v", a)
				}
			}
			assertStaticFadePTS(t, out, plan.Frames, plan.FPS)
			assertStaticFadeQuality(t, ref, out)
		})
	}
}

func TestVAAPIDraftPreview(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	project, err := scene.LoadProject("../../examples/presentation/backstage.json")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Load(project, "complete")
	if err != nil {
		t.Fatal(err)
	}
	plan.Project.Render.Encoder = "vaapi"
	defer func() { plan.Project.Render.Encoder = "" }()
	requireVAAPI(t, plan)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out := filepath.Join(t.TempDir(), "draft.mp4")
	if err := writePreview(ctx, plan, out, io.Discard, PreviewOpts{From: 1, To: 3, Scale: 0.5}); err != nil {
		t.Fatal(err)
	}
	assertStaticFadePTS(t, out, 2*plan.FPS, plan.FPS)
}

func TestVAAPIProbeFailureStopsRender(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	resetSeams()
	defer resetSeams()
	real, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found")
	}
	writeFFmpegShim(t, `case "$*" in *h264_vaapi*) echo no-vaapi-device >&2; exit 9;; esac
exec `+strconv.Quote(real)+` "$@"`)
	plan := staticFadePlan(t, 10, 0.4)
	plan.Project.Render.Encoder = "vaapi"
	out := filepath.Join(t.TempDir(), "keep.mp4")
	if err := os.WriteFile(out, []byte("prior mp4"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var log bytes.Buffer
	err = Render(ctx, plan, out, &log)
	if err == nil || !strings.Contains(err.Error(), "render.encoder vaapi") || !strings.Contains(err.Error(), "no-vaapi-device") {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(log.String(), "chunk-") {
		t.Fatalf("chunks ran after the probe failed:\n%s", log.String())
	}
	if b, err := os.ReadFile(out); err != nil || string(b) != "prior mp4" {
		t.Fatal("replaced output", err)
	}
	if leftover := leftoverWork(filepath.Dir(out)); len(leftover) > 0 {
		t.Fatalf("work remains %v", leftover)
	}
}
