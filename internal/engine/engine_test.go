package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/prompter"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

// --- fakes -------------------------------------------------------------------

type fakePane struct {
	runs  []string
	types []string
	keys  [][]string
}

func (f *fakePane) Run(t, c string) error  { f.runs = append(f.runs, t+":"+c); return nil }
func (f *fakePane) Type(t, c string) error { f.types = append(f.types, t+":"+c); return nil }
func (f *fakePane) Keys(t string, c []string, _ time.Duration) error {
	f.keys = append(f.keys, c)
	return nil
}

type fakePrompt struct {
	shown  []string
	closed int
}

func (f *fakePrompt) Show(text string, _ prompter.Opts) error {
	f.shown = append(f.shown, text)
	return nil
}
func (f *fakePrompt) Close() error { f.closed++; return nil }

func newTestEngine(p *scene.Project) (*Engine, *fakePane, *fakePrompt) {
	fp := &fakePane{}
	pr := &fakePrompt{}
	e := &Engine{Project: p, Prompt: pr, Speed: 0.0001}
	e.pane = fp
	return e, fp, pr
}

// --- tests -------------------------------------------------------------------

func TestResolveAlias(t *testing.T) {
	p := &scene.Project{Aliases: map[string]scene.Alias{
		"okt-terminal": {Action: "keys", Target: "okt"},
	}}
	e := &Engine{Project: p}
	a, tg := e.resolve(scene.Step{Action: "okt-terminal", Commands: []string{"m"}})
	if a != "keys" || tg != "okt" {
		t.Errorf("resolve alias = (%s,%s), want (keys,okt)", a, tg)
	}
	// explicit target wins over alias default
	a, tg = e.resolve(scene.Step{Action: "okt-terminal", Target: "other"})
	if a != "keys" || tg != "other" {
		t.Errorf("resolve explicit target = (%s,%s), want (keys,other)", a, tg)
	}
	// non-alias passes through
	a, tg = e.resolve(scene.Step{Action: "run", Target: "agent"})
	if a != "run" || tg != "agent" {
		t.Errorf("resolve passthrough = (%s,%s)", a, tg)
	}
}

func TestRunStepDispatch(t *testing.T) {
	e, fp, pr := newTestEngine(&scene.Project{Popup: scene.PopupCfg{CPS: 1000}})
	_ = e.runStep(0, scene.Step{Action: "run", Target: "agent", Value: "git status"})
	_ = e.runStep(1, scene.Step{Action: "type", Target: "agent", Value: "hi"})
	_ = e.runStep(2, scene.Step{Action: "keys", Target: "okt", Commands: []string{"m", "right arrow"}})
	_ = e.runStep(3, scene.Step{Action: "dialog", Value: "olá"})
	_ = e.runStep(4, scene.Step{Action: "wait"})

	if len(fp.runs) != 1 || fp.runs[0] != "agent:git status" {
		t.Errorf("run dispatch = %v", fp.runs)
	}
	if len(fp.types) != 1 || fp.types[0] != "agent:hi" {
		t.Errorf("type dispatch = %v", fp.types)
	}
	if len(fp.keys) != 1 || len(fp.keys[0]) != 2 {
		t.Errorf("keys dispatch = %v", fp.keys)
	}
	if len(pr.shown) != 1 || pr.shown[0] != "olá" || pr.closed != 1 {
		t.Errorf("dialog dispatch shown=%v closed=%d", pr.shown, pr.closed)
	}
}

func TestActProp(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "ok.sh")
	if err := os.WriteFile(good, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.sh")
	if err := os.WriteFile(bad, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Project: &scene.Project{Dir: dir}, Speed: 1}

	if err := e.actProp(scene.Step{Action: "prop", Value: "ok.sh"}); err != nil {
		t.Errorf("prop ok.sh should succeed: %v", err)
	}
	if err := e.actProp(scene.Step{Action: "prop", Value: "bad.sh"}); err == nil {
		t.Error("prop bad.sh should report non-zero exit")
	}
	if err := e.actProp(scene.Step{Action: "prop", Value: ""}); err != nil {
		t.Errorf("empty prop should no-op: %v", err)
	}
}

func TestSpeedScaling(t *testing.T) {
	// a tiny factor collapses a 10s sleep to ~1ms
	e := &Engine{Speed: 0.0001}
	t0 := time.Now()
	e.sleep(10)
	if d := time.Since(t0); d > 100*time.Millisecond {
		t.Errorf("speed factor not applied: slept %v", d)
	}
}

// --- fakes for stage/recorder (clip + staging tests) ---

type fakeStager struct {
	order *[]string
	m     *scene.Manifest
}

func (f *fakeStager) Setup(_ scene.Layout, _ *scene.Project) (*scene.Manifest, error) {
	*f.order = append(*f.order, "stage")
	return f.m, nil
}
func (f *fakeStager) Teardown() error { return nil }

type fakeRec struct {
	order *[]string
	out   string
}

func (f *fakeRec) Start(out string) error {
	f.out = out
	*f.order = append(*f.order, "rec")
	return nil
}
func (f *fakeRec) Stop() (string, error) { return f.out, nil }

func runClip(t *testing.T, opts Options) (order []string, recOut string) {
	t.Helper()
	if opts.Speed == 0 {
		opts.Speed = 0.0001 // collapse real sleeps
	}
	var ord []string
	rec := &fakeRec{order: &ord}
	e := &Engine{
		Project: &scene.Project{
			Dir:     t.TempDir(),
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    rec,
		Prompt: &fakePrompt{},
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}
	if err := e.Run(s, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return ord, rec.out
}

func TestRecordHidesStagingByDefault(t *testing.T) {
	order, out := runClip(t, Options{Record: true, OutPath: "/tmp/clip.mp4"})
	if len(order) != 2 || order[0] != "stage" || order[1] != "rec" {
		t.Errorf("default should record after staging, got %v", order)
	}
	if out != "/tmp/clip.mp4" {
		t.Errorf("OutPath not honored: %s", out)
	}
}

func TestShowStagingRecordsFirst(t *testing.T) {
	order, _ := runClip(t, Options{Record: true, OutPath: "/tmp/clip.mp4", ShowStaging: true})
	if len(order) != 2 || order[0] != "rec" || order[1] != "stage" {
		t.Errorf("ShowStaging should record before staging, got %v", order)
	}
}

func TestRunReturnsStepErrors(t *testing.T) {
	var ord []string
	e := &Engine{
		Project: &scene.Project{
			Dir:     t.TempDir(),
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Prompt: &fakePrompt{},
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "unknown"}}}
	if err := e.Run(s, Options{Record: false, Speed: 0.0001}); err == nil {
		t.Fatal("Run should return a step error")
	}
}
