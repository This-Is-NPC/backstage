package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/guest"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

func groupProject(dir string, aliases ...string) *scene.Project {
	vms := map[string]scene.VMCfg{}
	members := append([]string{}, aliases...)
	if len(members) == 0 {
		members = []string{"laptop", "server"}
	}
	for _, alias := range members {
		vms[alias] = scene.VMCfg{Stage: "house-" + alias}
	}
	return &scene.Project{
		Dir:         dir,
		Record:      scene.RecordCfg{Out: "recordings"},
		Layouts:     map[string]scene.Layout{"solo": {Panes: []scene.Pane{{Name: "t"}}}},
		VMs:         vms,
		StateGroups: map[string][]string{"household": members},
	}
}

func saveGroupStage(t *testing.T, store *machine.Store, name, gen, take string) *machine.Record {
	t.Helper()
	r := &machine.Record{
		Schema: machine.Schema, ID: strings.Repeat("ab", 16), Name: name,
		Domain: "backstage-test-" + name, URI: "qemu:///system",
		Spec: machine.DefaultSpec(), Status: "ready",
		Source:    machine.Source{Image: "initial-img"},
		Snapshots: map[string]string{"initial": "initial-img", "linked": name + "-img"},
		SnapshotOrigins: map[string]machine.SnapshotOrigin{
			"linked": {
				Project:    "/p",
				Scene:      "make-" + name,
				Take:       take,
				Group:      "household",
				Generation: gen,
				Image:      name + "-img",
			},
		},
	}
	if err := store.Save(r); err != nil {
		t.Fatal(err)
	}
	return r
}

func groupEngine(t *testing.T, dir string, aliases ...string) (*Engine, *machine.Manager, *int, *int) {
	t.Helper()
	if len(aliases) == 0 {
		aliases = []string{"laptop", "server"}
	}
	e := playEngine(t, dir, nil)
	e.Project = groupProject(dir, aliases...)
	root := t.TempDir()
	store := &machine.Store{Root: filepath.Join(root, "reg"), Cache: filepath.Join(root, "cache"), Storage: filepath.Join(root, "storage")}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	for _, alias := range aliases {
		saveGroupStage(t, store, "house-"+alias, "gen-a", machine.TakeRecording)
	}
	e.Managed = &machine.Manager{Store: store}
	e.ManagedName = "house-laptop"
	restores := 0
	begins := 0
	prevRestore := restoreGroupMember
	restoreGroupMember = func(_ *machine.Manager, _ context.Context, _ *machine.Record, _, _ string) (bool, error) {
		restores++
		return false, nil
	}
	prevBegin := beginManaged
	beginManaged = func(_ *machine.Manager, _ context.Context, _ *machine.Record, mode, snapshot, _, _ string, _ bool) (*guest.Guest, error) {
		begins++
		g := guest.New("dom", "user", "", "", "")
		g.StartMode = mode
		g.Snapshot = snapshot
		return g, nil
	}
	prevFinish := finishManaged
	finishManaged = func(*machine.Manager, *machine.Record, *guest.Guest, string, string, bool) error { return nil }
	prevRecover := recoverGroupMember
	t.Cleanup(func() {
		restoreGroupMember = prevRestore
		recoverGroupMember = prevRecover
		beginManaged = prevBegin
		finishManaged = prevFinish
	})
	return e, e.Managed, &restores, &begins
}

func groupConsumer() *scene.Scene {
	return &scene.Scene{
		Name:    "use",
		Layout:  "solo",
		VM:      "laptop",
		VMStart: &scene.VMStart{Mode: "clean", Snapshot: "linked", Group: "household"},
		Steps:   []scene.Step{{Action: "wait"}},
	}
}

func TestGroupConsumerRestoresSilentsAndBootsFilmed(t *testing.T) {
	var boots atomic.Int32
	machine.SetBootGuest(func(*guest.Guest, time.Duration) error {
		boots.Add(1)
		return nil
	})
	t.Cleanup(func() { machine.SetBootGuest(nil) })
	dir := t.TempDir()
	e, _, restores, begins := groupEngine(t, dir)
	if err := e.Run(groupConsumer(), Options{Speed: 0.0001}); err != nil {
		t.Fatal(err)
	}
	if *restores != 1 {
		t.Fatalf("silent restores %d, want 1", *restores)
	}
	if *begins != 1 {
		t.Fatalf("filmed begins %d, want 1", *begins)
	}
	if boots.Load() != 0 {
		t.Fatalf("silent member booted (%d)", boots.Load())
	}
}

func TestFilmedWrongGenerationFailsBeforeRestore(t *testing.T) {
	dir := t.TempDir()
	e, m, restores, begins := groupEngine(t, dir)
	rec, err := m.Store.Load("house-laptop")
	if err != nil {
		t.Fatal(err)
	}
	o := rec.SnapshotOrigins["linked"]
	o.Generation = "gen-other"
	rec.SnapshotOrigins["linked"] = o
	if err := m.Store.Save(rec); err != nil {
		t.Fatal(err)
	}
	err = e.Run(groupConsumer(), Options{Record: true, Speed: 0.0001})
	if err == nil {
		t.Fatal("filmed other generation was ignored")
	}
	if *restores != 0 {
		t.Fatalf("restored before checks: %d", *restores)
	}
	if *begins != 0 {
		t.Fatalf("booted after failed check: %d", *begins)
	}
}

func TestSecondRestoreFailureDoesNotBoot(t *testing.T) {
	dir := t.TempDir()
	e, _, _, begins := groupEngine(t, dir, "laptop", "server", "tablet")
	n := 0
	restoreGroupMember = func(_ *machine.Manager, _ context.Context, _ *machine.Record, _, _ string) (bool, error) {
		n++
		if n == 2 {
			return false, errors.New("restore failed")
		}
		return false, nil
	}
	err := e.Run(groupConsumer(), Options{Speed: 0.0001})
	if err == nil || !strings.Contains(err.Error(), "restore failed") {
		t.Fatalf("second restore: %v", err)
	}
	if n != 2 {
		t.Fatalf("restore calls %d", n)
	}
	if *begins != 0 {
		t.Fatalf("booted after partial restore: %d", *begins)
	}
}

func TestGroupLockManyIncludesSilentMember(t *testing.T) {
	dir := t.TempDir()
	e, m, restores, _ := groupEngine(t, dir)
	_, release, err := m.Store.LockHold("house-server")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	err = e.Run(groupConsumer(), Options{Speed: 0.0001})
	if !errors.Is(err, machine.ErrBusy) {
		t.Fatalf("busy silent: %v", err)
	}
	if *restores != 0 {
		t.Fatalf("restored while a member lock was held: %d", *restores)
	}
}

func TestIsolatedProducerMintsNewGeneration(t *testing.T) {
	dir := t.TempDir()
	e, m, _, _ := groupEngine(t, dir)
	project, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(project); err == nil {
		project = resolved
	}
	rec, err := m.Store.Load("house-laptop")
	if err != nil {
		t.Fatal(err)
	}
	rec.SnapshotOrigins["linked"] = machine.SnapshotOrigin{
		Project: project, Scene: "make", Take: machine.TakeRecording,
		Group: "household", Generation: "old-gen", Image: rec.Snapshots["linked"],
	}
	if err := m.Store.Save(rec); err != nil {
		t.Fatal(err)
	}
	prevGen := newStateGeneration
	newStateGeneration = func() string { return "fresh-gen" }
	var got machine.SnapshotOrigin
	prevReplace := replaceTakeState
	replaceTakeState = func(_ *machine.Manager, _ context.Context, r *machine.Record, name string, origin machine.SnapshotOrigin, _ bool) (machine.ReplaceResult, error) {
		got = origin
		if r.SnapshotOrigins == nil {
			r.SnapshotOrigins = map[string]machine.SnapshotOrigin{}
		}
		r.Snapshots[name] = "new-img"
		r.SnapshotOrigins[name] = origin
		return machine.ReplaceResult{Image: &machine.Image{ID: "new-img"}}, nil
	}
	t.Cleanup(func() {
		newStateGeneration = prevGen
		replaceTakeState = prevReplace
	})
	s := &scene.Scene{
		Name:    "make",
		Layout:  "solo",
		VM:      "laptop",
		VMStart: &scene.VMStart{Mode: "clean", Snapshot: "initial"},
		VMEnd:   &scene.VMEnd{Snapshot: "linked", Group: "household"},
		Steps:   []scene.Step{{Action: "wait"}},
	}
	if err := e.Run(s, Options{ReplaceState: true, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	if got.Group != "household" || got.Generation != "fresh-gen" {
		t.Fatalf("origin: %+v", got)
	}
}

func TestGroupConsumerWritesFactsMembers(t *testing.T) {
	dir := t.TempDir()
	e, _, _, _ := groupEngine(t, dir)
	clip := filepath.Join(dir, "use.mp4")
	if err := e.Run(groupConsumer(), Options{Record: true, OutPath: clip, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	got := readFacts(t, clip)
	if len(got.GroupMembers) != 2 {
		t.Fatalf("group-members: %+v", got.GroupMembers)
	}
	seen := map[string]facts.GroupMember{}
	for _, m := range got.GroupMembers {
		seen[m.Stage] = m
		if m.Snapshot != "linked" || m.Generation != "gen-a" || m.Image == "" {
			t.Fatalf("member: %+v", m)
		}
	}
	if _, ok := seen["house-laptop"]; !ok {
		t.Fatal("filmed missing from facts")
	}
	if _, ok := seen["house-server"]; !ok {
		t.Fatal("silent missing from facts")
	}
	if seen["house-server"].RestoreSkipped || seen["house-laptop"].RestoreSkipped {
		t.Fatalf("stub restore marked skipped: %+v", got.GroupMembers)
	}
}

func TestSilentSkipMarksFactsRestoreSkipped(t *testing.T) {
	dir := t.TempDir()
	e, _, _, _ := groupEngine(t, dir)
	restoreGroupMember = func(_ *machine.Manager, _ context.Context, _ *machine.Record, _, _ string) (bool, error) {
		return true, nil
	}
	clip := filepath.Join(dir, "use.mp4")
	if err := e.Run(groupConsumer(), Options{Record: true, OutPath: clip, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	got := readFacts(t, clip)
	seen := map[string]facts.GroupMember{}
	for _, m := range got.GroupMembers {
		seen[m.Stage] = m
	}
	if !seen["house-server"].RestoreSkipped {
		t.Fatalf("silent skip not in facts: %+v", got.GroupMembers)
	}
	if seen["house-laptop"].RestoreSkipped {
		t.Fatalf("filmed marked skipped: %+v", got.GroupMembers)
	}
}

func TestPlayRefusesSilentRehearsalBeforeRestore(t *testing.T) {
	dir := t.TempDir()
	e, m, restores, _ := groupEngine(t, dir)
	rec, err := m.Store.Load("house-server")
	if err != nil {
		t.Fatal(err)
	}
	o := rec.SnapshotOrigins["linked"]
	o.Take = machine.TakeRehearsal
	rec.SnapshotOrigins["linked"] = o
	if err := m.Store.Save(rec); err != nil {
		t.Fatal(err)
	}
	err = e.Run(groupConsumer(), Options{Record: true, Speed: 0.0001})
	if err == nil || !strings.Contains(err.Error(), "rehearsal") {
		t.Fatalf("play silent rehearsal: %v", err)
	}
	if *restores != 0 {
		t.Fatalf("restored rehearsal member: %d", *restores)
	}
	if err := e.Run(groupConsumer(), Options{Speed: 0.0001}); err != nil {
		t.Fatalf("rehearse from recorded+rehearsal members: %v", err)
	}
}

func TestFilmedNotReadyRestoresNoSilent(t *testing.T) {
	dir := t.TempDir()
	e, m, restores, begins := groupEngine(t, dir)
	rec, err := m.Store.Load("house-laptop")
	if err != nil {
		t.Fatal(err)
	}
	rec.Status = "failed"
	if err := m.Store.Save(rec); err != nil {
		t.Fatal(err)
	}
	err = e.Run(groupConsumer(), Options{Speed: 0.0001})
	if err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("filmed not ready: %v", err)
	}
	if *restores != 0 {
		t.Fatalf("restored silent before filmed ready: %d", *restores)
	}
	if *begins != 0 {
		t.Fatalf("began after not ready: %d", *begins)
	}
}

func TestSilentNotReadyFailsBeforeRestore(t *testing.T) {
	dir := t.TempDir()
	e, m, restores, begins := groupEngine(t, dir)
	rec, err := m.Store.Load("house-server")
	if err != nil {
		t.Fatal(err)
	}
	rec.Status = "building"
	if err := m.Store.Save(rec); err != nil {
		t.Fatal(err)
	}
	err = e.Run(groupConsumer(), Options{Speed: 0.0001})
	if err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("silent not ready: %v", err)
	}
	if *restores != 0 {
		t.Fatalf("restored before silent ready: %d", *restores)
	}
	if *begins != 0 {
		t.Fatalf("began after silent not ready: %d", *begins)
	}
}

func TestSilentActivateJournalRecoveredBeforeRestore(t *testing.T) {
	dir := t.TempDir()
	e, m, restores, _ := groupEngine(t, dir)
	path := filepath.Join(m.Store.Dir("house-server"), "activate.json")
	if err := os.WriteFile(path, []byte(`{"leftover":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered := false
	restoreSawJournal := false
	recoverGroupMember = func(mgr *machine.Manager, _ context.Context, r *machine.Record) error {
		if r.Name == "house-server" {
			if _, err := os.Stat(path); err != nil {
				t.Fatal("journal missing at recover")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			recovered = true
		}
		return nil
	}
	restoreGroupMember = func(_ *machine.Manager, _ context.Context, r *machine.Record, _, _ string) (bool, error) {
		if r.Name == "house-server" {
			if _, err := os.Stat(path); err == nil {
				restoreSawJournal = true
			}
			*restores++
		}
		return false, nil
	}
	if err := e.Run(groupConsumer(), Options{Speed: 0.0001}); err != nil {
		t.Fatal(err)
	}
	if !recovered {
		t.Fatal("silent journal was not recovered")
	}
	if restoreSawJournal {
		t.Fatal("restore ran before recover cleared the journal")
	}
	if *restores != 1 {
		t.Fatalf("silent restores %d", *restores)
	}
}

func TestEnsureProducerGenerationLeavesConsumerEmpty(t *testing.T) {
	opts := Options{}
	ensureProducerGeneration(groupConsumer(), &opts)
	if opts.StateGeneration != "" {
		t.Fatalf("consumer minted %s", opts.StateGeneration)
	}
	prev := newStateGeneration
	newStateGeneration = func() string { return "minted" }
	t.Cleanup(func() { newStateGeneration = prev })
	s := &scene.Scene{VMEnd: &scene.VMEnd{Snapshot: "linked", Group: "household"}}
	ensureProducerGeneration(s, &opts)
	if opts.StateGeneration != "minted" {
		t.Fatalf("producer: %s", opts.StateGeneration)
	}
}

func TestValidStateGeneration(t *testing.T) {
	if !ValidStateGeneration(strings.Repeat("ab", 16)) {
		t.Fatal("32 lowercase hex")
	}
	if ValidStateGeneration("from-parent") {
		t.Fatal("short id")
	}
	if ValidStateGeneration(strings.Repeat("AB", 16)) {
		t.Fatal("uppercase")
	}
	if ValidStateGeneration(strings.Repeat("ab", 15) + "g") {
		t.Fatal("non-hex")
	}
}
