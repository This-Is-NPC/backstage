package scene

import (
	"path/filepath"
	"strings"
	"testing"
)

func groupProject() *Project {
	return &Project{
		Layouts: map[string]Layout{"solo": {Panes: []Pane{{Name: "t"}}}},
		VMs: map[string]VMCfg{
			"laptop": {Stage: "house-laptop"},
			"server": {Stage: "house-server"},
			"tablet": {Stage: "house-tablet"},
			"extra":  {Stage: "house-extra"},
		},
		StateGroups: map[string][]string{"household": {"laptop", "server"}},
	}
}

func TestValidateStateGroupsRules(t *testing.T) {
	if err := groupProject().validateStateGroups(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		p    *Project
		want string
	}{
		{"unmanaged", &Project{
			VMs:         map[string]VMCfg{"laptop": {Stage: "house-laptop"}, "server": {Domain: "x", User: "u"}},
			StateGroups: map[string][]string{"household": {"laptop", "server"}},
		}, "managed"},
		{"unknown-alias", &Project{
			VMs:         map[string]VMCfg{"laptop": {Stage: "house-laptop"}},
			StateGroups: map[string][]string{"household": {"laptop", "ghost"}},
		}, "not a vm alias"},
		{"one-member", &Project{
			VMs:         map[string]VMCfg{"laptop": {Stage: "house-laptop"}},
			StateGroups: map[string][]string{"household": {"laptop"}},
		}, "at least two"},
		{"alias-two-groups", &Project{
			VMs: map[string]VMCfg{"laptop": {Stage: "a"}, "server": {Stage: "b"}, "tablet": {Stage: "c"}},
			StateGroups: map[string][]string{
				"household": {"laptop", "server"},
				"office":    {"laptop", "tablet"},
			},
		}, `vm "laptop" belongs`},
		{"stage-two-groups", &Project{
			VMs: map[string]VMCfg{"laptop": {Stage: "shared"}, "server": {Stage: "b"}, "other": {Stage: "shared"}, "extra": {Stage: "c"}},
			StateGroups: map[string][]string{
				"household": {"laptop", "server"},
				"office":    {"other", "extra"},
			},
		}, "stage"},
		{"bad-name", &Project{
			VMs:         map[string]VMCfg{"laptop": {Stage: "a"}, "server": {Stage: "b"}},
			StateGroups: map[string][]string{"HOUSE": {"laptop", "server"}},
		}, "invalid name"},
		{"repeated-alias", &Project{
			VMs:         map[string]VMCfg{"laptop": {Stage: "a"}, "server": {Stage: "b"}},
			StateGroups: map[string][]string{"household": {"laptop", "laptop"}},
		}, "repeated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.validateStateGroups()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err: %v", err)
			}
		})
	}
}

func TestStateGroupsInheritLikeNamedMaps(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"vms": {
			"laptop": {"stage": "house-laptop"},
			"server": {"stage": "house-server"},
			"tablet": {"stage": "house-tablet"},
			"extra": {"stage": "house-extra"}
		},
		"state-groups": {
			"household": ["laptop", "server"],
			"office": ["tablet", "extra"]
		}
	}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{
		"extends": "../backstage.json",
		"state-groups": {
			"household": ["laptop", "tablet"],
			"office": null
		}
	}`)
	p, err := LoadProject(filepath.Join(leaf, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.StateGroups["household"]; len(got) != 2 || got[0] != "laptop" || got[1] != "tablet" {
		t.Fatalf("replaced household: %v", p.StateGroups)
	}
	if _, ok := p.StateGroups["office"]; ok {
		t.Fatal("null should remove office")
	}

	keep := filepath.Join(ws, "keep")
	writeFile(t, filepath.Join(keep, "backstage.json"), `{"extends": "../backstage.json"}`)
	p, err = LoadProject(filepath.Join(keep, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.StateGroups["household"]) != 2 || len(p.StateGroups["office"]) != 2 {
		t.Fatalf("absent key should inherit: %v", p.StateGroups)
	}
}

func TestVMStartEndGroupValidation(t *testing.T) {
	p := groupProject()
	ok := &Scene{Name: "use", Layout: "solo", VM: "laptop", VMStart: &VMStart{Mode: "clean", Snapshot: "linked", Group: "household"}, Steps: []Step{{Action: "wait"}}}
	if err := ok.Validate(p); err != nil {
		t.Fatal(err)
	}

	err := (&Scene{Name: "use", Layout: "solo", VM: "laptop", VMStart: &VMStart{Mode: "clean", Group: "household"}, Steps: []Step{{Action: "wait"}}}).Validate(p)
	if err == nil || !strings.Contains(err.Error(), "requires a snapshot") {
		t.Fatalf("no snapshot: %v", err)
	}

	err = (&Scene{Name: "use", Layout: "solo", VM: "laptop", VMStart: &VMStart{Mode: "clean", Snapshot: "linked", Group: "missing"}, Steps: []Step{{Action: "wait"}}}).Validate(p)
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown group: %v", err)
	}

	err = (&Scene{Name: "use", Layout: "solo", VM: "tablet", VMStart: &VMStart{Mode: "clean", Snapshot: "linked", Group: "household"}, Steps: []Step{{Action: "wait"}}}).Validate(p)
	if err == nil || !strings.Contains(err.Error(), "not a member") {
		t.Fatalf("not a member: %v", err)
	}

	err = (&Scene{Name: "use", Layout: "solo", VM: "laptop", VMStart: &VMStart{Mode: "reuse", Group: "household"}, Steps: []Step{{Action: "wait"}}}).Validate(p)
	if err == nil || !strings.Contains(err.Error(), "group") {
		t.Fatalf("reuse group: %v", err)
	}

	err = (&Scene{Name: "use", Layout: "solo", VM: "laptop", VMStart: &VMStart{Mode: "continue", After: "prev", Group: "household"}, Steps: []Step{{Action: "wait"}}}).Validate(p)
	if err == nil || !strings.Contains(err.Error(), "group") {
		t.Fatalf("continue group: %v", err)
	}

	err = (&Scene{
		Name: "loop", Layout: "solo", VM: "laptop",
		VMStart: &VMStart{Mode: "clean", Snapshot: "linked", Group: "household"},
		VMEnd:   &VMEnd{Snapshot: "linked", Group: "household"},
		Steps:   []Step{{Action: "wait"}},
	}).Validate(p)
	if err == nil || !strings.Contains(err.Error(), "same group snapshot") {
		t.Fatalf("same snapshot: %v", err)
	}
}

func TestStartGroupStagesSorted(t *testing.T) {
	p := groupProject()
	s := &Scene{VM: "laptop", VMStart: &VMStart{Mode: "clean", Snapshot: "linked", Group: "household"}}
	got := StartGroupStages(p, s)
	if len(got) != 2 || got[0] != "house-laptop" || got[1] != "house-server" {
		t.Fatalf("stages: %v", got)
	}
	if StartGroupStages(p, &Scene{VMStart: &VMStart{Mode: "clean", Snapshot: "linked"}}) != nil {
		t.Fatal("no group should return nil")
	}
}

func TestConfigShowListsStateGroups(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"vms": {"laptop": {"stage": "house-laptop"}, "server": {"stage": "house-server"}},
		"state-groups": {"household": ["laptop", "server"]}
	}`)
	p, err := LoadProject(filepath.Join(dir, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := WriteConfigShow(&b, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "state-groups.household") {
		t.Fatalf("show:\n%s", b.String())
	}
}
