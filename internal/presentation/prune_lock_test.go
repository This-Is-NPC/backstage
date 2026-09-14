package presentation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/take"
)

func TestLoadAndPruneDoNotDeadlock(t *testing.T) {
	stubMedia(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scenes"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "beta"} {
		if err := os.WriteFile(filepath.Join(root, "scenes", name+".json"), []byte(`{"layout":"solo","steps":[{"action":"wait"}]}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	doc := Document{
		Version:  1,
		Duration: 2,
		Sources:  map[string]Source{"p": {Scene: "beta"}, "q": {Scene: "alpha"}},
		Tracks:   map[string]Track{"p": {Source: "p", Start: 0}, "q": {Source: "q", Start: 0}},
		Timeline: []Event{{At: 0, Layout: "single", Slots: map[string]string{"center": "p"}}},
	}
	body, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(root, "show.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	p := &scene.Project{
		Dir:           root,
		Record:        scene.RecordCfg{Out: "recordings"},
		Layouts:       map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}, "single": {Panes: []scene.Pane{{Name: "t"}}}},
		Presentations: map[string]scene.PresentationRef{"show": {File: "show.json"}},
	}
	s, err := take.Begin(p, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Clip(), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.FactsFile(), []byte(`{"result":"ok"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	beta, err := take.ForScene(p, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(beta.OutDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beta.StableClip(), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beta.StableFacts(), []byte(`{"result":"ok"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(beta.OutDir, ".takes", "beta", "lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	planCh := make(chan error, 1)
	pruneCh := make(chan error, 1)
	go func() {
		plan, err := Load(p, "show")
		if err != nil {
			planCh <- err
			return
		}
		time.Sleep(80 * time.Millisecond)
		planCh <- plan.Close()
	}()
	go func() {
		_, err := take.Prune(context.Background(), p, take.PruneOptions{OlderThan: time.Nanosecond, Now: time.Now().Add(time.Hour)})
		pruneCh <- err
	}()
	timeout := time.After(5 * time.Second)
	seen := 0
	for seen < 2 {
		select {
		case err := <-planCh:
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			seen++
		case err := <-pruneCh:
			if err != nil {
				t.Fatalf("prune: %v", err)
			}
			seen++
		case <-timeout:
			t.Fatal("Load and prune deadlocked")
		}
	}
}
