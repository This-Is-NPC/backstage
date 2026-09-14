package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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

func TestStageInspectJSONIncludesAtState(t *testing.T) {
	m := testStageRegistry(t)
	r := testStageRecord()
	r.AtState = &machine.AtState{
		Snapshot: "ready",
		Image:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Disk:     machine.FilePrint{Path: "/disk", Inode: 1, Size: 2, MtimeNs: 3, CtimeNs: 4},
		NVRAM:    machine.FilePrint{Path: "/nvram", Inode: 5, Size: 6, MtimeNs: 7, CtimeNs: 8},
	}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	cmd := stageInspectCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json", "demo"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got struct {
		AtState *machine.AtState `json:"at-state"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.AtState == nil || got.AtState.Snapshot != "ready" || got.AtState.Image != r.AtState.Image {
		t.Fatalf("at-state: %+v\n%s", got.AtState, out.String())
	}
	if got.AtState.Disk.CtimeNs != 4 || got.AtState.NVRAM.Inode != 5 {
		t.Fatalf("fingerprint: %+v", got.AtState)
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

type cliRunner func(context.Context, io.Reader, string, ...string) (string, error)

func (f cliRunner) Run(ctx context.Context, in io.Reader, name string, args ...string) (string, error) {
	return f(ctx, in, name, args...)
}

func fakeSnapshotMachine(t *testing.T) *machine.Manager {
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
	m.Store.Storage = t.TempDir()
	r := &machine.Record{
		Schema:    machine.Schema,
		ID:        "12345678901234567890123456789012",
		Name:      "demo",
		Domain:    "backstage-test-demo",
		URI:       m.URI,
		Spec:      machine.DefaultSpec(),
		Status:    "ready",
		Snapshots: map[string]string{},
	}
	r.Disk = filepath.Join(m.Store.Storage, r.ID+".qcow2")
	r.NVRAM = filepath.Join(m.Store.Storage, r.ID+".fd")
	if err := os.MkdirAll(m.Store.Storage, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{r.Disk, r.NVRAM} {
		if err := os.WriteFile(path, []byte("disk"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	key := filepath.Join(m.Store.Dir(r.Name), "id_ed25519")
	if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key+".pub", []byte("pub"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := m.Store.SaveCredentials(r.Name, machine.Credentials{Password: "pw", Key: key}); err != nil {
		t.Fatal(err)
	}
	m.Runner = cliRunner(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			if len(args) > 0 && args[0] == "info" {
				return `[{"filename":"` + r.Disk + `"}]`, nil
			}
			return "", os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
		}
		if len(args) > 2 {
			switch args[2] {
			case "domuuid":
				return r.ID[:8] + "-" + r.ID[8:12] + "-" + r.ID[12:16] + "-" + r.ID[16:20] + "-" + r.ID[20:], nil
			case "domstate":
				return "shut off", nil
			}
		}
		return "", nil
	})
	prev := newMachine
	newMachine = func() (*machine.Manager, error) { return m, nil }
	t.Cleanup(func() { newMachine = prev })
	return m
}

func TestStageSnapshotVerbFinishesWithoutCatalogHold(t *testing.T) {
	fakeSnapshotMachine(t)
	cmd := stageOperationCmd("snapshot")
	cmd.SetArgs([]string{"demo", "hand"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stage snapshot hung (catalog deadlock?)")
	}
}

func TestStageSnapshotTimesOutWhenCatalogHeld(t *testing.T) {
	m := fakeSnapshotMachine(t)
	hold, err := m.Store.LockMany("image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	cmd := stageOperationCmd("snapshot")
	cmd.SetArgs([]string{"demo", "hand"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	cmd.SetContext(ctx)
	err = cmd.Execute()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want timeout holding catalog, got %v", err)
	}
}

func TestStageRestoreIgnoresUnreadablePending(t *testing.T) {
	m := fakeSnapshotMachine(t)
	r, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	disk := filepath.Join(m.Store.Storage, id+"-image.qcow2")
	nvram := filepath.Join(m.Store.Storage, id+"-image.fd")
	for _, path := range []string{disk, nvram} {
		if err := os.WriteFile(path, []byte("img"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	body := []byte(`{"schema":1,"id":"` + id + `","disk":"` + disk + `","nvram":"` + nvram + `"}`)
	if err := os.MkdirAll(filepath.Join(m.Store.Root, "images"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Store.Root, "images", id+".json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	r.Snapshots["initial"] = id
	r.Video = "bochs"
	r.Firmware = "firmware"
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(m.Store.Root, "pending"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Store.Root, "pending", "bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := stageOperationCmd("restore")
	cmd.SetArgs([]string{"demo", "initial"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}
