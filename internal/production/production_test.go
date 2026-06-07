package production

import (
	"testing"

	"github.com/This-Is-NPC/backstage/internal/scene"
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
