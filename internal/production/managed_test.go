package production

import (
	"github.com/This-Is-NPC/backstage/internal/scene"
	"os"
	"path/filepath"
	"testing"
)

func TestProductionValidatesAndReservesContinuityChain(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "scenes"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"one": `{"name":"one","vm":"a"}`, "two": `{"name":"two","vm":"b","vm-start":{"mode":"continue","after":"one"}}`} {
		if err := os.WriteFile(filepath.Join(dir, "scenes", name+".json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := &scene.Project{Dir: dir, VMs: map[string]scene.VMCfg{"a": {Stage: "shared"}, "b": {Stage: "shared"}}}
	prod := scene.Production{Scenes: []scene.SceneRef{{Scene: "one"}, {Scene: "two"}}}
	reserved, err := managedStages(p, prod)
	if err != nil {
		t.Fatal(err)
	}
	if len(reserved) != 1 || !reserved["shared"] {
		t.Fatal(reserved)
	}
	prod.Scenes = []scene.SceneRef{{Scene: "two"}, {Scene: "one"}}
	if _, err := managedStages(p, prod); err == nil {
		t.Fatal("accepted reversed dependency")
	}
}

func TestManagedStagesConsumerMustFollowProducer(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "scenes"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"make": `{"name":"make","vm":"box","vm-end":{"snapshot":"ready"}}`,
		"use":  `{"name":"use","vm":"box","vm-start":{"mode":"clean","snapshot":"ready"}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, "scenes", name+".json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := &scene.Project{Dir: dir, VMs: map[string]scene.VMCfg{"box": {Stage: "demo"}}}
	if _, err := managedStages(p, scene.Production{Scenes: []scene.SceneRef{{Scene: "use"}, {Scene: "make"}}}); err == nil {
		t.Fatal("accepted consumer before producer")
	}
	if _, err := managedStages(p, scene.Production{Scenes: []scene.SceneRef{{Scene: "make"}, {Scene: "use"}}}); err != nil {
		t.Fatal(err)
	}
}

func TestManagedStagesRejectsDuplicateProducers(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "scenes"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"one": `{"name":"one","vm":"box","vm-end":{"snapshot":"ready"}}`,
		"two": `{"name":"two","vm":"box","vm-end":{"snapshot":"ready"}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, "scenes", name+".json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := &scene.Project{Dir: dir, VMs: map[string]scene.VMCfg{"box": {Stage: "demo"}}}
	if _, err := managedStages(p, scene.Production{Scenes: []scene.SceneRef{{Scene: "one"}, {Scene: "two"}}}); err == nil {
		t.Fatal("accepted two producers of the same snapshot")
	}
	if _, err := managedStages(p, scene.Production{Scenes: []scene.SceneRef{{Scene: "one"}, {Scene: "one"}}}); err != nil {
		t.Fatalf("same scene may replace its own snapshot: %v", err)
	}
}

func TestManagedStagesContinueAfterVMEnd(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "scenes"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"make": `{"name":"make","vm":"box","vm-end":{"snapshot":"ready"}}`,
		"next": `{"name":"next","vm":"box","vm-start":{"mode":"continue","after":"make"}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, "scenes", name+".json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := &scene.Project{Dir: dir, VMs: map[string]scene.VMCfg{"box": {Stage: "demo"}}}
	if _, err := managedStages(p, scene.Production{Scenes: []scene.SceneRef{{Scene: "make"}, {Scene: "next"}}}); err == nil {
		t.Fatal("accepted continue after vm-end")
	}
}
