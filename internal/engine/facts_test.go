package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/guest"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

type fakeVMStager struct {
	fakeStager
	g       *guest.Guest
	omarchy string
}

func (f *fakeVMStager) RecordingGuest() (*guest.Guest, string) {
	return f.g, f.omarchy
}

func readFacts(t *testing.T, clip string) facts.Facts {
	t.Helper()
	body, err := os.ReadFile(facts.Path(clip))
	if err != nil {
		t.Fatalf("facts: %v", err)
	}
	var got facts.Facts
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("facts json: %v\n%s", err, body)
	}
	return got
}

func TestHostTakeWritesFacts(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "demo.mp4")
	var ord []string
	e := &Engine{
		Project: &scene.Project{
			Dir:     dir,
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    &fakeRec{order: &ord},
		Prompt: &fakePrompt{},
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}
	if err := e.Run(s, Options{Record: true, OutPath: clip, Speed: 0.0001, Version: "1.2.3-test"}); err != nil {
		t.Fatal(err)
	}
	got := readFacts(t, clip)
	if got.Backstage != "1.2.3-test" || got.Result != facts.ResultOK {
		t.Fatalf("host facts: %+v", got)
	}
	if got.Domain != "" || got.Omarchy != "" || got.StartImage != "" || got.StartState != nil {
		t.Fatalf("host facts should omit guest fields: %+v", got)
	}
	if got.Made == "" {
		t.Fatal("host facts missing made")
	}
}

func TestVMTakeWritesGuestFieldsAndNewFacts(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "guest.mp4")
	var ord []string
	g := guest.New("omahouse-kid", "kid", "parent", "", "")
	g.Address = "192.168.122.230"
	g.StageName = "demo"
	g.Origin = "img-origin"
	g.StartMode = "clean"
	g.Snapshot = "initial"
	g.ISOVersion = "3.0.0"
	g.ISOChecksum = "abc"
	g.Recipe = "iso"
	e := &Engine{
		Project: &scene.Project{
			Dir:     dir,
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		},
		Stager: &fakeVMStager{
			fakeStager: fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
			g:          g,
			omarchy:    "4.0.2-1",
		},
		Rec:    &fakeRec{order: &ord},
		Prompt: &fakePrompt{},
		Speed:  0.0001,
	}
	s := &scene.Scene{
		Name:    "guest",
		Layout:  "solo",
		VMStart: &scene.VMStart{Mode: "clean", Snapshot: "initial"},
		Steps:   []scene.Step{{Action: "wait"}},
	}
	if err := e.Run(s, Options{Record: true, OutPath: clip, Speed: 0.0001, Version: "9.9.9"}); err != nil {
		t.Fatal(err)
	}
	got := readFacts(t, clip)
	if got.Domain != "omahouse-kid" || got.User != "kid" || got.Address != "192.168.122.230" ||
		got.Omarchy != "4.0.2-1" || got.Stage != "demo" || got.Origin != "img-origin" ||
		got.StartMode != "clean" || got.Snapshot != "initial" || got.ISOVersion != "3.0.0" ||
		got.ISOChecksum != "abc" || got.Recipe != "iso" {
		t.Fatalf("vm fields: %+v", got)
	}
	if got.Backstage != "9.9.9" || got.Result != facts.ResultOK {
		t.Fatalf("new fields: %+v", got)
	}
	if got.StartState == nil || got.StartState.Snapshot != "initial" {
		t.Fatalf("start-state: %+v", got.StartState)
	}
}

func TestWriteClipFactsCleanStartImage(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "clean.mp4")
	g := guest.New("omahouse-kid", "kid", "", "", "")
	e := &Engine{
		Stager:        &fakeVMStager{g: g, omarchy: "4.0.2-1"},
		startImage:    "img-restored",
		startSnapshot: "theme-installed",
	}
	s := &scene.Scene{VMStart: &scene.VMStart{Mode: "clean", Snapshot: "theme-installed"}}
	if err := e.writeClipFacts(clip, s, "1.0.0", facts.ResultOK); err != nil {
		t.Fatal(err)
	}
	got := readFacts(t, clip)
	if got.StartImage != "img-restored" {
		t.Fatalf("start-image = %q", got.StartImage)
	}
	if got.StartState == nil || got.StartState.Snapshot != "theme-installed" {
		t.Fatalf("start-state: %+v", got.StartState)
	}
}

func TestFactsResultStepsFailed(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "fail.mp4")
	var ord []string
	e := &Engine{
		Project: &scene.Project{
			Dir:     dir,
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    &fakeRec{order: &ord},
		Prompt: &fakePrompt{},
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "fail", Layout: "solo", Steps: []scene.Step{{Action: "unknown"}}}
	err := e.Run(s, Options{Record: true, OutPath: clip, Speed: 0.0001, Version: "v"})
	if err == nil || !strings.Contains(err.Error(), "step") {
		t.Fatalf("step error: %v", err)
	}
	got := readFacts(t, clip)
	if got.Result != facts.ResultStepsFailed {
		t.Fatalf("result = %q", got.Result)
	}
}

func TestFactsResultShort(t *testing.T) {
	stubClipLength(t, 0.01, nil)
	wasSlack := takeSlack
	takeSlack = 0.1
	t.Cleanup(func() { takeSlack = wasSlack })

	dir := t.TempDir()
	clip := filepath.Join(dir, "short.mp4")
	var ord []string
	e := &Engine{
		Project: &scene.Project{
			Dir:     dir,
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    &fakeRec{order: &ord},
		Prompt: &fakePrompt{},
		Speed:  0.1,
	}
	s := &scene.Scene{Name: "short", Layout: "solo", Steps: []scene.Step{{Action: "wait", DelayAfter: 2}}}
	err := e.Run(s, Options{Record: true, OutPath: clip, Speed: 0.1, Version: "v"})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("short error: %v", err)
	}
	got := readFacts(t, clip)
	if got.Result != facts.ResultShort {
		t.Fatalf("result = %q", got.Result)
	}
}

func TestFactsResultPrefersStepsFailedWhenAlsoShort(t *testing.T) {
	stubClipLength(t, 0.01, nil)
	wasSlack := takeSlack
	takeSlack = 0.1
	t.Cleanup(func() { takeSlack = wasSlack })

	dir := t.TempDir()
	clip := filepath.Join(dir, "both.mp4")
	var ord []string
	e := &Engine{
		Project: &scene.Project{
			Dir:     dir,
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    &fakeRec{order: &ord},
		Prompt: &fakePrompt{},
		Speed:  0.1,
	}
	s := &scene.Scene{Name: "both", Layout: "solo", Steps: []scene.Step{
		{Action: "unknown"},
		{Action: "wait", DelayAfter: 2},
	}}
	err := e.Run(s, Options{Record: true, OutPath: clip, Speed: 0.1, Version: "v"})
	if err == nil {
		t.Fatal("expected joined errors")
	}
	if !strings.Contains(err.Error(), "step") || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("joined error: %v", err)
	}
	got := readFacts(t, clip)
	if got.Result != facts.ResultStepsFailed {
		t.Fatalf("result = %q", got.Result)
	}
}

func TestRehearsalWritesNoFacts(t *testing.T) {
	dir := t.TempDir()
	var ord []string
	e := &Engine{
		Project: &scene.Project{
			Dir:     dir,
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    &fakeRec{order: &ord},
		Prompt: &fakePrompt{},
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}
	if err := e.Run(s, Options{Record: false, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.facts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("rehearsal wrote facts: %v", matches)
	}
}

func TestRunFactsWriteLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "demo.mp4")
	var ord []string
	e := &Engine{
		Project: &scene.Project{
			Dir:     dir,
			Record:  scene.RecordCfg{Out: "recordings"},
			Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		},
		Stager: &fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		Rec:    &fakeRec{order: &ord},
		Prompt: &fakePrompt{},
		Speed:  0.0001,
	}
	s := &scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}
	if err := e.Run(s, Options{Record: true, OutPath: clip, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("temporary file left: %s", e.Name())
		}
	}
}
