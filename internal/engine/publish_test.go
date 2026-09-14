package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/take"
)

func playEngine(t *testing.T, dir string, rec recorderOr) *Engine {
	t.Helper()
	var ord []string
	if rec == nil {
		rec = &fakeRec{order: &ord}
	}
	return &Engine{
		Project: &scene.Project{
			Dir:     dir,
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    rec,
		Prompt: &fakePrompt{},
		Speed:  0.0001,
	}
}

type recorderOr interface {
	Start(string) error
	Stop() (string, error)
}

func TestDefaultPlayPublishesGeneration(t *testing.T) {
	dir := t.TempDir()
	e := playEngine(t, dir, nil)
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}
	if err := e.Run(s, Options{Record: true, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	h, err := take.Open(e.Project, "demo")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if !strings.Contains(h.Clip, filepath.Join(".takes", "demo")) {
		t.Fatalf("expected generation clip, got %s", h.Clip)
	}
	stable := filepath.Join(dir, "recordings", "demo.mp4")
	if _, err := os.Stat(stable); err != nil {
		t.Fatal(err)
	}
	got := readFacts(t, stable)
	if got.Result != facts.ResultOK || got.Backstage != "v" {
		t.Fatalf("projected facts: %+v", got)
	}
}

func TestFailedPlayBecomesAttempt(t *testing.T) {
	dir := t.TempDir()
	e := playEngine(t, dir, nil)
	ok := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}
	if err := e.Run(ok, Options{Record: true, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "recordings", "demo.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	e = playEngine(t, dir, nil)
	fail := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "unknown"}}}
	err = e.Run(fail, Options{Record: true, Speed: 0.0001, Version: "v"})
	if err == nil || !strings.Contains(err.Error(), "step") {
		t.Fatalf("step error: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "recordings", "demo.mp4"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("stable path changed after a failed take")
	}
	h, err := take.Open(e.Project, "demo")
	if err != nil {
		t.Fatal(err)
	}
	h.Close()
	attempts, err := filepath.Glob(filepath.Join(dir, "recordings", ".takes", "demo", "attempts", "*", "clip.mp4"))
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts: %v %v", attempts, err)
	}
	got := readFacts(t, attempts[0])
	if got.Result != facts.ResultStepsFailed {
		t.Fatalf("attempt facts: %+v", got)
	}
}

func TestRehearsalCreatesNoTakes(t *testing.T) {
	dir := t.TempDir()
	e := playEngine(t, dir, nil)
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}
	if err := e.Run(s, Options{Record: false, Speed: 0.0001}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "recordings", ".takes")); !os.IsNotExist(err) {
		t.Fatal("rehearsal created .takes")
	}
}

func TestHookFailureCreatesNoTakes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "hooks", "reset.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := playEngine(t, dir, nil)
	e.Project.Hooks.Reset = "hooks/reset.sh"
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}
	if err := e.Run(s, Options{Record: true, Speed: 0.0001}); err == nil {
		t.Fatal("expected hook failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "recordings", ".takes")); !os.IsNotExist(err) {
		t.Fatal("hook failure left .takes")
	}
}

func TestRecorderStartFailureDiscardsEmptyPending(t *testing.T) {
	dir := t.TempDir()
	e := playEngine(t, dir, &startErrRec{startErr: os.ErrInvalid})
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}
	if err := e.Run(s, Options{Record: true, Speed: 0.0001}); err == nil {
		t.Fatal("expected recorder failure")
	}
	matches, err := filepath.Glob(filepath.Join(dir, "recordings", ".takes", "demo", ".pending-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("empty pending left: %v", matches)
	}
}

type errStager struct{ err error }

func (e errStager) Setup(scene.Layout, *scene.Project) (*scene.Manifest, error) {
	return nil, e.err
}
func (e errStager) Teardown() error { return nil }

func TestShowStagingSetupFailureStopsRecorderBeforeSessionCleanup(t *testing.T) {
	dir := t.TempDir()
	rec := &statOnStopRec{}
	e := playEngine(t, dir, rec)
	e.Stager = errStager{err: os.ErrPermission}
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}
	if err := e.Run(s, Options{Record: true, ShowStaging: true, Speed: 0.0001}); err == nil {
		t.Fatal("expected stage failure")
	}
	if !rec.stopped {
		t.Fatal("recorder was not stopped")
	}
	if !rec.existed {
		t.Fatal("session cleanup deleted the clip under the active recorder")
	}
}

type statOnStopRec struct {
	out     string
	stopped bool
	existed bool
}

func (r *statOnStopRec) Start(out string) error {
	r.out = out
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return err
	}
	return os.WriteFile(out, []byte("clip"), 0o644)
}

func (r *statOnStopRec) Stop() (string, error) {
	r.stopped = true
	_, err := os.Stat(r.out)
	r.existed = err == nil
	return r.out, nil
}

func TestCleanupOnInterruptMovesPendingClipToAttempts(t *testing.T) {
	dir := t.TempDir()
	e := playEngine(t, dir, nil)
	s, err := take.Begin(e.Project, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Clip(), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.takeSess.Store(s)
	e.cleanupOnInterrupt(nil)
	if !s.Finished() {
		t.Fatal("interrupt should finish the session")
	}
	attempts, err := filepath.Glob(filepath.Join(dir, "recordings", ".takes", "demo", "attempts", "*", "clip.mp4"))
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts: %v %v", attempts, err)
	}
}

func TestStageFailureBeforeRecorderCreatesNoTakes(t *testing.T) {
	dir := t.TempDir()
	e := playEngine(t, dir, nil)
	e.Stager = errStager{err: os.ErrPermission}
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}
	if err := e.Run(s, Options{Record: true, Speed: 0.0001}); err == nil {
		t.Fatal("expected stage failure")
	}
	if _, err := os.Stat(filepath.Join(dir, "recordings", ".takes")); !os.IsNotExist(err) {
		t.Fatal("stage failure created .takes")
	}
}
