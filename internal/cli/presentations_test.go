package cli

import (
	"bytes"
	"fmt"
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

func TestRenderRefusesScaleFlag(t *testing.T) {
	c := renderCmd()
	c.SetArgs([]string{"show", "--scale", "0.5"})
	var errBuf bytes.Buffer
	c.SetErr(&errBuf)
	err := c.Execute()
	if err == nil {
		t.Fatal("render accepted --scale")
	}
	if !strings.Contains(err.Error(), "unknown flag") && !strings.Contains(errBuf.String(), "unknown flag") {
		t.Fatalf("%v %s", err, errBuf.String())
	}
}

func writePreviewProject(t *testing.T, duration float64, fps int) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "scenes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "backstage.json"), []byte(fmt.Sprintf(`{"presentations":{"show":{"file":"show.json"}},"render":{"w":160,"h":90,"fps":%d}}`, fps)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "show.json"), []byte(fmt.Sprintf(`{"version":1,"duration":%g,"timeline":[{"at":0,"scene":"explain"}]}`, duration)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "visual.html"), []byte(`<script>window.backstageTemplate={version:1};window.render=async()=>{};</script>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scenes", "explain.json"), []byte(`{"type":"visual","entry":"visual.html","duration":10}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runPreview(t *testing.T, dir string, args ...string) error {
	t.Helper()
	old := projectFlag
	projectFlag = dir
	t.Cleanup(func() { projectFlag = old })
	c := previewCmd()
	c.SetArgs(args)
	c.SetErr(&bytes.Buffer{})
	c.SetOut(&bytes.Buffer{})
	return c.Execute()
}

func TestPreviewRefusesFlags(t *testing.T) {
	dir := writePreviewProject(t, 8, 12)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"from-negative", []string{"show", "--from", "-1"}, "from must be"},
		{"from-nan", []string{"show", "--from", "NaN"}, "from must be"},
		{"to-inf", []string{"show", "--to", "Inf"}, "to must be"},
		{"to-over", []string{"show", "--to", "9"}, "to must be"},
		{"to-zero", []string{"show", "--from", "0", "--to", "0"}, "from must be less than to"},
		{"empty-interval", []string{"show", "--from", "1.01", "--to", "1.05"}, "interval has no frames"},
		{"scale-zero", []string{"show", "--scale", "0"}, "scale must be"},
		{"scale-over", []string{"show", "--scale", "1.1"}, "scale must be"},
		{"scale-nan", []string{"show", "--scale", "NaN"}, "scale must be"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := runPreview(t, dir, c.args...)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%v want %q", err, c.want)
			}
		})
	}
	c := previewCmd()
	c.SetArgs([]string{"show", "--from", "abc"})
	c.SetErr(&bytes.Buffer{})
	if err := c.Execute(); err == nil || !strings.Contains(err.Error(), "invalid argument") {
		t.Fatalf("non-numeric: %v", err)
	}
	c = previewCmd()
	if err := c.ParseFlags([]string{"--from", "1"}); err != nil {
		t.Fatal(err)
	}
	if c.Flags().Changed("to") {
		t.Fatal("to marked changed by default")
	}
	c = previewCmd()
	if err := c.ParseFlags([]string{"--to", "0"}); err != nil {
		t.Fatal(err)
	}
	if !c.Flags().Changed("to") {
		t.Fatal("explicit --to 0 must be Changed")
	}
}
