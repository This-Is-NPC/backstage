package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

type tracedPane struct {
	fakePane
	started time.Time
	stops   int
}

func (p *tracedPane) TracePointer(_ context.Context, started time.Time) func() []facts.PointerSample {
	p.started = started
	return func() []facts.PointerSample {
		p.stops++
		return []facts.PointerSample{{At: 0, Pointer: &facts.Pointer{X: .3, Y: .4, Click: true}}}
	}
}

func TestRecordingFinalizesPointerEvidenceBeforePublishingFacts(t *testing.T) {
	for _, recording := range []bool{false, true} {
		for _, observations := range []bool{false, true} {
			for _, staging := range []bool{false, true} {
				p := &scene.Project{Dir: t.TempDir(), Record: scene.RecordCfg{Observations: observations}, Layouts: map[string]scene.Layout{"screen": {}}}
				driver := &tracedPane{}
				e := &Engine{Project: p, PaneDriver: driver, Rec: &fakeRec{}, Prompt: &fakePrompt{}}
				clip := filepath.Join(p.Dir, "clip.mp4")
				if err := e.Run(&scene.Scene{Name: "demo", Layout: "screen", Steps: []scene.Step{{Action: "wait"}}}, Options{Record: recording, ShowStaging: staging, Speed: .0001, OutPath: clip}); err != nil {
					t.Fatal(err)
				}
				if !recording || !observations {
					if driver.stops != 0 || !driver.started.IsZero() {
						t.Fatal("captured an unrequested trace")
					}
					continue
				}
				f, err := facts.Read(facts.Path(clip))
				if err != nil {
					t.Fatal(err)
				}
				if driver.stops != 1 || driver.started != e.recordingStarted || len(f.PointerTrace) != 1 || !f.PointerTrace[0].Pointer.Click {
					t.Fatalf("stops=%d trace=%+v", driver.stops, f.PointerTrace)
				}
			}
		}
	}
}

func TestRecordedTimingUsesResolvedActionAndVideoClock(t *testing.T) {
	p := &scene.Project{Aliases: map[string]scene.Alias{"shortcut": {Action: "keys", Target: "editor"}}}
	e, _, _ := newTestEngine(p)
	e.recordingStarted = time.Now().Add(-time.Second)
	if err := e.runStep(0, scene.Step{StepID: "open", Action: "shortcut", Commands: []string{"ctrl+s"}}); err != nil {
		t.Fatal(err)
	}
	if len(e.steps) != 1 {
		t.Fatal(e.steps)
	}
	s := e.steps[0]
	if s.ID != "open" || s.Action != "keys" || s.Target != "editor" || s.Start < 1 || s.End < s.Start || len(s.Keys) != 1 || s.Keys[0] != "ctrl+s" {
		t.Fatalf("step: %+v", s)
	}
	e.recordingStarted = time.Time{}
	if err := e.runStep(1, scene.Step{Action: "wait"}); err != nil {
		t.Fatal(err)
	}
	if len(e.steps) != 1 {
		t.Fatal("recorded timing during rehearsal")
	}
}

type observedPane struct {
	fakePane
	target string
}

func (p *observedPane) Observe(target string) facts.VisualState {
	p.target = target
	return facts.VisualState{Region: &facts.Region{X: 0.25, Y: 0.25, W: 0.5, H: 0.5}, Pointer: &facts.Pointer{X: 0.5, Y: 0.5}}
}

func TestStepStoresObservedGeometryAndTypedInput(t *testing.T) {
	e, _, _ := newTestEngine(&scene.Project{Record: scene.RecordCfg{Observations: true}})
	p := &observedPane{}
	e.pane = p
	e.recordingStarted = time.Now()
	if err := e.runStep(0, scene.Step{Action: "run", Target: "editor", Value: "echo ready"}); err != nil {
		t.Fatal(err)
	}
	s := e.steps[0]
	if p.target != "editor" || s.Region == nil || s.Region.W != 0.5 || s.Pointer == nil || s.Pointer.X != 0.5 || len(s.Keys) != 2 || s.Keys[0] != "echo ready" || s.Keys[1] != "enter" {
		t.Fatalf("step=%+v", s)
	}
}
