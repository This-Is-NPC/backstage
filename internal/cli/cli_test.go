package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestListProject(t *testing.T) {
	dir := t.TempDir()
	scenesDir := filepath.Join(dir, "scenes")
	if err := os.MkdirAll(scenesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(scenesDir, "01-intro.json"),
		[]byte(`{"layout":"solo","steps":[{"action":"wait"},{"action":"dialog","value":"hi"}]}`), 0o644)
	os.WriteFile(filepath.Join(scenesDir, "broken.json"), []byte(`{not json`), 0o644)

	p := &scene.Project{
		Dir: dir,
		Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		Productions: map[string]scene.Production{
			"tour": {Scenes: []string{"01-intro"}, Transitions: []scene.TransitionUse{{After: "01-intro", Use: "x"}}},
		},
	}

	var b strings.Builder
	if err := listProject(&b, p); err != nil {
		t.Fatalf("listProject: %v", err)
	}
	out := b.String()

	for _, want := range []string{
		"01-intro", "layout=solo", "steps=2", // valid scene
		"broken", "invalid:", // bad scene flagged, not fatal
		"Productions:", "tour", "scenes=1 transitions=1", // productions
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q\n---\n%s", want, out)
		}
	}
}

func TestListProjectEmpty(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "scenes"), 0o755)
	var b strings.Builder
	if err := listProject(&b, &scene.Project{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "(none)") {
		t.Errorf("empty project should print (none): %q", b.String())
	}
}

func TestSplitCSV(t *testing.T) {
	got := splitCSV(" a, b ,,c,")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("splitCSV = %v, want [a b c]", got)
	}
	if len(splitCSV("")) != 0 {
		t.Error("empty string should give no items")
	}
}
