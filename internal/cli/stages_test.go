package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/machine"
)

func TestStageListWithoutProjectIsAnEmptyJSONArray(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cmd := stageListCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var list []any
	if err := json.Unmarshal(out.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 || list == nil {
		t.Fatalf("unexpected list: %s", out.String())
	}
}

func TestStageDeleteRequiresMatchingConfirmationBeforeLibvirt(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	m, err := machine.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Store.Init(); err != nil {
		t.Fatal(err)
	}
	r := &machine.Record{Schema: machine.Schema, ID: "12345678901234567890123456789012", Name: "demo", Domain: "unused", Status: "ready"}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	cmd := stageOperationCmd("delete")
	cmd.SetArgs([]string{"demo"})
	cmd.SetIn(bytes.NewBufferString("other\n"))
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("accepted wrong confirmation")
	}
	if _, err := os.Stat(filepath.Join(m.Store.Dir("demo"), "stage.json")); err != nil {
		t.Fatal("deleted registry", err)
	}
}

func TestStageCommandsExposePlannedOperations(t *testing.T) {
	root := stageCmd()
	for _, verb := range []string{"doctor", "create", "list", "inspect", "start", "stop", "ssh", "credentials", "snapshot", "snapshots", "restore", "clone", "delete"} {
		cmd, _, err := root.Find([]string{verb})
		if err != nil || cmd == root {
			t.Fatalf("missing %s", verb)
		}
	}
}
