package scene

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVMEndPolicies(t *testing.T) {
	p := &Project{
		Dir:     t.TempDir(),
		Layouts: map[string]Layout{"solo": {Panes: []Pane{{Name: "t"}}}},
		VMs:     map[string]VMCfg{"managed": {Stage: "demo"}, "external": {Domain: "external", User: "u"}},
	}
	for _, tc := range []struct {
		name string
		s    *Scene
		bad  bool
		want string
	}{
		{"ok", &Scene{Name: "prod", Layout: "solo", VM: "managed", VMEnd: &VMEnd{Snapshot: "ready"}, Steps: []Step{{Action: "wait"}}}, false, ""},
		{"initial", &Scene{Name: "prod", Layout: "solo", VM: "managed", VMEnd: &VMEnd{Snapshot: "initial"}, Steps: []Step{{Action: "wait"}}}, true, "initial"},
		{"no-stage", &Scene{Name: "prod", Layout: "solo", VM: "external", VMEnd: &VMEnd{Snapshot: "ready"}, Steps: []Step{{Action: "wait"}}}, true, "managed"},
		{"no-vm", &Scene{Name: "prod", Layout: "solo", VMEnd: &VMEnd{Snapshot: "ready"}, Steps: []Step{{Action: "wait"}}}, true, "managed"},
		{"bad-name", &Scene{Name: "prod", Layout: "solo", VM: "managed", VMEnd: &VMEnd{Snapshot: "UPPER"}, Steps: []Step{{Action: "wait"}}}, true, "invalid"},
		{"visual", &Scene{Type: "visual", Name: "card", Entry: "card.html", Duration: 1, VMEnd: &VMEnd{Snapshot: "ready"}}, true, "recording configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.Validate(p)
			if tc.bad {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestContinueRefusesVMEndPredecessorWhenFileExists(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "scenes"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"one","layout":"solo","vm":"managed","vm-end":{"snapshot":"ready"},"steps":[{"action":"wait"}]}`
	if err := os.WriteFile(filepath.Join(dir, "scenes", "one.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Project{
		Dir:     dir,
		Layouts: map[string]Layout{"solo": {Panes: []Pane{{Name: "t"}}}},
		VMs:     map[string]VMCfg{"managed": {Stage: "demo"}},
	}
	s := &Scene{Name: "two", Layout: "solo", VM: "managed", VMStart: &VMStart{Mode: "continue", After: "one"}, Steps: []Step{{Action: "wait"}}}
	err := s.Validate(p)
	if err == nil || !strings.Contains(err.Error(), "ends the guest") {
		t.Fatalf("predecessor: %v", err)
	}
	s.VMStart.After = "missing"
	if err := s.Validate(p); err != nil {
		t.Fatalf("missing file: %v", err)
	}
}
