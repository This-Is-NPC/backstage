package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestStageSnapshotsJSONKeepsLegacyMap(t *testing.T) {
	m := testStageRegistry(t)
	r := testStageRecord()
	r.Snapshots = map[string]string{"initial": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "ready": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	origin := machine.SnapshotOrigin{
		Project: "/proj", Scene: "alpha", Image: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Take: machine.TakeRecording, Made: time.Unix(0, 0).UTC(), Backstage: "test",
	}
	r.SnapshotOrigins = map[string]machine.SnapshotOrigin{"ready": origin}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	cmd := stageOperationCmd("snapshots")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"demo"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var legacy map[string]string
	if err := json.Unmarshal(out.Bytes(), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy["initial"] != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || legacy["ready"] != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("legacy: %s", out.String())
	}
	if strings.Contains(out.String(), "origin") {
		t.Fatal("default snapshots listed origins")
	}
}

func TestStageSnapshotsOriginsFlag(t *testing.T) {
	m := testStageRegistry(t)
	r := testStageRecord()
	r.Snapshots = map[string]string{"initial": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "ready": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	origin := machine.SnapshotOrigin{
		Project: "/proj", Scene: "alpha", Image: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Take: machine.TakeRecording, Made: time.Unix(0, 0).UTC(), Backstage: "test",
	}
	r.SnapshotOrigins = map[string]machine.SnapshotOrigin{"ready": origin}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	cmd := stageOperationCmd("snapshots")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--origins", "demo"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var info map[string]machine.SnapshotInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info["initial"].Image != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || info["initial"].Origin != nil {
		t.Fatalf("manual: %#v", info["initial"])
	}
	if info["ready"].Origin == nil || info["ready"].Origin.Scene != "alpha" {
		t.Fatalf("produced: %#v", info["ready"])
	}
}

func TestStageSnapshotDeleteRefusesInitialAndRemovesOrigin(t *testing.T) {
	m := testStageRegistry(t)
	r := testStageRecord()
	keep := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	gone := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	r.Snapshots = map[string]string{"initial": keep, "ready": gone}
	r.SnapshotOrigins = map[string]machine.SnapshotOrigin{"ready": {Project: "/p", Scene: "s", Image: gone, Take: machine.TakeRecording}}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	refuse := stageOperationCmd("snapshot-delete")
	refuse.SetArgs([]string{"demo", "initial"})
	if err := refuse.Execute(); err == nil || !strings.Contains(err.Error(), "initial") {
		t.Fatalf("initial: %v", err)
	}
	cmd := stageOperationCmd("snapshot-delete")
	cmd.SetArgs([]string{"demo", "ready"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Snapshots["ready"]; ok {
		t.Fatal("mapping remains")
	}
	if _, ok := got.SnapshotOrigins["ready"]; ok {
		t.Fatal("origin remains")
	}
}

func TestStageInspectJSONIncludesSnapshotOrigins(t *testing.T) {
	m := testStageRegistry(t)
	r := testStageRecord()
	r.Snapshots = map[string]string{"ready": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	r.SnapshotOrigins = map[string]machine.SnapshotOrigin{"ready": {Project: "/p", Scene: "s", Image: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	cmd := stageInspectCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json", "demo"})
	_ = cmd.Execute()
	if !strings.Contains(out.String(), "snapshot-origins") || !strings.Contains(out.String(), `"scene": "s"`) {
		t.Fatalf("origins missing: %s", out.String())
	}
}

func testStageRegistry(t *testing.T) *machine.Manager {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	m, err := machine.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Store.Init(); err != nil {
		t.Fatal(err)
	}
	return m
}

func testStageRecord() *machine.Record {
	return &machine.Record{
		Schema: machine.Schema,
		ID:     "12345678901234567890123456789012",
		Name:   "demo",
		Domain: "unused",
		Status: "ready",
	}
}

func TestStageCommandsExposePlannedOperations(t *testing.T) {
	root := stageCmd()
	for _, verb := range []string{"doctor", "create", "list", "inspect", "start", "stop", "ssh", "credentials", "snapshot", "snapshots", "restore", "snapshot-delete", "prune-states", "clone", "delete"} {
		cmd, _, err := root.Find([]string{verb})
		if err != nil || cmd == root {
			t.Fatalf("missing %s", verb)
		}
	}
}
