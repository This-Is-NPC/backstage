package production

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/engine"
	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestManagedStagesReservesGroupMembers(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "scenes"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"make-laptop": `{"name":"make-laptop","layout":"solo","vm":"laptop","vm-end":{"snapshot":"linked","group":"household"},"steps":[{"action":"wait"}]}`,
		"make-server": `{"name":"make-server","layout":"solo","vm":"server","vm-end":{"snapshot":"linked","group":"household"},"steps":[{"action":"wait"}]}`,
		"use":         `{"name":"use","layout":"solo","vm":"laptop","vm-start":{"mode":"clean","snapshot":"linked","group":"household"},"steps":[{"action":"wait"}]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, "scenes", name+".json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := &scene.Project{
		Dir:     dir,
		Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		VMs: map[string]scene.VMCfg{
			"laptop": {Stage: "house-laptop"},
			"server": {Stage: "house-server"},
		},
		StateGroups: map[string][]string{"household": {"laptop", "server"}},
	}
	reserved, err := managedStages(p, scene.Production{Scenes: []scene.SceneRef{
		{Scene: "make-laptop"}, {Scene: "make-server"}, {Scene: "use"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !reserved["house-laptop"] || !reserved["house-server"] {
		t.Fatalf("reserved: %v", reserved)
	}
}

func TestManagedStagesReservesStartGroupMembers(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "scenes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scenes", "use.json"), []byte(`{"name":"use","layout":"solo","vm":"laptop","vm-start":{"mode":"clean","snapshot":"linked","group":"household"},"steps":[{"action":"wait"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &scene.Project{
		Dir:     dir,
		Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		VMs: map[string]scene.VMCfg{
			"laptop": {Stage: "house-laptop"},
			"server": {Stage: "house-server"},
		},
		StateGroups: map[string][]string{"household": {"laptop", "server"}},
	}
	reserved, err := managedStages(p, scene.Production{Scenes: []scene.SceneRef{{Scene: "use"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !reserved["house-laptop"] || !reserved["house-server"] {
		t.Fatalf("start-group members: %v", reserved)
	}
}

func TestProduceRunMintsOneGenerationForGroupScenes(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "scenes"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, vm := range map[string]string{"make-laptop": "laptop", "make-server": "server"} {
		body := `{"name":"` + name + `","layout":"solo","vm":"` + vm + `","vm-start":{"mode":"clean"},"vm-end":{"snapshot":"linked","group":"household"},"steps":[{"action":"wait"}]}`
		if err := os.WriteFile(filepath.Join(dir, "scenes", name+".json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := &scene.Project{
		Dir:     dir,
		Record:  scene.RecordCfg{Out: "recordings", FPS: 30},
		Render:  scene.RenderCfg{W: 64, H: 36, FPS: 30},
		Layouts: map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		VMs: map[string]scene.VMCfg{
			"laptop": {Stage: "house-laptop"},
			"server": {Stage: "house-server"},
		},
		StateGroups: map[string][]string{"household": {"laptop", "server"}},
	}
	var got []string
	prevEng := runEngine
	runEngine = func(_ *scene.Project, s *scene.Scene, opts engine.Options) error {
		got = append(got, opts.StateGeneration)
		if err := os.MkdirAll(filepath.Dir(opts.OutPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(opts.OutPath, []byte(s.Name), 0o644); err != nil {
			return err
		}
		return facts.Write(facts.Path(opts.OutPath), facts.Facts{Result: facts.ResultOK, Backstage: "t"})
	}
	prevTD := teardownHost
	teardownHost = func() error { return nil }
	prevCmd := runProductionCommand
	runProductionCommand = func(cmd *exec.Cmd, _ func()) error {
		if cmd == nil || len(cmd.Args) == 0 {
			return nil
		}
		return os.WriteFile(cmd.Args[len(cmd.Args)-1], []byte("clip"), 0o644)
	}
	t.Cleanup(func() {
		runEngine = prevEng
		teardownHost = prevTD
		runProductionCommand = prevCmd
	})

	_, err := Run(Options{
		Project: p, Prod: scene.Production{Scenes: []scene.SceneRef{
			{Scene: "make-laptop"}, {Scene: "make-server"},
		}},
		Speed: 1, ShowStaging: true, KeepSegments: true, Version: "t",
		OutPath: filepath.Join(dir, "recordings", "production.mp4"),
	})
	if err != nil {
		t.Fatalf("Run: %v gens %v", err, got)
	}
	if len(got) != 2 {
		t.Fatalf("Run recorded %d scenes: %v", len(got), got)
	}
	if got[0] == "" || got[0] != got[1] || !engine.ValidStateGeneration(got[0]) {
		t.Fatalf("minted generations: %v", got)
	}
}

func TestProducePassesOneGenerationToEveryScene(t *testing.T) {
	pr := testProducer(t, 2)
	pr.generation = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	var got []string
	prev := runEngine
	runEngine = func(_ *scene.Project, s *scene.Scene, opts engine.Options) error {
		got = append(got, opts.StateGeneration)
		if err := os.MkdirAll(filepath.Dir(opts.OutPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(opts.OutPath, []byte(s.Name), 0o644); err != nil {
			return err
		}
		return facts.Write(facts.Path(opts.OutPath), facts.Facts{Result: facts.ResultOK, Backstage: "t"})
	}
	t.Cleanup(func() { runEngine = prev })
	if err := pr.recordScene(0, segment{kind: "scene", name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := pr.recordScene(1, segment{kind: "scene", name: "beta"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != pr.generation || got[1] != pr.generation {
		t.Fatalf("generations: %v", got)
	}
	if !engine.ValidStateGeneration(got[0]) {
		t.Fatalf("empty or malformed generation: %q", got[0])
	}
}
