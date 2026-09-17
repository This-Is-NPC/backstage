package presentation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/take"
)

func stubMedia(t *testing.T) {
	t.Helper()
	was := inspectMedia
	inspectMedia = func(path string) (Media, error) {
		return Media{Path: path, Duration: 4, HasVideo: true}, nil
	}
	t.Cleanup(func() { inspectMedia = was })
}

func recordingProject(t *testing.T) *scene.Project {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scenes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scenes", "demo.json"), []byte(`{"layout":"solo","steps":[{"action":"wait"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := Document{
		Version:  1,
		Duration: 2,
		Sources:  map[string]Source{"clip": {Scene: "demo"}},
		Tracks:   map[string]Track{"clip": {Source: "clip", Start: 0}},
		Timeline: []Event{{At: 0, Layout: "single", Slots: map[string]string{"center": "clip"}}},
	}
	body, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(root, "show.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	return &scene.Project{
		Dir:           root,
		Record:        scene.RecordCfg{Out: "recordings"},
		Layouts:       map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}, "single": {Panes: []scene.Pane{{Name: "t"}}}},
		Presentations: map[string]scene.PresentationRef{"show": {File: "show.json"}},
	}
}

func publishDemo(t *testing.T, p *scene.Project, body string) take.Published {
	t.Helper()
	s, err := take.Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Clip(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.FactsFile(), []byte(`{"result":"ok"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pub, err := s.Publish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestSceneSourceUsesGenerationAndHoldsLease(t *testing.T) {
	stubMedia(t)
	p := recordingProject(t)
	pub := publishDemo(t, p, "gen-one")
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	path := plan.Tracks["clip"].Media.Path
	if path != pub.Clip {
		t.Fatalf("scene source %s, want generation %s", path, pub.Clip)
	}
	if strings.HasSuffix(path, "demo.mp4") && !strings.Contains(path, ".takes") {
		t.Fatal("scene source resolved the projection")
	}
	lease := filepath.Join(filepath.Dir(path), "lease")
	f, err := os.Open(lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		_ = f.Close()
		t.Fatal("generation lease was not held during the plan")
	}
	_ = f.Close()
	// A later probe, like FFmpeg reopening the file, still sees the same clip.
	media, err := inspectMedia(path)
	if err != nil || media.Path != path {
		t.Fatalf("reopen: %v %+v", err, media)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	f, err = os.Open(lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		t.Fatalf("lease still held after Close: %v", err)
	}
	_ = f.Close()
}

func TestFileSourceFollowsProjection(t *testing.T) {
	stubMedia(t)
	p := recordingProject(t)
	publishDemo(t, p, "first")
	publishDemo(t, p, "second")
	doc := Document{
		Version:  1,
		Duration: 2,
		Sources:  map[string]Source{"clip": {File: "recordings/demo.mp4"}},
		Tracks:   map[string]Track{"clip": {Source: "clip", Start: 0}},
		Timeline: []Event{{At: 0, Layout: "single", Slots: map[string]string{"center": "clip"}}},
	}
	body, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(p.Dir, "show.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	path := plan.Tracks["clip"].Media.Path
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "second" {
		t.Fatalf("file source %s: %s %v", path, got, err)
	}
	if strings.Contains(path, ".takes") {
		t.Fatal("file source should read the projection")
	}
}

func TestOutputRefusesPublishedTakePaths(t *testing.T) {
	stubMedia(t)
	p := recordingProject(t)
	publishDemo(t, p, "gen")
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	for _, out := range []string{
		"recordings/demo.mp4",
		"recordings/demo.facts.json",
		"recordings/demo.take.json",
		"recordings/.takes/demo/x.mp4",
	} {
		if _, err := plan.Output(out); err == nil {
			t.Fatalf("allowed overwrite of %s", out)
		}
	}
	if _, err := plan.Output("exports/show.mp4"); err != nil {
		t.Fatal(err)
	}
}
