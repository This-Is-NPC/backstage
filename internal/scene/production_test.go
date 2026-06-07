package scene

import (
	"os"
	"path/filepath"
	"testing"
)

func projectWithScenes(t *testing.T, names ...string) *Project {
	t.Helper()
	dir := t.TempDir()
	sc := filepath.Join(dir, "scenes")
	if err := os.MkdirAll(sc, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(sc, n+".json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &Project{Dir: dir}
}

func TestRenderFPSDefault(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "backstage.json")
	os.WriteFile(cfg, []byte(`{"record":{"fps":60},"layouts":{}}`), 0o644)
	p, err := LoadProject(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p.Render.FPS != 60 {
		t.Errorf("render.fps should default to record.fps (60), got %d", p.Render.FPS)
	}
}

func TestValidateProduction(t *testing.T) {
	p := projectWithScenes(t, "a", "b")
	p.Transitions = map[string]Transition{
		"slide": {Cmd: "render --out {{out}} --w {{w}}"},
		"bad":   {Cmd: "render --no-output-placeholder"},
	}

	ok := Production{Scenes: []string{"a", "b"}, Transitions: []TransitionUse{{After: "a", Use: "slide"}}}
	if err := p.ValidateProduction(ok); err != nil {
		t.Errorf("valid production rejected: %v", err)
	}

	missingScene := Production{Scenes: []string{"a", "missing"}}
	if err := p.ValidateProduction(missingScene); err == nil {
		t.Error("expected missing-scene error")
	}

	unknownUse := Production{Scenes: []string{"a", "b"}, Transitions: []TransitionUse{{After: "a", Use: "nope"}}}
	if err := p.ValidateProduction(unknownUse); err == nil {
		t.Error("expected unknown-transition error")
	}

	noOut := Production{Scenes: []string{"a", "b"}, Transitions: []TransitionUse{{After: "a", Use: "bad"}}}
	if err := p.ValidateProduction(noOut); err == nil {
		t.Error("expected missing-{{out}} error")
	}

	afterNotInSeq := Production{Scenes: []string{"a", "b"}, Transitions: []TransitionUse{{After: "z", Use: "slide"}}}
	if err := p.ValidateProduction(afterNotInSeq); err == nil {
		t.Error("expected after-not-in-sequence error")
	}

	duplicateAfter := Production{Scenes: []string{"a", "b"}, Transitions: []TransitionUse{{After: "a", Use: "slide"}, {After: "a", Use: "slide"}}}
	if err := p.ValidateProduction(duplicateAfter); err == nil {
		t.Error("expected duplicate-transition error")
	}

	duplicateIntro := Production{Scenes: []string{"a", "b"}, Transitions: []TransitionUse{{After: "", Use: "slide"}, {After: "", Use: "slide"}}}
	if err := p.ValidateProduction(duplicateIntro); err == nil {
		t.Error("expected duplicate-intro error")
	}

	empty := Production{}
	if err := p.ValidateProduction(empty); err == nil {
		t.Error("expected no-scenes error")
	}
}

func TestProductionLookup(t *testing.T) {
	p := &Project{Productions: map[string]Production{"tour": {Scenes: []string{"a"}}}}
	if _, err := p.Production("tour"); err != nil {
		t.Errorf("lookup tour: %v", err)
	}
	if _, err := p.Production("ghost"); err == nil {
		t.Error("expected unknown-production error")
	}
}

func TestValidateIntroTransition(t *testing.T) {
	p := projectWithScenes(t, "a", "b")
	p.Transitions = map[string]Transition{"intro": {Cmd: "r --out {{out}}"}}
	prod := Production{Scenes: []string{"a", "b"}, Transitions: []TransitionUse{{After: "", Use: "intro"}}}
	if err := p.ValidateProduction(prod); err != nil {
		t.Errorf("intro transition (after empty) should be valid: %v", err)
	}
}
