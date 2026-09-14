package scene

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigShowIncludesOrigins(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"term": "ghostty"
	}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{
		"extends": "../backstage.json",
		"record": {"fps": 24}
	}`)
	p, err := LoadProject(filepath.Join(leaf, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := WriteConfigShow(&b, p); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"workspace: " + ws,
		"project: " + leaf,
		"extends: ../backstage.json",
		"term = \"ghostty\"  [backstage.json]",
		"record.fps = 24  [leaf/backstage.json]",
		"record.monitor = \"eDP-1\"  [default]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("show missing %q\n%s", want, out)
		}
	}
}
