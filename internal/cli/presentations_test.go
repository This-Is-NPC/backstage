package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestListVisualScenesAndPresentations(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "scenes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scenes", "explain.json"), []byte(`{"type":"visual","entry":"slide.html","duration":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &scene.Project{Dir: dir, Presentations: map[string]scene.PresentationRef{"show": {File: "show.json"}}}
	var b bytes.Buffer
	if err := listProject(&b, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "type=visual") || !strings.Contains(b.String(), "Presentations:") {
		t.Fatal(b.String())
	}
}
func TestRenderFailsOnMissingInputWithoutRecording(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "backstage.json"), []byte(`{"presentations":{"show":{"file":"missing.json"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := projectFlag
	projectFlag = dir
	defer func() { projectFlag = old }()
	c := renderCmd()
	c.SetArgs([]string{"show", "--check"})
	c.SetErr(&bytes.Buffer{})
	if err := c.Execute(); err == nil {
		t.Fatal("accepted missing presentation")
	}
	if _, err := os.Stat(filepath.Join(dir, "recordings")); !os.IsNotExist(err) {
		t.Fatal("unexpected recording output")
	}
}
