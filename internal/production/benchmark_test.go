package production

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/engine"
	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

// BenchmarkProduction substitutes deterministic footage for desktop recording.
// Publication, retiming, transitions, normalization and concat use the real pipeline.
func BenchmarkProduction(b *testing.B) { benchmarkProduction(b, false) }

func BenchmarkProductionCopy(b *testing.B) { benchmarkProduction(b, true) }

func benchmarkProduction(b *testing.B, copyOnly bool) {
	p, err := scene.LoadProject("../../examples/production/backstage.json")
	if err != nil {
		b.Fatal(err)
	}
	if copyOnly {
		prod := p.Productions["benchmark"]
		prod.Transitions = nil
		for i := range prod.Scenes {
			prod.Scenes[i].Speed = nil
		}
		p.Productions["benchmark"] = prod
	}
	dir := b.TempDir()
	for _, name := range []string{"plain", "speed", "final"} {
		body, err := os.ReadFile(filepath.Join(p.Dir, "scenes", name+".json"))
		if err != nil {
			b.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "scenes"), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "scenes", name+".json"), body, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	p.Dir, p.Workspace = dir, dir
	fixture := filepath.Join(dir, "source.mp4")
	cmd := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=s=1280x720:r=30:d=8", "-c:v", "libx264", "-threads", "4", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p", fixture)
	if out, err := cmd.CombinedOutput(); err != nil {
		b.Fatalf("fixture: %v: %s", err, out)
	}
	video, err := os.ReadFile(fixture)
	if err != nil {
		b.Fatal(err)
	}
	previousRun, previousTeardown := runEngine, teardownHost
	runEngine = func(_ *scene.Project, _ *scene.Scene, opts engine.Options) error {
		if err := os.WriteFile(opts.OutPath, video, 0o644); err != nil {
			return err
		}
		return facts.Write(facts.Path(opts.OutPath), facts.Facts{Result: facts.ResultOK})
	}
	teardownHost = func() error { return nil }
	b.Cleanup(func() { runEngine, teardownHost = previousRun, previousTeardown })
	out := os.Getenv("BACKSTAGE_BENCH_OUT")
	if out == "" {
		out = filepath.Join(dir, "production.mp4")
	}
	b.ResetTimer()
	for range b.N {
		if _, err := Run(Options{Context: context.Background(), Project: p, Prod: p.Productions["benchmark"], OutPath: out, Version: "benchmark"}); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	f, err := facts.Read(facts.Path(out))
	if err != nil || f.ProductionPhases == nil {
		b.Fatalf("production metrics: %+v, %v", f, err)
	}
	b.ReportMetric(f.ProductionPhases.Normalize, "normalize-s/op")
	b.ReportMetric(f.ProductionPhases.Retime, "retime-s/op")
}
