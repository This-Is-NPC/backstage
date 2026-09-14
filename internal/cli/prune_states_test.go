package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/workspace"
)

const (
	pruneInitial = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pruneReady   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	pruneOld     = "cccccccccccccccccccccccccccccccc"
	pruneHand    = "dddddddddddddddddddddddddddddddd"
	pruneForeign = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	pruneLive    = "ffffffffffffffffffffffffffffffff"
)

func TestPruneStatesRenamedSceneRemovesOldState(t *testing.T) {
	dir, store, mgr := pruneWorkspace(t)
	writePruneScene(t, dir, "setup", pruneClean("setup", "", "ready"))
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "old-theme": pruneOld, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"old-theme": {Project: dir, Scene: "theme", Take: machine.TakeRecording, Image: pruneOld},
		"ready":     {Project: dir, Scene: "setup", Take: machine.TakeRecording, Image: pruneReady},
	})
	var deleted []string
	out, err := runPrune(t, pruneStatesExec{Store: store, Delete: recordDelete(&deleted, mgr)}, "demo", dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(deleted, []string{"old-theme"}) {
		t.Fatalf("deleted %v", deleted)
	}
	if !strings.Contains(out, "removed old-theme (undeclared)") {
		t.Fatalf("report: %s", out)
	}
	got := loadPruneStage(t, store)
	if _, ok := got.Snapshots["old-theme"]; ok {
		t.Fatal("old mapping remains")
	}
	if _, ok := got.Snapshots["ready"]; !ok {
		t.Fatal("current producer was removed")
	}
}

func TestPruneStatesKeepsProtectedSnapshots(t *testing.T) {
	dir, store, mgr := pruneWorkspace(t)
	writePruneScene(t, dir, "use", pruneClean("use", "held", ""))
	savePruneStage(t, store, map[string]string{
		"initial": pruneInitial,
		"hand":    pruneHand,
		"foreign": pruneForeign,
		"held":    pruneReady,
		"live":    pruneLive,
	}, map[string]machine.SnapshotOrigin{
		"foreign": {Project: "/other/workspace/proj", Scene: "make", Take: machine.TakeRecording, Image: pruneForeign},
		"held":    {Project: dir, Scene: "gone", Take: machine.TakeRecording, Image: pruneReady},
		"live":    {Project: dir, Scene: "gone", Take: machine.TakeRecording, Image: pruneLive},
	})
	rec := loadPruneStage(t, store)
	rec.Source.Image = pruneLive
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}
	var deleted []string
	out, err := runPrune(t, pruneStatesExec{Store: store, Delete: recordDelete(&deleted, mgr)}, "demo", dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 {
		t.Fatalf("deleted %v", deleted)
	}
	for _, line := range []string{
		"kept initial (initial)",
		"kept hand (manual)",
		"kept foreign (outside-workspace)",
		"kept held (consumed)",
		"kept live (in-use)",
	} {
		if !strings.Contains(out, line) {
			t.Fatalf("missing %q in %s", line, out)
		}
	}
}

func TestPruneStatesDryRunMatchesRealAndCreatesNoFiles(t *testing.T) {
	dir, store, mgr := pruneWorkspace(t)
	writePruneScene(t, dir, "setup", pruneClean("setup", "", "ready"))
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "old-theme": pruneOld, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"old-theme": {Project: dir, Scene: "theme", Take: machine.TakeRecording, Image: pruneOld},
		"ready":     {Project: dir, Scene: "setup", Take: machine.TakeRecording, Image: pruneReady},
	})
	dataHome := os.Getenv("XDG_DATA_HOME")
	before := listRegularFiles(t, dir, dataHome)
	dry := pruneStatesExec{
		Store: store,
		Delete: func(*machine.Record, string) (string, error) {
			t.Fatal("dry-run called delete")
			return "", nil
		},
		Lock: func(...string) (func(), error) {
			t.Fatal("dry-run took a lock")
			return func() {}, nil
		},
	}
	dryOut, err := runPrune(t, dry, "demo", dir, true)
	if err != nil {
		t.Fatal(err)
	}
	afterDry := listRegularFiles(t, dir, dataHome)
	if !slices.Equal(before, afterDry) {
		t.Fatalf("dry-run created files:\nbefore %v\nafter %v", before, afterDry)
	}
	var deleted []string
	realOut, err := runPrune(t, pruneStatesExec{Store: store, Delete: recordDelete(&deleted, mgr)}, "demo", dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dryOut, "would remove old-theme (undeclared)") {
		t.Fatalf("dry-run wording: %s", dryOut)
	}
	if namesFromReport(t, dryOut, "would remove") != strings.Join(deleted, ",") {
		t.Fatalf("dry-run %q real %v", dryOut, deleted)
	}
	if namesFromReport(t, dryOut, "kept") != namesFromReport(t, realOut, "kept") {
		t.Fatalf("kept dry-run %q real %q", dryOut, realOut)
	}
}

func TestPruneStatesBrokenLeafConfigAbortsWithAndWithoutDryRun(t *testing.T) {
	root, store, _ := pruneWorkspace(t)
	en := filepath.Join(root, "en")
	writeFile(t, filepath.Join(en, "backstage.json"), `{`)
	writePruneScene(t, en, "make", pruneClean("make", "", "ready"))
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: en, Scene: "make", Take: machine.TakeRecording, Image: pruneReady},
	})
	for _, dry := range []bool{true, false} {
		args := []string{"--workspace", root, "demo"}
		if dry {
			args = append([]string{"--dry-run"}, args...)
		}
		out, errOut, err := runPruneFull(t, pruneStatesExec{
			Store: store,
			Delete: func(*machine.Record, string) (string, error) {
				t.Fatalf("delete after warning dry-run=%v", dry)
				return "", nil
			},
			Lock: func(...string) (func(), error) {
				t.Fatalf("lock after warning dry-run=%v", dry)
				return func() {}, nil
			},
		}, args)
		if !errors.Is(err, errWorkspacePrune) {
			t.Fatalf("dry-run=%v err %v", dry, err)
		}
		if errOut != "" {
			t.Fatalf("dry-run=%v abort must leave stderr empty: %q", dry, errOut)
		}
		if !strings.Contains(out, "Warnings:") {
			t.Fatalf("dry-run=%v warnings not listed: %s", dry, out)
		}
		if strings.Contains(out, "removed ") {
			t.Fatalf("dry-run=%v printed a plan: %s", dry, out)
		}
		if _, ok := loadPruneStage(t, store).Snapshots["ready"]; !ok {
			t.Fatalf("dry-run=%v removed despite warning", dry)
		}
	}
}

func TestPruneStatesSiblingSceneErrorAborts(t *testing.T) {
	root, store, _ := pruneWorkspace(t)
	en := filepath.Join(root, "en")
	pt := filepath.Join(root, "pt")
	writeFile(t, filepath.Join(en, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeFile(t, filepath.Join(pt, "backstage.json"), `{"extends":"../backstage.json"}`)
	writePruneScene(t, en, "broken", `{`)
	writePruneScene(t, pt, "use", pruneClean("use", "ready", ""))
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: en, Scene: "make", Take: machine.TakeRecording, Image: pruneReady},
	})
	out, errOut, err := runPruneFull(t, pruneStatesExec{
		Store: store,
		Delete: func(*machine.Record, string) (string, error) {
			t.Fatal("delete after sibling scene error")
			return "", nil
		},
		Lock: func(...string) (func(), error) {
			t.Fatal("lock after sibling scene error")
			return func() {}, nil
		},
	}, []string{"--workspace", pt, "demo"})
	if !errors.Is(err, errWorkspacePrune) {
		t.Fatalf("err %v", err)
	}
	if errOut != "" {
		t.Fatalf("abort must leave stderr empty: %q", errOut)
	}
	if !strings.Contains(out, "Errors:") {
		t.Fatalf("errors not listed: %s", out)
	}
	if _, ok := loadPruneStage(t, store).Snapshots["ready"]; !ok {
		t.Fatal("removed despite sibling scene error")
	}
}

func TestPruneStatesWorkspaceErrorRemovesNothing(t *testing.T) {
	dir, store, _ := pruneWorkspace(t)
	writePruneScene(t, dir, "broken", `{`)
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: dir, Scene: "make", Take: machine.TakeRecording, Image: pruneReady},
	})
	out, errOut, err := runPruneFull(t, pruneStatesExec{
		Store: store,
		Delete: func(*machine.Record, string) (string, error) {
			t.Fatal("delete after workspace error")
			return "", nil
		},
		Lock: func(...string) (func(), error) {
			t.Fatal("lock after workspace error")
			return func() {}, nil
		},
	}, []string{"--workspace", dir, "demo"})
	if !errors.Is(err, errWorkspacePrune) {
		t.Fatalf("err %v", err)
	}
	if errOut != "" {
		t.Fatalf("abort must leave stderr empty: %q", errOut)
	}
	if !strings.Contains(out, "Errors:") {
		t.Fatalf("errors not listed: %s", out)
	}
	if _, ok := loadPruneStage(t, store).Snapshots["ready"]; !ok {
		t.Fatal("removed despite workspace error")
	}
}

func TestPruneStatesBusyRemovesNothing(t *testing.T) {
	dir, store, _ := pruneWorkspace(t)
	writePruneScene(t, dir, "setup", pruneClean("setup", "", "ready"))
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "old-theme": pruneOld}, map[string]machine.SnapshotOrigin{
		"old-theme": {Project: dir, Scene: "theme", Take: machine.TakeRecording, Image: pruneOld},
	})
	release, err := store.LockMany("demo")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var deleted []string
	_, errOut, err := runPruneFull(t, pruneStatesExec{
		Store: store,
		Delete: func(*machine.Record, string) (string, error) {
			t.Fatal("delete while busy")
			return "", nil
		},
	}, []string{"--workspace", dir, "demo"})
	if !errors.Is(err, machine.ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
	if !strings.Contains(errOut, "busy") {
		t.Fatalf("busy error must go to stderr: %q", errOut)
	}
	if len(deleted) != 0 {
		t.Fatalf("deleted %v", deleted)
	}
	if _, ok := loadPruneStage(t, store).Snapshots["old-theme"]; !ok {
		t.Fatal("removed while busy")
	}
}

func TestPruneStatesCollectWarningKeepsRemoval(t *testing.T) {
	dir, store, mgr := pruneWorkspace(t)
	writePruneScene(t, dir, "setup", pruneClean("setup", "", "ready"))
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "old-theme": pruneOld, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"old-theme": {Project: dir, Scene: "theme", Take: machine.TakeRecording, Image: pruneOld},
		"ready":     {Project: dir, Scene: "setup", Take: machine.TakeRecording, Image: pruneReady},
	})
	out, err := runPrune(t, pruneStatesExec{
		Store: store,
		Delete: func(r *machine.Record, name string) (string, error) {
			if _, err := mgr.DeleteSnapshot(r, name); err != nil {
				return "", err
			}
			return "pending cleanup: collect busy", nil
		},
	}, "demo", dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "warning: pending cleanup: collect busy") {
		t.Fatalf("warning: %s", out)
	}
	if _, ok := loadPruneStage(t, store).Snapshots["old-theme"]; ok {
		t.Fatal("collect warning rolled back the removal")
	}
}

func TestPruneStatesTwoLeavesShareTheStage(t *testing.T) {
	root, store, mgr := pruneWorkspace(t)
	en := filepath.Join(root, "en")
	pt := filepath.Join(root, "pt")
	writeFile(t, filepath.Join(en, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeFile(t, filepath.Join(pt, "backstage.json"), `{"extends":"../backstage.json"}`)
	writePruneScene(t, en, "make", pruneClean("make", "", "ready"))
	writePruneScene(t, pt, "use", pruneClean("use", "ready", ""))
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "ready": pruneReady, "old": pruneOld}, map[string]machine.SnapshotOrigin{
		"ready": {Project: en, Scene: "make", Take: machine.TakeRecording, Image: pruneReady},
		"old":   {Project: en, Scene: "theme", Take: machine.TakeRecording, Image: pruneOld},
	})
	var deleted []string
	_, err := runPrune(t, pruneStatesExec{Store: store, Delete: recordDelete(&deleted, mgr)}, "demo", pt, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(deleted, []string{"old"}) {
		t.Fatalf("from pt: %v", deleted)
	}
	got := loadPruneStage(t, store)
	if _, ok := got.Snapshots["ready"]; !ok {
		t.Fatal("sibling producer was pruned from the other leaf")
	}
}

func TestPruneStatesJSONAndRequiredWorkspace(t *testing.T) {
	cmd := stagePruneStatesCmd()
	if cmd.Flags().Lookup("json") == nil || cmd.Flags().Lookup("dry-run") == nil || cmd.Flags().Lookup("workspace") == nil || cmd.Flags().Lookup("include-missing-projects") == nil {
		t.Fatal("flags")
	}
	if cmd.Use != "prune-states STAGE" {
		t.Fatalf("use: %s", cmd.Use)
	}
	_, errOut, err := runPruneFull(t, pruneStatesExec{Store: testStageRegistry(t).Store}, []string{"demo"})
	if err == nil {
		t.Fatal("workspace must be required")
	}
	if ExitStatus(err) != 1 {
		t.Fatalf("exit %d", ExitStatus(err))
	}
	if !strings.Contains(errOut, "workspace") {
		t.Fatalf("missing --workspace must go to stderr: %q", errOut)
	}
}

func TestPruneStatesDryRunJSON(t *testing.T) {
	dir, store, _ := pruneWorkspace(t)
	writePruneScene(t, dir, "setup", pruneClean("setup", "", "ready"))
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "old-theme": pruneOld, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"old-theme": {Project: dir, Scene: "theme", Take: machine.TakeRecording, Image: pruneOld},
		"ready":     {Project: dir, Scene: "setup", Take: machine.TakeRecording, Image: pruneReady},
	})
	cmd := pruneStatesCmd(pruneStatesExec{Store: store, Delete: func(*machine.Record, string) (string, error) {
		t.Fatal("dry-run delete")
		return "", nil
	}, Lock: func(...string) (func(), error) {
		t.Fatal("dry-run lock")
		return func() {}, nil
	}})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json", "--dry-run", "--workspace", dir, "demo"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	report := decodePruneReport(t, out.String())
	if !report.DryRun || report.Stage != "demo" || len(report.Removed) != 1 || report.Removed[0].Snapshot != "old-theme" {
		t.Fatalf("report: %#v", report)
	}
	if report.CleanupWarnings == nil {
		t.Fatal("cleanup-warnings must be a string list")
	}
}

func TestPruneStatesLocksStageAndImageCatalog(t *testing.T) {
	dir, store, mgr := pruneWorkspace(t)
	writePruneScene(t, dir, "setup", pruneClean("setup", "", "ready"))
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "old-theme": pruneOld, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"old-theme": {Project: dir, Scene: "theme", Take: machine.TakeRecording, Image: pruneOld},
		"ready":     {Project: dir, Scene: "setup", Take: machine.TakeRecording, Image: pruneReady},
	})
	var locked []string
	_, err := runPrune(t, pruneStatesExec{
		Store:  store,
		Delete: recordDelete(new([]string), mgr),
		Lock: func(names ...string) (func(), error) {
			locked = append([]string{}, names...)
			return func() {}, nil
		},
	}, "demo", dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(locked) != 2 || !containsName(locked, "demo") || !containsName(locked, "image-catalog") {
		t.Fatalf("locks %v", locked)
	}
}

func TestPruneStatesStopsAtFirstFailure(t *testing.T) {
	dir, store, _ := pruneWorkspace(t)
	writePruneScene(t, dir, "setup", pruneClean("setup", "", "ready"))
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "old-a": pruneOld, "old-b": pruneHand, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"old-a": {Project: dir, Scene: "theme", Take: machine.TakeRecording, Image: pruneOld},
		"old-b": {Project: dir, Scene: "gone", Take: machine.TakeRecording, Image: pruneHand},
		"ready": {Project: dir, Scene: "setup", Take: machine.TakeRecording, Image: pruneReady},
	})
	var deleted []string
	out, errOut, err := runPruneFull(t, pruneStatesExec{
		Store: store,
		Lock:  func(...string) (func(), error) { return func() {}, nil },
		Delete: func(_ *machine.Record, name string) (string, error) {
			deleted = append(deleted, name)
			return "", errors.New("disk full")
		},
	}, []string{"--workspace", dir, "demo"})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err %v", err)
	}
	if !slices.Equal(deleted, []string{"old-a"}) {
		t.Fatalf("attempted %v", deleted)
	}
	if !strings.Contains(out, "failed old-a: disk full") || !strings.Contains(out, "not attempted old-b") {
		t.Fatalf("text report: %s", out)
	}
	if !strings.Contains(errOut, "disk full") {
		t.Fatalf("stderr: %q", errOut)
	}

	jsonOut, _, err := runPruneFull(t, pruneStatesExec{
		Store: store,
		Lock:  func(...string) (func(), error) { return func() {}, nil },
		Delete: func(_ *machine.Record, name string) (string, error) {
			return "", errors.New("disk full")
		},
	}, []string{"--json", "--workspace", dir, "demo"})
	if err == nil {
		t.Fatal("json run succeeded")
	}
	report := decodePruneReport(t, jsonOut)
	if report.Failed == nil || report.Failed.Snapshot != "old-a" || report.Failed.Error != "disk full" {
		t.Fatalf("failed: %#v", report.Failed)
	}
	if !slices.Equal(report.NotAttempted, []string{"old-b"}) {
		t.Fatalf("not-attempted: %v", report.NotAttempted)
	}
	if report.CleanupWarnings == nil {
		t.Fatal("cleanup-warnings must be a string list")
	}
}

func TestPruneStatesPlanUsesRecordLoadedUnderLock(t *testing.T) {
	dir, store, mgr := pruneWorkspace(t)
	writePruneScene(t, dir, "setup", pruneClean("setup", "", "ready"))
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "old-theme": pruneOld, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"old-theme": {Project: dir, Scene: "theme", Take: machine.TakeRecording, Image: pruneOld},
		"ready":     {Project: dir, Scene: "setup", Take: machine.TakeRecording, Image: pruneReady},
	})
	var deleted []string
	_, err := runPrune(t, pruneStatesExec{
		Store:  store,
		Delete: recordDelete(&deleted, mgr),
		Lock: func(...string) (func(), error) {
			rec := loadPruneStage(t, store)
			rec.Source.Image = pruneOld
			if err := store.Save(rec); err != nil {
				t.Fatal(err)
			}
			return func() {}, nil
		},
	}, "demo", dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(deleted, "old-theme") {
		t.Fatalf("lock-mutated in-use snapshot was deleted: %v", deleted)
	}
	if _, ok := loadPruneStage(t, store).Snapshots["old-theme"]; !ok {
		t.Fatal("old-theme mapping gone")
	}
}

func TestPruneStatesReevaluateUnderLockSeesNewProducer(t *testing.T) {
	dir, store, mgr := pruneWorkspace(t)
	writePruneScene(t, dir, "host", `{"name":"host","layout":"solo","steps":[{"action":"wait","delay-after":0.05}]}`)
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: dir, Scene: "make", Take: machine.TakeRecording, Image: pruneReady},
	})
	var deleted []string
	_, err := runPrune(t, pruneStatesExec{
		Store:  store,
		Delete: recordDelete(&deleted, mgr),
		Lock: func(...string) (func(), error) {
			writePruneScene(t, dir, "setup", pruneClean("setup", "", "ready"))
			return func() {}, nil
		},
	}, "demo", dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 {
		t.Fatalf("re-eval producer was pruned: %v", deleted)
	}
}

func TestPruneStatesReevaluateUnderLockAbortsOnNewSceneError(t *testing.T) {
	dir, store, _ := pruneWorkspace(t)
	writePruneScene(t, dir, "host", `{"name":"host","layout":"solo","steps":[{"action":"wait","delay-after":0.05}]}`)
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: dir, Scene: "make", Take: machine.TakeRecording, Image: pruneReady},
	})
	out, errOut, err := runPruneFull(t, pruneStatesExec{
		Store: store,
		Delete: func(*machine.Record, string) (string, error) {
			t.Fatal("delete after re-eval error")
			return "", nil
		},
		Lock: func(...string) (func(), error) {
			writePruneScene(t, dir, "broken", `{`)
			return func() {}, nil
		},
	}, []string{"--workspace", dir, "demo"})
	if !errors.Is(err, errWorkspacePrune) {
		t.Fatalf("err %v", err)
	}
	if errOut != "" {
		t.Fatalf("abort must leave stderr empty: %q", errOut)
	}
	if !strings.Contains(out, "Errors:") {
		t.Fatalf("errors not listed: %s", out)
	}
	if _, ok := loadPruneStage(t, store).Snapshots["ready"]; !ok {
		t.Fatal("removed after re-eval error")
	}
}

func TestPruneStatesAbortLeavesStderrEmpty(t *testing.T) {
	root, store, _ := pruneWorkspace(t)
	en := filepath.Join(root, "en")
	writeFile(t, filepath.Join(en, "backstage.json"), `{`)
	writePruneScene(t, en, "make", pruneClean("make", "", "ready"))
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: en, Scene: "make", Take: machine.TakeRecording, Image: pruneReady},
	})
	_, errOut, err := runPruneFull(t, pruneStatesExec{
		Store: store,
		Delete: func(*machine.Record, string) (string, error) {
			t.Fatal("delete after abort")
			return "", nil
		},
		Lock: func(...string) (func(), error) {
			t.Fatal("lock after abort")
			return func() {}, nil
		},
	}, []string{"--workspace", root, "demo"})
	if !errors.Is(err, errWorkspacePrune) {
		t.Fatalf("err %v", err)
	}
	if errOut != "" {
		t.Fatalf("S3: abort must leave stderr empty: %q", errOut)
	}
}

func TestPruneStatesConflictTextLeavesStderrEmpty(t *testing.T) {
	dir, store := conflictPruneWorkspace(t)
	out, errOut, err := runPruneFull(t, pruneStatesExec{
		Store: store,
		Delete: func(*machine.Record, string) (string, error) {
			t.Fatal("delete after conflict")
			return "", nil
		},
		Lock: func(...string) (func(), error) {
			t.Fatal("lock after conflict")
			return func() {}, nil
		},
	}, []string{"--workspace", dir, "demo"})
	if err == nil {
		t.Fatal("conflict must fail")
	}
	var conflict *workspace.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err %v", err)
	}
	if errOut != "" {
		t.Fatalf("S4: conflict text must leave stderr empty: %q", errOut)
	}
	if !strings.Contains(out, "two scenes") {
		t.Fatalf("conflict not on stdout: %s", out)
	}
}

func TestPruneStatesConflictJSONLeavesStderrEmpty(t *testing.T) {
	dir, store := conflictPruneWorkspace(t)
	out, errOut, err := runPruneFull(t, pruneStatesExec{
		Store: store,
		Delete: func(*machine.Record, string) (string, error) {
			t.Fatal("delete after conflict")
			return "", nil
		},
		Lock: func(...string) (func(), error) {
			t.Fatal("lock after conflict")
			return func() {}, nil
		},
	}, []string{"--json", "--workspace", dir, "demo"})
	if err == nil {
		t.Fatal("conflict must fail")
	}
	if errOut != "" {
		t.Fatalf("S5: conflict JSON must leave stderr empty: %q", errOut)
	}
	var conflict workspace.ConflictError
	decodeOneJSON(t, out, &conflict)
	if len(conflict.Duplicates) == 0 {
		t.Fatalf("conflict JSON: %s", out)
	}
}

func TestPruneStatesEvaluateErrorGoesToStderr(t *testing.T) {
	store := testStageRegistry(t).Store
	empty := t.TempDir()
	out, errOut, err := runPruneFull(t, pruneStatesExec{
		Store: store,
		Delete: func(*machine.Record, string) (string, error) {
			t.Fatal("delete after evaluate error")
			return "", nil
		},
		Lock: func(...string) (func(), error) {
			t.Fatal("lock after evaluate error")
			return func() {}, nil
		},
	}, []string{"--workspace", empty, "demo"})
	if err == nil {
		t.Fatal("missing project must fail")
	}
	if ExitStatus(err) != 1 {
		t.Fatalf("exit %d", ExitStatus(err))
	}
	if !strings.Contains(errOut, "backstage.json") {
		t.Fatalf("S6: evaluate error must go to stderr: %q", errOut)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("text evaluate error must not write stdout: %s", out)
	}
}

func TestPruneStatesDryRunLoadErrorGoesToStderr(t *testing.T) {
	dir, store, _ := pruneWorkspace(t)
	writePruneScene(t, dir, "host", `{"name":"host","layout":"solo","steps":[{"action":"wait","delay-after":0.05}]}`)
	_, errOut, err := runPruneFull(t, pruneStatesExec{
		Store: store,
		Delete: func(*machine.Record, string) (string, error) {
			t.Fatal("dry-run delete")
			return "", nil
		},
		Lock: func(...string) (func(), error) {
			t.Fatal("dry-run lock")
			return func() {}, nil
		},
	}, []string{"--dry-run", "--workspace", dir, "demo"})
	if err == nil {
		t.Fatal("missing stage must fail")
	}
	if ExitStatus(err) != 1 {
		t.Fatalf("exit %d", ExitStatus(err))
	}
	if !strings.Contains(errOut, "demo") {
		t.Fatalf("S7: Load error must go to stderr: %q", errOut)
	}
}

func TestPruneStatesFlagAndArgErrorsGoToStderr(t *testing.T) {
	store := testStageRegistry(t).Store
	dir, _, _ := pruneWorkspace(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing-workspace", []string{"demo"}, "workspace"},
		{"missing-stage", []string{"--workspace", dir}, "accepts 1 arg"},
		{"unknown-flag", []string{"--not-a-flag", "--workspace", dir, "demo"}, "unknown flag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, err := runPruneFull(t, pruneStatesExec{Store: store}, tc.args)
			if err == nil {
				t.Fatal("expected error")
			}
			if ExitStatus(err) != 1 {
				t.Fatalf("exit %d err=%v", ExitStatus(err), err)
			}
			if !strings.Contains(errOut, tc.want) {
				t.Fatalf("stderr %q want %q", errOut, tc.want)
			}
			if strings.Count(errOut, tc.want) != 1 {
				t.Fatalf("error must appear once: %q", errOut)
			}
			if strings.TrimSpace(out) != "" {
				t.Fatalf("stdout should stay empty without --json: %s", out)
			}
		})
	}
}

func TestPruneStatesJSONOneDocument(t *testing.T) {
	dir, store, _ := pruneWorkspace(t)
	writePruneScene(t, dir, "setup", pruneClean("setup", "", "ready"))
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "old-theme": pruneOld, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"old-theme": {Project: dir, Scene: "theme", Take: machine.TakeRecording, Image: pruneOld},
		"ready":     {Project: dir, Scene: "setup", Take: machine.TakeRecording, Image: pruneReady},
	})

	successOut, _, err := runPruneFull(t, pruneStatesExec{
		Store: store,
		Lock:  func(...string) (func(), error) { return func() {}, nil },
		Delete: func(*machine.Record, string) (string, error) {
			return "", nil
		},
	}, []string{"--json", "--workspace", dir, "demo"})
	if err != nil {
		t.Fatal(err)
	}
	success := decodePruneReport(t, successOut)
	if success.CleanupWarnings == nil {
		t.Fatal("success cleanup-warnings")
	}

	midOut, _, err := runPruneFull(t, pruneStatesExec{
		Store: store,
		Lock:  func(...string) (func(), error) { return func() {}, nil },
		Delete: func(*machine.Record, string) (string, error) {
			return "", errors.New("disk full")
		},
	}, []string{"--json", "--workspace", dir, "demo"})
	if err == nil {
		t.Fatal("mid-failure succeeded")
	}
	mid := decodePruneReport(t, midOut)
	if mid.Failed == nil || mid.CleanupWarnings == nil {
		t.Fatalf("mid-failure report: %#v", mid)
	}

	abortRoot, abortStore, _ := pruneWorkspace(t)
	writeFile(t, filepath.Join(abortRoot, "en", "backstage.json"), `{`)
	writePruneScene(t, filepath.Join(abortRoot, "en"), "make", pruneClean("make", "", "ready"))
	abortOut, abortErr, err := runPruneFull(t, pruneStatesExec{
		Store: abortStore,
		Lock:  func(...string) (func(), error) { t.Fatal("lock"); return func() {}, nil },
	}, []string{"--json", "--workspace", abortRoot, "demo"})
	if !errors.Is(err, errWorkspacePrune) {
		t.Fatalf("abort err %v", err)
	}
	if abortErr != "" {
		t.Fatalf("abort stderr %q", abortErr)
	}
	var abort workspace.Result
	decodeOneJSON(t, abortOut, &abort)
	if len(abort.Warnings) == 0 {
		t.Fatalf("abort Result: %s", abortOut)
	}

	conflictDir, conflictStore := conflictPruneWorkspace(t)
	conflictOut, conflictErr, err := runPruneFull(t, pruneStatesExec{
		Store: conflictStore,
		Lock:  func(...string) (func(), error) { t.Fatal("lock"); return func() {}, nil },
	}, []string{"--json", "--workspace", conflictDir, "demo"})
	if err == nil {
		t.Fatal("conflict succeeded")
	}
	if conflictErr != "" {
		t.Fatalf("conflict stderr %q", conflictErr)
	}
	var conflict workspace.ConflictError
	decodeOneJSON(t, conflictOut, &conflict)
	if len(conflict.Duplicates) == 0 {
		t.Fatalf("conflict: %s", conflictOut)
	}

	busyDir, busyStore, _ := pruneWorkspace(t)
	writePruneScene(t, busyDir, "setup", pruneClean("setup", "", "ready"))
	savePruneStage(t, busyStore, map[string]string{"initial": pruneInitial, "old-theme": pruneOld}, map[string]machine.SnapshotOrigin{
		"old-theme": {Project: busyDir, Scene: "theme", Take: machine.TakeRecording, Image: pruneOld},
	})
	release, err := busyStore.LockMany("demo")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	busyOut, busyErr, err := runPruneFull(t, pruneStatesExec{Store: busyStore}, []string{"--json", "--workspace", busyDir, "demo"})
	if !errors.Is(err, machine.ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
	if !strings.Contains(busyErr, "busy") {
		t.Fatalf("busy stderr %q", busyErr)
	}
	var doc pruneErrorDoc
	decodeOneJSON(t, busyOut, &doc)
	if doc.Stage != "demo" || !strings.Contains(doc.Error, "busy") {
		t.Fatalf("busy JSON: %#v", doc)
	}

	missingWS, missingWSErr, err := runPruneFull(t, pruneStatesExec{Store: store}, []string{"--json", "demo"})
	if err == nil {
		t.Fatal("missing workspace succeeded")
	}
	if !strings.Contains(missingWSErr, "workspace") {
		t.Fatalf("missing workspace stderr %q", missingWSErr)
	}
	var missingDoc pruneErrorDoc
	decodeOneJSON(t, missingWS, &missingDoc)
	if missingDoc.Stage != "demo" || !strings.Contains(missingDoc.Error, "workspace") {
		t.Fatalf("missing workspace JSON: %#v", missingDoc)
	}

	missingStage, missingStageErr, err := runPruneFull(t, pruneStatesExec{Store: store}, []string{"--json", "--workspace", dir})
	if err == nil {
		t.Fatal("missing stage succeeded")
	}
	if !strings.Contains(missingStageErr, "accepts 1 arg") {
		t.Fatalf("missing stage stderr %q", missingStageErr)
	}
	decodeOneJSON(t, missingStage, &missingDoc)
	if !strings.Contains(missingDoc.Error, "accepts 1 arg") {
		t.Fatalf("missing stage JSON: %#v", missingDoc)
	}
}

func TestPruneStatesUncleanOriginIsManual(t *testing.T) {
	dir, store, mgr := pruneWorkspace(t)
	writePruneScene(t, dir, "host", `{"name":"host","layout":"solo","steps":[{"action":"wait","delay-after":0.05}]}`)
	unclean := filepath.Join(dir, "outlnk") + string(filepath.Separator) + ".." + string(filepath.Separator) + "retired2"
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: unclean, Scene: "make", Take: machine.TakeRecording, Image: pruneReady},
	})
	var deleted []string
	out, err := runPrune(t, pruneStatesExec{Store: store, Delete: recordDelete(&deleted, mgr)}, "demo", dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 || !strings.Contains(out, "kept ready (manual)") {
		t.Fatalf("unclean origin: deleted=%v report=%s", deleted, out)
	}
	out, err = runPruneFullOut(t, pruneStatesExec{Store: store, Delete: recordDelete(&deleted, mgr)}, []string{"--include-missing-projects", "--workspace", dir, "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 || !strings.Contains(out, "kept ready (manual)") {
		t.Fatalf("include-missing unclean: deleted=%v report=%s", deleted, out)
	}
}

func TestPruneStatesRelativeOriginIsManual(t *testing.T) {
	dir, store, mgr := pruneWorkspace(t)
	writePruneScene(t, dir, "host", `{"name":"host","layout":"solo","steps":[{"action":"wait","delay-after":0.05}]}`)
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: "en", Scene: "make", Take: machine.TakeRecording, Image: pruneReady},
	})
	var deleted []string
	out, err := runPrune(t, pruneStatesExec{Store: store, Delete: recordDelete(&deleted, mgr)}, "demo", dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 {
		t.Fatalf("deleted %v", deleted)
	}
	if !strings.Contains(out, "kept ready (manual)") {
		t.Fatalf("report: %s", out)
	}
}

func TestPruneStatesMissingProjectDefaultAndFlag(t *testing.T) {
	dir, store, mgr := pruneWorkspace(t)
	writePruneScene(t, dir, "host", `{"name":"host","layout":"solo","steps":[{"action":"wait","delay-after":0.05}]}`)
	gone := filepath.Join(dir, "retired")
	savePruneStage(t, store, map[string]string{"initial": pruneInitial, "ready": pruneReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: gone, Scene: "make", Take: machine.TakeRecording, Image: pruneReady},
	})
	var deleted []string
	out, err := runPrune(t, pruneStatesExec{Store: store, Delete: recordDelete(&deleted, mgr)}, "demo", dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 {
		t.Fatalf("default deleted %v", deleted)
	}
	if !strings.Contains(out, "kept ready (missing-project)") || !strings.Contains(out, missingProjectHint) {
		t.Fatalf("default report: %s", out)
	}
	out, err = runPruneFullOut(t, pruneStatesExec{Store: store, Delete: recordDelete(&deleted, mgr)}, []string{"--include-missing-projects", "--workspace", dir, "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(deleted, []string{"ready"}) {
		t.Fatalf("flag deleted %v", deleted)
	}
	if !strings.Contains(out, "removed ready (undeclared)") {
		t.Fatalf("flag report: %s", out)
	}
}

func conflictPruneWorkspace(t *testing.T) (string, *machine.Store) {
	t.Helper()
	dir, store, _ := pruneWorkspace(t)
	writePruneScene(t, dir, "one", pruneClean("one", "", "ready"))
	writePruneScene(t, dir, "two", pruneClean("two", "", "ready"))
	return dir, store
}

func decodeOneJSON(t *testing.T, raw string, dest any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	if err := dec.Decode(dest); err != nil {
		t.Fatalf("json: %v raw=%s", err, raw)
	}
	if dec.More() {
		t.Fatalf("more than one JSON document: %s", raw)
	}
}

func decodePruneReport(t *testing.T, raw string) pruneReport {
	t.Helper()
	var rawMap map[string]json.RawMessage
	decodeOneJSON(t, raw, &rawMap)
	if _, ok := rawMap["warnings"]; ok {
		t.Fatalf("report must not use warnings: %s", raw)
	}
	if _, ok := rawMap["cleanup-warnings"]; !ok {
		t.Fatalf("report missing cleanup-warnings: %s", raw)
	}
	var warns []string
	if err := json.Unmarshal(rawMap["cleanup-warnings"], &warns); err != nil {
		t.Fatalf("cleanup-warnings must be strings: %v %s", err, raw)
	}
	var report pruneReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func runPrune(t *testing.T, x pruneStatesExec, stage, dir string, dryRun bool) (string, error) {
	t.Helper()
	args := []string{"--workspace", dir, stage}
	if dryRun {
		args = append([]string{"--dry-run"}, args...)
	}
	out, _, err := runPruneFull(t, x, args)
	return out, err
}

func runPruneFullOut(t *testing.T, x pruneStatesExec, args []string) (string, error) {
	t.Helper()
	out, _, err := runPruneFull(t, x, args)
	return out, err
}

func runPruneFull(t *testing.T, x pruneStatesExec, args []string) (string, string, error) {
	t.Helper()
	cmd := pruneStatesCmd(x)
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errBuf.String(), err
}

func containsName(names []string, want string) bool {
	return slices.Contains(names, want)
}

func recordDelete(deleted *[]string, mgr *machine.Manager) pruneDelete {
	return func(r *machine.Record, name string) (string, error) {
		*deleted = append(*deleted, name)
		return mgr.DeleteSnapshot(r, name)
	}
}

func pruneWorkspace(t *testing.T) (string, *machine.Store, *machine.Manager) {
	t.Helper()
	mgr := testStageRegistry(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {"laptop": {"stage": "demo"}}
	}`)
	return dir, mgr.Store, mgr
}

func writePruneScene(t *testing.T, dir, name, body string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "scenes", name+".json"), body)
}

func pruneClean(name, snap, end string) string {
	body := `{"name":"` + name + `","layout":"solo","vm":"laptop","vm-start":{"mode":"clean"`
	if snap != "" {
		body += `,"snapshot":"` + snap + `"`
	}
	body += `}`
	if end != "" {
		body += `,"vm-end":{"snapshot":"` + end + `"}`
	}
	body += `,"steps":[{"action":"wait","delay-after":0.05}]}`
	return body
}

func savePruneStage(t *testing.T, store *machine.Store, snaps map[string]string, origins map[string]machine.SnapshotOrigin) {
	t.Helper()
	r := testStageRecord()
	r.Snapshots = snaps
	r.SnapshotOrigins = origins
	if err := store.Save(r); err != nil {
		t.Fatal(err)
	}
}

func loadPruneStage(t *testing.T, store *machine.Store) *machine.Record {
	t.Helper()
	rec, err := store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func namesFromReport(t *testing.T, out, kind string) string {
	t.Helper()
	var names []string
	prefix := kind + " "
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, prefix))
		if len(fields) == 0 {
			continue
		}
		names = append(names, fields[0])
	}
	return strings.Join(names, ",")
}

func listRegularFiles(t *testing.T, roots ...string) []string {
	t.Helper()
	var files []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(root)+":"+filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(files)
	return files
}
