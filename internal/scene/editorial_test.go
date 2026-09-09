package scene

import "testing"

func TestVisualSceneDoesNotRequireRecordingLayout(t *testing.T) {
	p := &Project{Dir: t.TempDir()}
	s := Scene{Type: "visual", Entry: "slides/explain.html", Duration: 2}
	if err := s.Validate(p); err != nil {
		t.Fatal(err)
	}
	s.VM = "guest"
	if err := s.Validate(p); err == nil {
		t.Fatal("visual scene accepted VM")
	}
}
func TestNarrationRejectsDuplicateIDs(t *testing.T) {
	p := &Project{Dir: t.TempDir()}
	s := Scene{Type: "visual", Entry: "slide.html", Duration: 2, Narration: Narration{Cues: []NarrationCue{{ID: "one", Text: "A", Start: 0, End: 1}, {ID: "one", Text: "B", Start: 1, End: 2}}}}
	if err := s.Validate(p); err == nil {
		t.Fatal("accepted duplicate cue IDs")
	}
}
