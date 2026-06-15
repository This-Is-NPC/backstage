package engine

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	shown          []string
	opts           []prompter.Opts
	closed         int
	preflights     []string
	hyprPreflights int
	preErr         error
	hyprPreErr     error
}

func (f *fakePrompt) Preflight(term string) error {
	f.preflights = append(f.preflights, term)
	return f.preErr
}
func (f *fakePrompt) PreflightHypr() error {
	f.hyprPreflights++
	return f.hyprPreErr
}
func (f *fakePrompt) Show(text string, opts prompter.Opts) error {
	f.shown = append(f.shown, text)
	f.opts = append(f.opts, opts)
	return nil
}
func (f *fakePrompt) Close() error { f.closed++; return nil }

type fakeEngineGuard struct {
	stop        func() error
	onInterrupt func()
}

func (f *fakeEngineGuard) Stop() error {
	if f.stop == nil {
		return nil
	}
	return f.stop()
}

func (f *fakeEngineGuard) Release() {}

func (f *fakeEngineGuard) Interrupt() {
	_ = f.Stop()
	if f.onInterrupt != nil {
		f.onInterrupt()
	}
}

func newTestEngine(p *scene.Project) (*Engine, *fakePane, *fakePrompt) {
	fp := &fakePane{}
	pr := &fakePrompt{}
	e := &Engine{Project: p, Prompt: pr, Speed: 0.0001}
	e.pane = fp
	return e, fp, pr
}

func TestDialogPassesPopupStyle(t *testing.T) {
	e, _, pr := newTestEngine(&scene.Project{
		Term: "ghostty",
		Popup: scene.PopupCfg{
			CPS:  1000,
			Size: []int{900, 300},
			Style: scene.PopupStyleCfg{
				FontSize: 24,
				Title:    "backstage.prompt",
				Header:   "backstage@demo:~$",
				Chrome:   "minimal",
				Class:    "backstage.demo.popup",
			},
		},
	})
	if err := e.runStep(0, scene.Step{Action: "dialog", Value: "styled"}); err != nil {
		t.Fatalf("dialog: %v", err)
	}
	if len(pr.opts) != 1 {
		t.Fatalf("expected one prompter opts, got %d", len(pr.opts))
	}
	got := pr.opts[0]
	if got.Width != 900 || got.Height != 300 || got.FontSize != 24 || got.Title != "backstage.prompt" ||
		got.Header != "backstage@demo:~$" || got.Chrome != "minimal" || got.Class != "backstage.demo.popup" {
		t.Errorf("prompter opts not propagated: %+v", got)
	}
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

func TestRunScriptStartsHookInProcessGroup(t *testing.T) {
	dir := t.TempDir()
	hook := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(hook, []byte(`#!/bin/sh
read stat < /proc/$$/stat
set -- $stat
[ "$5" = "$$" ] || exit 7
printf '%s' "$BACKSTAGE_TEST_ENV" > hook.out
`), 0o755); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Project: &scene.Project{Dir: dir, Env: map[string]string{"BACKSTAGE_TEST_ENV": "yes"}}}
	if err := e.runScript("hook.sh"); err != nil {
		t.Fatalf("runScript: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "hook.out")); err != nil || string(b) != "yes" {
		t.Fatalf("hook env output = %q, %v", b, err)
	}
}

func TestRunInterruptibleCommandSkipsStartAfterInterrupt(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	cmd := exec.Command("sh", "-c", `printf started > "$1"`, "sh", started)
	scene.SetProcessGroup(cmd)

	oldGuard := newInterruptGuard
	newInterruptGuard = func(stop func() error, onInterrupt func()) interruptGuard {
		if stop != nil {
			_ = stop()
		}
		if onInterrupt != nil {
			onInterrupt()
		}
		return &fakeEngineGuard{}
	}
	defer func() { newInterruptGuard = oldGuard }()

	err := runInterruptibleCommand(cmd)
	if !errors.Is(err, ErrCommandInterrupted) {
		t.Fatalf("runInterruptibleCommand error = %v, want %v", err, ErrCommandInterrupted)
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatalf("command started after interrupt cleanup; stat err=%v", err)
	}
}

func TestRunInterruptDuringHookKillsHookAndRunsOptionCleanup(t *testing.T) {
	dir := t.TempDir()
	ticks := filepath.Join(dir, "ticks")
	ready := filepath.Join(dir, "ready")
	cleaned := filepath.Join(dir, "cleaned")
	hook := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(hook, []byte(`#!/bin/sh
(while :; do printf x >> "$TICKS"; sleep 0.05; done) &
printf ready > "$READY"
wait
`), 0o755); err != nil {
		t.Fatal(err)
	}

	guards := make(chan *fakeEngineGuard, 2)
	oldGuard := newInterruptGuard
	newInterruptGuard = func(stop func() error, onInterrupt func()) interruptGuard {
		g := &fakeEngineGuard{stop: stop, onInterrupt: onInterrupt}
		guards <- g
		return g
	}
	defer func() { newInterruptGuard = oldGuard }()

	e := &Engine{
		Project: &scene.Project{
			Dir:   dir,
			Env:   map[string]string{"TICKS": ticks, "READY": ready},
			Hooks: scene.Hooks{Setup: "hook.sh"},
			Layouts: map[string]scene.Layout{
				"screen": {Panes: []scene.Pane{}},
			},
		},
		Prompt: &fakePrompt{},
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "demo", Layout: "screen", Fresh: true}
	errCh := make(chan error, 1)
	go func() {
		errCh <- e.Run(s, Options{Record: false, Speed: 0.0001, OnInterrupt: func() {
			_ = os.WriteFile(cleaned, []byte("yes"), 0o644)
		}})
	}()

	var guard *fakeEngineGuard
	select {
	case guard = <-guards:
	case <-time.After(2 * time.Second):
		t.Fatal("interrupt guard was not installed before hooks")
	}
	waitForTestFile(t, ready)
	guard.Interrupt()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("killed hook should report an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Engine.Run did not return after interrupt cleanup killed the hook")
	}
	if len(guards) != 0 {
		t.Fatalf("hook installed a competing interrupt guard")
	}
	if b, err := os.ReadFile(cleaned); err != nil || string(b) != "yes" {
		t.Fatalf("OnInterrupt cleanup output = %q, %v", b, err)
	}
	before, _ := os.ReadFile(ticks)
	time.Sleep(200 * time.Millisecond)
	after, _ := os.ReadFile(ticks)
	if len(after) != len(before) {
		t.Fatalf("hook child process kept running after process-group kill: before=%d after=%d", len(before), len(after))
	}
}

func TestActTransition(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args.log")
	script := filepath.Join(dir, "live.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\n' \"$@\" > args.log\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Project: &scene.Project{Dir: dir, Transitions: map[string]scene.Transition{
		"chapter": {Live: scene.LiveTransition{Prop: "live.sh", Args: []string{"--base"}}},
	}}, Speed: 1}
	if err := e.actTransition(scene.Step{Action: "transition", Value: "chapter", Args: []string{"--extra"}}); err != nil {
		t.Fatalf("transition should succeed: %v", err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), "--base\n--extra\n"; got != want {
		t.Errorf("transition args = %q, want %q", got, want)
	}
	if err := e.actTransition(scene.Step{Action: "transition", Value: "missing"}); err == nil {
		t.Error("missing transition should fail")
	}
	e.Project.Transitions["offline"] = scene.Transition{Cmd: "render --out {{out}}"}
	if err := e.actTransition(scene.Step{Action: "transition", Value: "offline"}); err == nil {
		t.Error("offline transition used as step should fail")
	}
}

func TestActTransitionSubstitutesPlaceholders(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args.log")
	script := filepath.Join(dir, "live.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > args.log\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Project: &scene.Project{
		Dir:    dir,
		Render: scene.RenderCfg{W: 1920, H: 1080, FPS: 60},
		Transitions: map[string]scene.Transition{
			// {{out}} must be substituted to empty (recorder owns the clip);
			// {{w}}/{{h}}/{{fps}} come from the render config.
			"chapter": {Live: scene.LiveTransition{Prop: "live.sh", Args: []string{
				"--out", "{{out}}", "--size", "{{w}}x{{h}}", "--fps", "{{fps}}",
			}}},
		},
	}, Speed: 1}
	if err := e.actTransition(scene.Step{Action: "transition", Value: "chapter"}); err != nil {
		t.Fatalf("transition should succeed: %v", err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "--out\n\n--size\n1920x1080\n--fps\n60\n"
	if got := string(b); got != want {
		t.Errorf("transition args = %q, want %q", got, want)
	}
}

// When render.* is omitted (the common config), the in-scene live transition must
// resolve {{fps}} via the record.fps fallback (matching the production pipeline's
// intent), and substitute {{w}}/{{h}} to the native sentinel "0" rather than a
// stale render.fps of 0. Asserts the shared ResolveRenderDims behavior.
func TestActTransitionFallsBackToRecordFPS(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args.log")
	script := filepath.Join(dir, "live.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > args.log\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Project: &scene.Project{
		Dir:    dir,
		Record: scene.RecordCfg{FPS: 24}, // render.* omitted → fps falls back to 24
		Transitions: map[string]scene.Transition{
			"chapter": {Live: scene.LiveTransition{Prop: "live.sh", Args: []string{
				"--size", "{{w}}x{{h}}", "--fps", "{{fps}}",
			}}},
		},
	}, Speed: 1}
	if err := e.actTransition(scene.Step{Action: "transition", Value: "chapter"}); err != nil {
		t.Fatalf("transition should succeed: %v", err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "--size\n0x0\n--fps\n24\n"
	if got := string(b); got != want {
		t.Errorf("transition args = %q, want %q (fps must fall back to record.fps)", got, want)
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
func (f *fakeStager) Teardown() error {
	if f.order != nil {
		*f.order = append(*f.order, "teardown")
	}
	return nil
}

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

type startErrRec struct {
	startErr error
	stops    int
}

func (f *startErrRec) Start(string) error { return f.startErr }
func (f *startErrRec) Stop() (string, error) {
	f.stops++
	return "", nil
}

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

func TestEmptyPaneLayoutSkipsStaging(t *testing.T) {
	var ord []string
	rec := &fakeRec{order: &ord}
	e := &Engine{
		Project: &scene.Project{
			Dir:     t.TempDir(),
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"screen": {Panes: []scene.Pane{}}},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{}},
		Rec:    rec,
		Prompt: &fakePrompt{},
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "demo", Layout: "screen", Steps: []scene.Step{{Action: "wait"}}}
	if err := e.Run(s, Options{Record: true, OutPath: "/tmp/clip.mp4", Speed: 0.0001}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(ord) != 1 || ord[0] != "rec" {
		t.Errorf("empty-pane layout should skip stage and only record, got %v", ord)
	}
}

func TestRunStopsRecorderWhenStartFailsAfterArming(t *testing.T) {
	rec := &startErrRec{startErr: errors.New("recorder failed after spawn")}
	e := &Engine{
		Project: &scene.Project{
			Dir:     t.TempDir(),
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"screen": {Panes: []scene.Pane{}}},
		},
		Rec:    rec,
		Prompt: &fakePrompt{},
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "demo", Layout: "screen", Steps: []scene.Step{{Action: "wait"}}}
	if err := e.Run(s, Options{Record: true, OutPath: filepath.Join(t.TempDir(), "clip.mp4"), Speed: 0.0001}); err == nil {
		t.Fatal("Run should return the recorder start error")
	}
	if rec.stops != 1 {
		t.Fatalf("recorder Stop called %d times, want 1", rec.stops)
	}
}

func TestCleanupOnInterruptRunsOptionCallback(t *testing.T) {
	var order []string
	pr := &fakePrompt{}
	e := &Engine{Prompt: pr, Stager: &fakeStager{order: &order}}
	e.cleanupOnInterrupt(func() { order = append(order, "extra") })
	if pr.closed != 1 {
		t.Fatalf("Prompt.Close called %d times, want 1", pr.closed)
	}
	if got, want := strings.Join(order, ","), "teardown,extra"; got != want {
		t.Fatalf("cleanup order = %q, want %q", got, want)
	}
}

func TestDialogPreflightFailsBeforeRecording(t *testing.T) {
	var ord []string
	rec := &fakeRec{order: &ord}
	pr := &fakePrompt{preErr: errors.New("popup driver hypr-terminal requires hyprctl")}
	e := &Engine{
		Project: &scene.Project{
			Dir:     t.TempDir(),
			Term:    "ghostty",
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    rec,
		Prompt: pr,
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "dialog", Value: "hello"}}}
	err := e.Run(s, Options{Record: true, OutPath: "/tmp/clip.mp4", Speed: 0.0001})
	if err == nil {
		t.Fatal("Run should fail fast when prompter preflight fails")
	}
	if len(pr.preflights) != 1 || pr.preflights[0] != "ghostty" {
		t.Errorf("preflight should run once with configured term, got %v", pr.preflights)
	}
	if len(ord) != 0 {
		t.Errorf("no recording or staging should occur before preflight failure, got %v", ord)
	}
	if len(pr.shown) != 0 {
		t.Errorf("dialog should never be shown after preflight failure, got %v", pr.shown)
	}
}

func TestLiveTransitionStepPreflightsBeforeRecording(t *testing.T) {
	// A transition-only scene records a compositor overlay but never opens the
	// prompter terminal, so it must fail fast on a missing compositor (PreflightHypr)
	// without requiring the terminal (full Preflight must not run).
	var ord []string
	rec := &fakeRec{order: &ord}
	pr := &fakePrompt{hyprPreErr: errors.New("popup driver hypr-terminal requires hyprctl")}
	e := &Engine{
		Project: &scene.Project{
			Dir:     t.TempDir(),
			Term:    "ghostty",
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
			Transitions: map[string]scene.Transition{
				"chapter": {Live: scene.LiveTransition{Prop: "live.sh"}},
			},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    rec,
		Prompt: pr,
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{
		{Action: "transition", Value: "chapter"},
	}}
	err := e.Run(s, Options{Record: true, OutPath: "/tmp/clip.mp4", Speed: 0.0001})
	if err == nil {
		t.Fatal("Run should fail fast when a live transition step's hyprctl preflight fails")
	}
	if pr.hyprPreflights != 1 {
		t.Errorf("hyprctl-only preflight should run once for a live transition step, got %d", pr.hyprPreflights)
	}
	if len(pr.preflights) != 0 {
		t.Errorf("a transition-only scene must not run the full (terminal) preflight, got %v", pr.preflights)
	}
	if len(ord) != 0 {
		t.Errorf("no recording or staging should occur before preflight failure, got %v", ord)
	}
}

// A dialog scene opens the prompter terminal, so a missing terminal (surfaced via
// the full Preflight) must fail the run.
func TestDialogPreflightRequiresTerminal(t *testing.T) {
	var ord []string
	rec := &fakeRec{order: &ord}
	pr := &fakePrompt{preErr: errors.New(`popup driver hypr-terminal requires terminal "ghostty"`)}
	e := &Engine{
		Project: &scene.Project{
			Dir:     t.TempDir(),
			Term:    "ghostty",
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    rec,
		Prompt: pr,
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "dialog", Value: "hi"}}}
	if err := e.Run(s, Options{Record: true, OutPath: "/tmp/clip.mp4", Speed: 0.0001}); err == nil {
		t.Fatal("dialog scene missing the terminal should fail")
	}
	if len(pr.preflights) != 1 || pr.preflights[0] != "ghostty" {
		t.Errorf("dialog scene should run the full preflight once, got %v", pr.preflights)
	}
	if pr.hyprPreflights != 0 {
		t.Errorf("dialog scene should not run the hyprctl-only preflight, got %d", pr.hyprPreflights)
	}
}

// A transition-only scene missing the terminal (but with hyprctl present) must NOT
// fail on the terminal: it uses the compositor overlay, never the terminal.
func TestTransitionOnlySceneTolerantOfMissingTerminal(t *testing.T) {
	var ord []string
	rec := &fakeRec{order: &ord}
	// preErr is set (terminal "missing") to prove the full Preflight is never used;
	// hyprPreErr is nil so the hyprctl-only check passes.
	pr := &fakePrompt{preErr: errors.New("terminal missing — must not be consulted")}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "live.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := &Engine{
		Project: &scene.Project{
			Dir:     dir,
			Term:    "ghostty",
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
			Transitions: map[string]scene.Transition{
				"chapter": {Live: scene.LiveTransition{Prop: "live.sh"}},
			},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    rec,
		Prompt: pr,
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "transition", Value: "chapter"}}}
	if err := e.Run(s, Options{Record: true, OutPath: filepath.Join(dir, "clip.mp4"), Speed: 0.0001}); err != nil {
		t.Fatalf("transition-only scene must not fail on a missing terminal: %v", err)
	}
	if pr.hyprPreflights != 1 {
		t.Errorf("transition-only scene should run the hyprctl-only preflight once, got %d", pr.hyprPreflights)
	}
	if len(pr.preflights) != 0 {
		t.Errorf("transition-only scene must not run the full (terminal) preflight, got %v", pr.preflights)
	}
}

func TestOfflineTransitionStepSkipsPreflight(t *testing.T) {
	// An offline-only transition records no Hypr overlay, so no preflight runs.
	// (Validation would reject it as a step, but Run must not preflight regardless.)
	var ord []string
	rec := &fakeRec{order: &ord}
	pr := &fakePrompt{preErr: errors.New("should not be called")}
	e := &Engine{
		Project: &scene.Project{
			Dir:     t.TempDir(),
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
			Transitions: map[string]scene.Transition{
				"fade": {Cmd: "render --out {{out}}"},
			},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    rec,
		Prompt: pr,
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}
	if err := e.Run(s, Options{Record: true, OutPath: "/tmp/clip.mp4", Speed: 0.0001}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(pr.preflights) != 0 {
		t.Errorf("offline transition (no overlay) should not preflight, got %v", pr.preflights)
	}
}

func TestRehearsalSkipsPreflight(t *testing.T) {
	// A rehearse/dry-run (Record==false) produces no video, so a dialog scene must
	// NOT preflight hyprctl/terminal — it must run fine on a non-Hypr host.
	var ord []string
	rec := &fakeRec{order: &ord}
	pr := &fakePrompt{preErr: errors.New("popup driver hypr-terminal requires hyprctl")}
	e := &Engine{
		Project: &scene.Project{
			Dir:     t.TempDir(),
			Term:    "ghostty",
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    rec,
		Prompt: pr,
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "dialog", Value: "hello"}}}
	if err := e.Run(s, Options{Record: false, Speed: 0.0001}); err != nil {
		t.Fatalf("rehearsal with a dialog step should not preflight: %v", err)
	}
	if len(pr.preflights) != 0 {
		t.Errorf("rehearsal (Record=false) must not preflight, got %v", pr.preflights)
	}
	if len(pr.shown) != 0 {
		t.Errorf("rehearsal (Record=false) must not open the Hypr popup, got %v", pr.shown)
	}
}

func TestNoDialogSkipsPreflight(t *testing.T) {
	var ord []string
	rec := &fakeRec{order: &ord}
	pr := &fakePrompt{preErr: errors.New("should not be called")}
	e := &Engine{
		Project: &scene.Project{
			Dir:     t.TempDir(),
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    rec,
		Prompt: pr,
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}
	if err := e.Run(s, Options{Record: true, OutPath: "/tmp/clip.mp4", Speed: 0.0001}); err != nil {
		t.Fatalf("Run with no dialog step should not preflight: %v", err)
	}
	if len(pr.preflights) != 0 {
		t.Errorf("preflight should not run without a dialog step, got %v", pr.preflights)
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

func waitForTestFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", path)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
