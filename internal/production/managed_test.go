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
