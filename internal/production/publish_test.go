package production

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/engine"
	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/take"
)

func TestProducePublishesRawTakeNotRetime(t *testing.T) {
	pr := testProducer(t, 2)
	restore := stubRunOK(t, "raw")
	defer restore()
	retimeClip = func(clip string, _ []scene.Segment, _ string) (string, error) {
		out := clip + ".retime"
		if err := os.WriteFile(out, []byte("retimed"), 0o644); err != nil {
			return "", err
		}
		return out, nil
	}
	defer func() { retimeClip = Retime }()

	sg := segment{kind: "scene", name: "alpha", speed: []scene.Segment{{Rate: 10}}}
	if err := pr.recordScene(0, sg); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(pr.raw[0], ".retime") {
		t.Fatalf("production used %s, want the retimed copy", pr.raw[0])
	}
	h, err := take.Open(pr.opts.Project, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	body, err := os.ReadFile(h.Clip)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "raw-alpha" {
		t.Fatalf("published %q, want the raw take", body)
	}
	stable, err := os.ReadFile(filepath.Join(pr.opts.Project.Dir, "recordings", "alpha.mp4"))
	if err != nil || string(stable) != "raw-alpha" {
		t.Fatalf("stable path: %s %v", stable, err)
	}
}

func TestProduceKeepsEarlierPublishWhenLaterSceneFails(t *testing.T) {
	pr := testProducer(t, 2)
	restore := stubRunByName(t, map[string]stubTake{
		"alpha": {body: "first", result: facts.ResultOK},
		"beta":  {body: "second", result: facts.ResultStepsFailed, err: errors.New("step 1")},
	})
	defer restore()
	if err := pr.recordScene(0, segment{kind: "scene", name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := pr.recordScene(1, segment{kind: "scene", name: "beta"}); err == nil {
		t.Fatal("beta should fail")
	}
	h, err := take.Open(pr.opts.Project, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	h.Close()
	if _, err := take.Open(pr.opts.Project, "beta"); err == nil {
		t.Fatal("failed scene was published")
	}
	attempts := filepath.Join(pr.opts.Project.Dir, "recordings", ".takes", "beta", "attempts")
	entries, err := os.ReadDir(attempts)
	if err != nil || len(entries) == 0 {
		t.Fatalf("failed scene attempt: %v %v", entries, err)
	}
}

func TestProduceVMEndWithoutEndStateIsAttempt(t *testing.T) {
	pr := testProducer(t, 1)
	writeProduceVMEndScene(t, pr.opts.Project.Dir, "alpha")
	pr.opts.Project.VMs = map[string]scene.VMCfg{"box": {Stage: "demo"}}
	restore := stubRunByName(t, map[string]stubTake{
		"alpha": {body: "ok", result: facts.ResultOK},
	})
	defer restore()
	if err := pr.recordScene(0, segment{kind: "scene", name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if _, err := take.Open(pr.opts.Project, "alpha"); err == nil {
		t.Fatal("published vm-end take without end-state")
	}
	attempts := filepath.Join(pr.opts.Project.Dir, "recordings", ".takes", "alpha", "attempts")
	if entries, err := os.ReadDir(attempts); err != nil || len(entries) == 0 {
		t.Fatalf("attempt: %v %v", entries, err)
	}
}

func TestProduceReadonlyWorkDirDoesNotPublish(t *testing.T) {
	pr := testProducer(t, 1)
	writeProduceVMEndScene(t, pr.opts.Project.Dir, "alpha")
	pr.opts.Project.VMs = map[string]scene.VMCfg{"box": {Stage: "demo"}}
	prev := runEngine
	runEngine = func(_ *scene.Project, _ *scene.Scene, opts engine.Options) error {
		if err := os.MkdirAll(filepath.Dir(opts.OutPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(opts.OutPath, []byte("clip-alpha"), 0o644); err != nil {
			return err
		}
		if err := facts.Write(facts.Path(opts.OutPath), facts.Facts{Result: facts.ResultOK, Backstage: "t"}); err != nil {
			return err
		}
		if err := os.Chmod(filepath.Dir(opts.OutPath), 0o555); err != nil {
			return err
		}
		return errors.Join(engine.ErrCaptureFailed, errors.New("no space left on device"))
	}
	t.Cleanup(func() {
		runEngine = prev
		_ = os.Chmod(pr.segDir, 0o755)
	})
	err := pr.recordScene(0, segment{kind: "scene", name: "alpha"})
	_ = os.Chmod(pr.segDir, 0o755)
	if !errors.Is(err, engine.ErrCaptureFailed) {
		t.Fatalf("capture failed: %v", err)
	}
	if _, err := take.Open(pr.opts.Project, "alpha"); err == nil {
		t.Fatal("published a take whose capture failed")
	}
	attempts := filepath.Join(pr.opts.Project.Dir, "recordings", ".takes", "alpha", "attempts")
	if entries, err := os.ReadDir(attempts); err != nil || len(entries) == 0 {
		t.Fatalf("attempt: %v %v", entries, err)
	}
}

func TestProduceCaptureFailedIsAttempt(t *testing.T) {
	pr := testProducer(t, 1)
	restore := stubRunByName(t, map[string]stubTake{
		"alpha": {body: "cap", result: facts.ResultCaptureFailed, err: errors.New("capture failed")},
	})
	defer restore()
	err := pr.recordScene(0, segment{kind: "scene", name: "alpha"})
	if err == nil || !strings.Contains(err.Error(), "capture") {
		t.Fatalf("capture-failed take: %v", err)
	}
	if _, err := take.Open(pr.opts.Project, "alpha"); err == nil {
		t.Fatal("capture-failed take was published")
	}
	attempts := filepath.Join(pr.opts.Project.Dir, "recordings", ".takes", "alpha", "attempts")
	if entries, err := os.ReadDir(attempts); err != nil || len(entries) == 0 {
		t.Fatalf("capture-failed attempt: %v %v", entries, err)
	}
}

func TestProduceFailedTakeIsAttemptWithoutKeepSegments(t *testing.T) {
	pr := testProducer(t, 1)
	pr.opts.KeepSegments = false
	restore := stubRunByName(t, map[string]stubTake{
		"alpha": {body: "bad", result: facts.ResultShort, err: errors.New("clip missing")},
	})
	defer restore()
	err := pr.recordScene(0, segment{kind: "scene", name: "alpha"})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("failed take: %v", err)
	}
	if _, err := take.Open(pr.opts.Project, "alpha"); err == nil {
		t.Fatal("failed take was published")
	}
	attempts := filepath.Join(pr.opts.Project.Dir, "recordings", ".takes", "alpha", "attempts")
	if entries, err := os.ReadDir(attempts); err != nil || len(entries) == 0 {
		t.Fatalf("attempt: %v %v", entries, err)
	}
}

func TestProduceSpeedAndShowStagingStayInWorkDir(t *testing.T) {
	t.Run("speed", func(t *testing.T) {
		pr := testProducer(t, 1)
		pr.speed = 0.5
		pr.opts.Speed = 0.5
		restore := stubRunOK(t, "slow")
		defer restore()
		if err := pr.recordScene(0, segment{kind: "scene", name: "alpha"}); err != nil {
			t.Fatal(err)
		}
		assertNoTakes(t, pr.opts.Project, "alpha")
	})
	t.Run("show-staging", func(t *testing.T) {
		pr := testProducer(t, 1)
		pr.opts.ShowStaging = true
		restore := stubRunByName(t, map[string]stubTake{
			"alpha": {body: "stage", result: facts.ResultStepsFailed, err: errors.New("step")},
		})
		defer restore()
		if err := pr.recordScene(0, segment{kind: "scene", name: "alpha"}); err == nil {
			t.Fatal("expected step error")
		}
		assertNoTakes(t, pr.opts.Project, "alpha")
	})
}

func TestProducePublishesOKFactsDespiteLaterRunError(t *testing.T) {
	pr := testProducer(t, 1)
	restore := stubRunByName(t, map[string]stubTake{
		"alpha": {body: "ok", result: facts.ResultOK, err: errors.New("continuity")},
	})
	defer restore()
	err := pr.recordScene(0, segment{kind: "scene", name: "alpha"})
	if err == nil || !strings.Contains(err.Error(), "continuity") {
		t.Fatalf("run error: %v", err)
	}
	h, err := take.Open(pr.opts.Project, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	h.Close()
}

func TestProduceTeardownErrorKeepsPublish(t *testing.T) {
	pr := testProducer(t, 1)
	restore := stubRunOK(t, "ok")
	defer restore()
	teardownHost = func() error { return errors.New("hypr down") }
	defer func() { teardownHost = func() error { return nil } }()
	err := pr.recordScene(0, segment{kind: "scene", name: "alpha"})
	if err == nil || !strings.Contains(err.Error(), "hypr down") {
		t.Fatalf("teardown: %v", err)
	}
	h, err := take.Open(pr.opts.Project, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	h.Close()
}

func TestProduceInterruptDuringImportKeepsAttempt(t *testing.T) {
	pr := testProducer(t, 1)
	var cleaned bool
	pr.cleanup = func() { cleaned = true }
	restore := stubRunOK(t, "int")
	defer restore()
	var onInt func()
	newInterruptGuard = func(_ func() error, onInterrupt func()) interruptGuard {
		onInt = onInterrupt
		return noopProductionGuard{}
	}
	defer func() {
		newInterruptGuard = func(stop func() error, onInterrupt func()) interruptGuard {
			return engine.NewInterruptGuard(stop, onInterrupt)
		}
	}()
	realImport := importTake
	importTake = func(ctx context.Context, p *scene.Project, name, clip, facts string, bind func(*take.Session)) (*take.Session, error) {
		s, err := realImport(ctx, p, name, clip, facts, func(in *take.Session) {
			bind(in)
			if onInt != nil {
				onInt()
			}
		})
		return s, err
	}
	defer func() { importTake = realImport }()
	err := pr.recordScene(0, segment{kind: "scene", name: "alpha"})
	if err == nil {
		t.Fatal("interrupted import returned nil")
	}
	if !cleaned {
		t.Fatal("work directory cleanup did not run")
	}
	assertNoTakesPublished(t, pr.opts.Project, "alpha")
	assertNoPendingOrCreating(t, pr.opts.Project, "alpha")
}

func TestFinishTakeInterruptRace(t *testing.T) {
	pr := testProducer(t, 1)
	restore := stubRunOK(t, "race")
	defer restore()
	var onInt func()
	newInterruptGuard = func(_ func() error, onInterrupt func()) interruptGuard {
		onInt = onInterrupt
		return noopProductionGuard{}
	}
	defer func() {
		newInterruptGuard = func(stop func() error, onInterrupt func()) interruptGuard {
			return engine.NewInterruptGuard(stop, onInterrupt)
		}
	}()
	started := make(chan struct{})
	realImport := importTake
	importTake = func(ctx context.Context, p *scene.Project, name, clip, facts string, bind func(*take.Session)) (*take.Session, error) {
		return realImport(ctx, p, name, clip, facts, func(s *take.Session) {
			bind(s)
			close(started)
			time.Sleep(30 * time.Millisecond)
		})
	}
	defer func() { importTake = realImport }()
	errCh := make(chan error, 1)
	go func() {
		errCh <- pr.recordScene(0, segment{kind: "scene", name: "alpha"})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("import did not bind")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if onInt != nil {
				onInt()
			}
		}()
	}
	wg.Wait()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("recordScene did not return")
	}
}

func TestProduceInterruptDuringPublishKeepsValidState(t *testing.T) {
	pr := testProducer(t, 1)
	restore := stubRunOK(t, "pub")
	defer restore()
	var onInt func()
	newInterruptGuard = func(_ func() error, onInterrupt func()) interruptGuard {
		onInt = onInterrupt
		return noopProductionGuard{}
	}
	defer func() {
		newInterruptGuard = func(stop func() error, onInterrupt func()) interruptGuard {
			return engine.NewInterruptGuard(stop, onInterrupt)
		}
	}()
	realImport := importTake
	importTake = func(ctx context.Context, p *scene.Project, name, clip, facts string, bind func(*take.Session)) (*take.Session, error) {
		s, err := realImport(ctx, p, name, clip, facts, bind)
		if err != nil {
			return s, err
		}
		if onInt != nil {
			onInt()
		}
		return s, nil
	}
	defer func() { importTake = realImport }()
	if err := pr.recordScene(0, segment{kind: "scene", name: "alpha"}); err == nil {
		t.Fatal("publish interrupt returned nil")
	}
	assertNoTakesPublished(t, pr.opts.Project, "alpha")
	attempts := filepath.Join(pr.opts.Project.Dir, "recordings", ".takes", "alpha", "attempts")
	if entries, err := os.ReadDir(attempts); err != nil || len(entries) == 0 {
		t.Fatalf("attempt after publish interrupt: %v %v", entries, err)
	}
}

type stubTake struct {
	body   string
	result string
	err    error
}

func testProducer(t *testing.T, n int) *producer {
	t.Helper()
	dir := t.TempDir()
	writeProduceScene(t, dir, "alpha")
	writeProduceScene(t, dir, "beta")
	p := &scene.Project{
		Dir:     dir,
		Record:  scene.RecordCfg{Out: "recordings", FPS: 30},
		Render:  scene.RenderCfg{W: 64, H: 36, FPS: 30},
		Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
	}
	prevTeardown := teardownHost
	teardownHost = func() error { return nil }
	t.Cleanup(func() { teardownHost = prevTeardown })
	return &producer{
		opts:    Options{Project: p, Speed: 1, Version: "test", KeepSegments: true},
		segDir:  t.TempDir(),
		speed:   1,
		raw:     make([]string, n),
		cleanup: func() {},
	}
}

func writeProduceVMEndScene(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, "scenes", name+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"` + name + `","layout":"solo","vm":"box","vm-end":{"snapshot":"saved"},"steps":[{"action":"wait"}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeProduceScene(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, "scenes", name+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"` + name + `","layout":"solo","steps":[{"action":"wait"}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func stubRunOK(t *testing.T, body string) func() {
	t.Helper()
	return stubRunByName(t, map[string]stubTake{
		"alpha": {body: body, result: facts.ResultOK},
		"beta":  {body: body, result: facts.ResultOK},
	})
}

func stubRunByName(t *testing.T, takes map[string]stubTake) func() {
	t.Helper()
	prev := runEngine
	runEngine = func(_ *scene.Project, s *scene.Scene, opts engine.Options) error {
		st, ok := takes[s.Name]
		if !ok {
			return errors.New("unexpected scene " + s.Name)
		}
		if err := os.MkdirAll(filepath.Dir(opts.OutPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(opts.OutPath, []byte(st.body+"-"+s.Name), 0o644); err != nil {
			return err
		}
		if err := facts.Write(facts.Path(opts.OutPath), facts.Facts{Result: st.result, Backstage: "t"}); err != nil {
			return err
		}
		return st.err
	}
	return func() { runEngine = prev }
}

func assertNoTakes(t *testing.T, p *scene.Project, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(p.Dir, "recordings", ".takes", name)); !os.IsNotExist(err) {
		t.Fatalf("take tree for %s: %v", name, err)
	}
}

func assertNoTakesPublished(t *testing.T, p *scene.Project, name string) {
	t.Helper()
	if _, err := take.Open(p, name); err == nil {
		t.Fatalf("scene %s was published", name)
	}
}

func assertNoPendingOrCreating(t *testing.T, p *scene.Project, name string) {
	t.Helper()
	sceneDir := filepath.Join(p.Dir, "recordings", ".takes", name)
	entries, err := os.ReadDir(sceneDir)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, ".pending-") || strings.HasPrefix(n, ".creating-") {
			t.Fatalf("left %s", n)
		}
	}
}
