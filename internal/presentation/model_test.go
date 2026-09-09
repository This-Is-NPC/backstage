package presentation

import (
	"encoding/json"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceSpanRepeatsAndCuts(t *testing.T) {
	tr := CompiledTrack{Track: Track{Start: 1, Segments: []Segment{{From: 0, To: 4, Rate: 2}, {From: 2, To: 4, Rate: 1}}}}
	spans := mapSpan(tr, 1, 3)
	want := [][3]float64{{1.5, 2.5, 2}, {3, 4, 1}}
	if len(spans) != len(want) {
		t.Fatal(spans)
	}
	for i := range want {
		if spans[i] != want[i] {
			t.Fatalf("%v != %v", spans, want)
		}
	}
}
func TestRationalSegmentDuration(t *testing.T) {
	var segments []Segment
	for range 300 {
		segments = append(segments, Segment{To: 0.1, Rate: 3})
	}
	if math.Abs(segmentDuration(segments)-10) > 1e-12 {
		t.Fatal(segmentDuration(segments))
	}
}
func TestTempoKeepsEachFilterInRange(t *testing.T) {
	for _, rate := range []float64{0.01, 0.5, 1, 2, 40} {
		if tempo(rate) == "" {
			t.Fatal(rate)
		}
	}
}

func writeFixture(t *testing.T, doc Document) *scene.Project {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scenes"), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(root, "show.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "visual.html"), []byte("<html></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scenes", "explain.json"), []byte(`{"type":"visual","entry":"visual.html","duration":10,"narration":{"cues":[{"id":"cue","start":0,"end":1,"text":"A rule"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return &scene.Project{Dir: root, Presentations: map[string]scene.PresentationRef{"show": {File: "show.json"}}}
}
func TestVisualProjectNeedsNoRecordingConfiguration(t *testing.T) {
	p := writeFixture(t, Document{Version: 1, Duration: 2, Timeline: []Event{{Scene: "explain"}}})
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Frames != 60 || plan.Width != 1920 {
		t.Fatal(plan.Frames, plan.Width)
	}
	if _, err = plan.Output("visual.html"); err == nil {
		t.Fatal("allowed overwriting source")
	}
}
func TestInvalidTimelineRejected(t *testing.T) {
	cases := []struct {
		name   string
		events []Event
	}{
		{"missing zero", []Event{{At: 1, Scene: "explain"}}},
		{"duplicate times", []Event{{Scene: "explain"}, {Scene: "explain"}}},
		{"transition overlap", []Event{{Scene: "explain"}, {At: 1, Scene: "explain", Transition: Transition{Effect: "fade", Duration: 2}}}},
		{"morph from visual", []Event{{Scene: "explain"}, {At: 1, Layout: "single", Transition: Transition{Effect: "morph", Duration: 0.5}}}},
		{"missing scene", []Event{{Scene: "missing"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeFixture(t, Document{Version: 1, Duration: 2, Timeline: tc.events})
			if _, err := Load(p, "show"); err == nil {
				t.Fatal("accepted invalid timeline")
			}
		})
	}
}
func TestCaptionsUseSceneTextAndExplicitClock(t *testing.T) {
	d := Document{Version: 1, Duration: 2, Timeline: []Event{{Scene: "explain"}}, Captions: []Caption{{Scene: "explain", Cue: "cue", Clock: "presentation", At: 0.5, Duration: 1, Slot: "subtitle"}}}
	plan, err := Load(writeFixture(t, d), "show")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Text) != 1 || plan.Text[0].Text != "A rule" || plan.Text[0].At != 0.5 {
		t.Fatal(plan.Text)
	}
	d.Captions[0].Duration = 2
	if _, err = Load(writeFixture(t, d), "show"); err == nil {
		t.Fatal("allowed caption past end")
	}
}
func TestStrictPresentationSchema(t *testing.T) {
	p := writeFixture(t, Document{})
	if err := os.WriteFile(filepath.Join(p.Dir, "show.json"), []byte(`{"version":1,"duraton":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p, "show"); err == nil {
		t.Fatal("ignored typo")
	}
}
func TestTemplateInitDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	if err := InitTemplate(root, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := InitTemplate(root, "demo"); err == nil {
		t.Fatal("overwrote template")
	}
	if err := InitTemplate(root, "../escape"); err == nil {
		t.Fatal("allowed escaping name")
	}
}
