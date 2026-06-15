package production

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/engine"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/transition"
)

func TestPlanOrder(t *testing.T) {
	prod := scene.Production{
		Scenes: []string{"a", "b", "c"},
		Transitions: []scene.TransitionUse{
			{After: "a", Use: "fade"},
			{After: "b", Use: "wipe"},
		},
	}
	segs := plan(prod)
	got := ""
	for _, s := range segs {
		if s.kind == "scene" {
			got += "[" + s.name + "]"
		} else {
			got += "(" + s.name + ":" + s.from + ">" + s.to + ")"
		}
	}
	want := "[a](fade:a>b)[b](wipe:b>c)[c]"
	if got != want {
		t.Errorf("plan = %s, want %s", got, want)
	}
}

func TestPlanNoTrailingTransition(t *testing.T) {
	// a transition after the last scene has no "next" → dropped.
	prod := scene.Production{
		Scenes:      []string{"a", "b"},
		Transitions: []scene.TransitionUse{{After: "b", Use: "fade"}},
	}
	segs := plan(prod)
	if len(segs) != 2 || segs[0].kind != "scene" || segs[1].kind != "scene" {
		t.Errorf("trailing transition should be dropped, got %v", segs)
	}
}

func TestAdHoc(t *testing.T) {
	p := AdHoc([]string{"a", "b", "c"}, "slide")
	if len(p.Transitions) != 2 {
		t.Fatalf("expected 2 transitions, got %d", len(p.Transitions))
	}
	if p.Transitions[0].After != "a" || p.Transitions[0].Use != "slide" {
		t.Errorf("first transition wrong: %+v", p.Transitions[0])
	}

	none := AdHoc([]string{"a", "b"}, "")
	if len(none.Transitions) != 0 {
		t.Errorf("no transition name → no transitions, got %d", len(none.Transitions))
	}

	single := AdHoc([]string{"a"}, "slide")
	if len(single.Transitions) != 0 {
		t.Errorf("single scene → no transitions, got %d", len(single.Transitions))
	}
}

func TestTransitionRenderModeSelection(t *testing.T) {
	offline := scene.Transition{Cmd: "render --out {{out}}"}
	live := scene.Transition{Live: scene.LiveTransition{Prop: "live.sh"}}
	both := scene.Transition{Cmd: "render --out {{out}}", Live: scene.LiveTransition{Prop: "live.sh"}}
	if offline.RenderMode() == scene.RenderLive {
		t.Error("offline-only transition should not record live")
	}
	if live.RenderMode() != scene.RenderLive || both.RenderMode() != scene.RenderLive {
		t.Error("live transition should record live")
	}
}

func TestPlanIntroTransition(t *testing.T) {
	prod := scene.Production{
		Scenes:      []string{"a", "b"},
		Transitions: []scene.TransitionUse{{After: "", Use: "intro"}, {After: "a", Use: "fade"}},
	}
	segs := plan(prod)
	got := ""
	for _, s := range segs {
		if s.kind == "scene" {
			got += "[" + s.name + "]"
		} else {
			got += "(" + s.name + ":" + s.from + ">" + s.to + ")"
		}
	}
	want := "(intro:>a)[a](fade:a>b)[b]"
	if got != want {
		t.Errorf("plan with intro = %s, want %s", got, want)
	}
}

func TestConcatEscape(t *testing.T) {
	got := concatEscape("/tmp/it'll-work.mp4")
	want := `/tmp/it'\''ll-work.mp4`
	if got != want {
		t.Errorf("concatEscape = %q, want %q", got, want)
	}
}

func TestRecordLiveTransitionStopsGuardBeforeRelease(t *testing.T) {
	stubDir := t.TempDir()
	installStub(t, stubDir, "hyprctl", "#!/bin/sh\nexit 0\n")
	installStub(t, stubDir, "gpu-screen-recorder", `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    out="$2"
    shift 2
  else
    shift
  fi
done
printf warm > "$out"
trap 'printf final > "$out"; exit 0' INT
while true; do sleep 1; done
`)
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	projectDir := t.TempDir()
	installStub(t, projectDir, "live.sh", "#!/bin/sh\nexit 0\n")
	clip := filepath.Join(t.TempDir(), "clip.mp4")
	p := &scene.Project{Dir: projectDir, Record: scene.RecordCfg{Monitor: "eDP-1", FPS: 30}}

	var events []string
	oldGuard := newInterruptGuard
	newInterruptGuard = func(stop func() error, _ func()) interruptGuard {
		return &fakeProductionGuard{events: &events, stop: stop}
	}
	defer func() { newInterruptGuard = oldGuard }()

	err := recordLiveTransition(p, scene.Transition{Live: scene.LiveTransition{Prop: "live.sh"}}, transition.Vars{}, clip, nil)
	if err != nil {
		t.Fatalf("recordLiveTransition: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("guard events = %v, want stop before release", events)
	}
	if events[0] != "stop" || events[1] != "release" {
		t.Fatalf("guard events = %v, want stop before release", events)
	}
}

func TestRecordLiveTransitionSkipsPropStartAfterInterruptDuringRecorderWarmup(t *testing.T) {
	stubDir := t.TempDir()
	ready := filepath.Join(stubDir, "recorder-ready")
	installStub(t, stubDir, "hyprctl", "#!/bin/sh\nexit 0\n")
	installStub(t, stubDir, "gpu-screen-recorder", `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    out="$2"
    shift 2
  else
    shift
  fi
done
printf ready > "$REC_READY"
trap 'printf final > "$out"; exit 0' INT TERM
while true; do sleep 1; done
`)
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("REC_READY", ready)

	projectDir := t.TempDir()
	propStarted := filepath.Join(projectDir, "prop-started")
	t.Setenv("PROP_STARTED", propStarted)
	installStub(t, projectDir, "live.sh", `#!/bin/sh
printf started > "$PROP_STARTED"
exit 0
`)
	clip := filepath.Join(t.TempDir(), "clip.mp4")
	p := &scene.Project{Dir: projectDir, Record: scene.RecordCfg{Monitor: "eDP-1", FPS: 30}}

	guards := make(chan *manualProductionGuard, 1)
	oldGuard := newInterruptGuard
	newInterruptGuard = func(stop func() error, onInterrupt func()) interruptGuard {
		g := &manualProductionGuard{stop: stop, onInterrupt: onInterrupt}
		guards <- g
		return g
	}
	defer func() { newInterruptGuard = oldGuard }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- recordLiveTransition(p, scene.Transition{Live: scene.LiveTransition{Prop: "live.sh"}}, transition.Vars{}, clip, nil)
	}()

	var guard *manualProductionGuard
	select {
	case guard = <-guards:
	case <-time.After(2 * time.Second):
		t.Fatal("interrupt guard was not installed")
	}
	waitForFile(t, ready)
	if err := guard.Stop(); err != nil {
		t.Fatalf("interrupt stop: %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, engine.ErrCommandInterrupted) {
			t.Fatalf("recordLiveTransition error = %v, want %v", err, engine.ErrCommandInterrupted)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("recordLiveTransition did not return after interrupt canceled the prop start")
	}
	if _, err := os.Stat(propStarted); !os.IsNotExist(err) {
		t.Fatalf("prop started after interrupt cleanup; stat err=%v", err)
	}
}

func TestRunProductionCommandInterruptKillsProcessGroupAndRunsCleanup(t *testing.T) {
	dir := t.TempDir()
	ticks := filepath.Join(dir, "ticks")
	ready := filepath.Join(dir, "ready")
	cleaned := filepath.Join(dir, "cleaned")
	cmd := exec.Command("sh", "-c", `
(while :; do printf x >> "$1"; sleep 0.05; done) &
printf ready > "$2"
wait
`, "sh", ticks, ready)
	defer scene.KillProcessGroup(cmd)

	interrupts := make(chan func(), 1)
	oldGuard := newInterruptGuard
	newInterruptGuard = func(stop func() error, onInterrupt func()) interruptGuard {
		interrupts <- func() {
			if stop != nil {
				_ = stop()
			}
			if onInterrupt != nil {
				onInterrupt()
			}
		}
		return &noopProductionGuard{}
	}
	defer func() { newInterruptGuard = oldGuard }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runProductionCommand(cmd, func() {
			_ = os.WriteFile(cleaned, []byte("yes"), 0o644)
		})
	}()

	var interrupt func()
	select {
	case interrupt = <-interrupts:
	case <-time.After(2 * time.Second):
		t.Fatal("interrupt guard was not installed")
	}
	waitForFile(t, ready)
	interrupt()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("killed command should report an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("command did not exit after interrupt cleanup")
	}
	if b, err := os.ReadFile(cleaned); err != nil || string(b) != "yes" {
		t.Fatalf("cleanup output = %q, %v", b, err)
	}
	before, _ := os.ReadFile(ticks)
	time.Sleep(200 * time.Millisecond)
	after, _ := os.ReadFile(ticks)
	if len(after) != len(before) {
		t.Fatalf("child process kept running after process-group kill: before=%d after=%d", len(before), len(after))
	}
}

func TestRunProductionCommandSkipsStartAfterInterrupt(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	cleaned := filepath.Join(dir, "cleaned")
	cmd := exec.Command("sh", "-c", `printf started > "$1"`, "sh", started)

	oldGuard := newInterruptGuard
	newInterruptGuard = func(stop func() error, onInterrupt func()) interruptGuard {
		if stop != nil {
			_ = stop()
		}
		if onInterrupt != nil {
			onInterrupt()
		}
		return &noopProductionGuard{}
	}
	defer func() { newInterruptGuard = oldGuard }()

	err := runProductionCommand(cmd, func() {
		_ = os.WriteFile(cleaned, []byte("yes"), 0o644)
	})
	if !errors.Is(err, engine.ErrCommandInterrupted) {
		t.Fatalf("runProductionCommand error = %v, want %v", err, engine.ErrCommandInterrupted)
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Fatalf("command started after interrupt cleanup; stat err=%v", err)
	}
	if b, err := os.ReadFile(cleaned); err != nil || string(b) != "yes" {
		t.Fatalf("cleanup output = %q, %v", b, err)
	}
}

func TestConcatFinalInterruptPreservesExistingOutput(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "production.mp4")
	if err := os.WriteFile(out, []byte("previous"), 0o644); err != nil {
		t.Fatal(err)
	}
	clip := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(clip, []byte("clip"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldGuard := newInterruptGuard
	newInterruptGuard = func(stop func() error, onInterrupt func()) interruptGuard {
		if stop != nil {
			_ = stop()
		}
		if onInterrupt != nil {
			onInterrupt()
		}
		return &noopProductionGuard{}
	}
	defer func() { newInterruptGuard = oldGuard }()

	err := concatFinal([]string{clip}, out, dir, nil)
	if !errors.Is(err, engine.ErrCommandInterrupted) {
		t.Fatalf("concatFinal error = %v, want %v", err, engine.ErrCommandInterrupted)
	}
	if b, err := os.ReadFile(out); err != nil || string(b) != "previous" {
		t.Fatalf("existing output = %q, %v; want previous output preserved", b, err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".production-*.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary concat outputs left behind: %v", matches)
	}
}

type noopProductionGuard struct{}

func (noopProductionGuard) Stop() error { return nil }
func (noopProductionGuard) Release()    {}

type manualProductionGuard struct {
	stop        func() error
	onInterrupt func()
}

func (m *manualProductionGuard) Stop() error {
	if m.stop == nil {
		return nil
	}
	return m.stop()
}

func (m *manualProductionGuard) Release() {}

func (m *manualProductionGuard) Interrupt() {
	_ = m.Stop()
	if m.onInterrupt != nil {
		m.onInterrupt()
	}
}

type fakeProductionGuard struct {
	events *[]string
	stop   func() error
}

func (f *fakeProductionGuard) Stop() error {
	*f.events = append(*f.events, "stop")
	return f.stop()
}

func (f *fakeProductionGuard) Release() {
	*f.events = append(*f.events, "release")
}

func installStub(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func waitForFile(t *testing.T, path string) {
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
