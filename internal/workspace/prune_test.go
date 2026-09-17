package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/machine"
)

func TestPlanPruneRenamedSceneLeavesOldState(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	writeScene(t, root, "setup", cleanScene("setup", "", "ready"))
	saveStage(t, store, "demo", map[string]string{
		"initial": imgInitial, "old-theme": imgOther, "ready": imgReady,
	}, map[string]machine.SnapshotOrigin{
		"old-theme": {Project: root, Scene: "theme", Take: machine.TakeRecording, Image: imgOther},
		"ready":     {Project: root, Scene: "setup", Take: machine.TakeRecording, Image: imgReady},
	})
	plan := planPrune(t, root, store, "demo")
	if !hasDecision(plan.Remove, "old-theme", PruneUndeclared) {
		t.Fatalf("old state must be undeclared: %+v", plan)
	}
	if hasSnapshot(plan.Remove, "ready") || hasSnapshot(plan.Keep, "ready") {
		t.Fatalf("current producer is not a prune decision: %+v", plan)
	}
}

func TestPlanPruneKeepsInitialManualOutsideConsumedAndInUse(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	writeScene(t, root, "use", cleanScene("use", "held", ""))
	saveStage(t, store, "demo", map[string]string{
		"initial": imgInitial,
		"hand":    "dddddddddddddddddddddddddddddddd",
		"foreign": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		"held":    imgReady,
		"live":    imgOther,
	}, map[string]machine.SnapshotOrigin{
		"foreign": {Project: "/other/workspace/proj", Scene: "make", Take: machine.TakeRecording, Image: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"},
		"held":    {Project: root, Scene: "gone", Take: machine.TakeRecording, Image: imgReady},
		"live":    {Project: root, Scene: "gone", Take: machine.TakeRecording, Image: imgOther},
	})
	rec := loadStage(t, store, "demo")
	rec.Source.Image = imgOther
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}
	plan := planPrune(t, root, store, "demo")
	wantKeep := map[string]string{
		"initial": PruneInitial,
		"hand":    PruneManual,
		"foreign": PruneOutside,
		"held":    PruneConsumed,
		"live":    PruneInUse,
	}
	if len(plan.Remove) != 0 {
		t.Fatalf("protected snapshots were candidates: %+v", plan.Remove)
	}
	if len(plan.Keep) != len(wantKeep) {
		t.Fatalf("kept %+v", plan.Keep)
	}
	for _, d := range plan.Keep {
		if wantKeep[d.Snapshot] != d.Reason {
			t.Fatalf("%s: %s, want %s", d.Snapshot, d.Reason, wantKeep[d.Snapshot])
		}
	}
}

func TestPlanPruneUnreadableOriginIsManual(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	writeScene(t, root, "host", hostScene("host"))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "broken": imgReady}, map[string]machine.SnapshotOrigin{
		"broken": {Scene: "gone", Take: machine.TakeRecording, Image: imgReady},
	})
	plan := planPrune(t, root, store, "demo")
	if !hasDecision(plan.Keep, "broken", PruneManual) {
		t.Fatalf("empty origin project is unreadable: %+v", plan)
	}
}

func TestPlanPruneDeletedProjectUnderRootIsInside(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	writeScene(t, root, "host", hostScene("host"))
	gone := filepath.Join(root, "retired")
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: gone, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
	})
	plan := planPrune(t, root, store, "demo")
	if !hasDecision(plan.Keep, "ready", PruneMissingProject) {
		t.Fatalf("deleted project is missing-project by default: %+v", plan)
	}
	withFlag := planPruneOpts(t, root, store, "demo", PruneOptions{IncludeMissingProjects: true})
	if !hasDecision(withFlag.Remove, "ready", PruneUndeclared) {
		t.Fatalf("include-missing-projects removes a gone project: %+v", withFlag)
	}
}

func TestPlanPruneRelativeOriginIsManual(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	writeScene(t, root, "host", hostScene("host"))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: "en", Scene: "make", Take: machine.TakeRecording, Image: imgReady},
	})
	plan := planPrune(t, root, store, "demo")
	if !hasDecision(plan.Keep, "ready", PruneManual) {
		t.Fatalf("relative origin is unreadable: %+v", plan)
	}
}

func TestPlanPruneUncleanOriginIsManual(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	writeScene(t, root, "host", hostScene("host"))
	outside := t.TempDir()
	outlnk := filepath.Join(root, "outlnk")
	if err := os.Symlink(outside, outlnk); err != nil {
		t.Fatal(err)
	}
	unclean := filepath.Join(root, "outlnk") + string(filepath.Separator) + ".." + string(filepath.Separator) + "retired2"
	if filepath.Clean(unclean) == unclean {
		t.Fatalf("fixture is already clean: %s", unclean)
	}
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: unclean, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
	})
	plan := planPrune(t, root, store, "demo")
	if !hasDecision(plan.Keep, "ready", PruneManual) {
		t.Fatalf("unclean origin is unreadable: %+v", plan)
	}
	withFlag := planPruneOpts(t, root, store, "demo", PruneOptions{IncludeMissingProjects: true})
	if !hasDecision(withFlag.Keep, "ready", PruneManual) {
		t.Fatalf("include-missing-projects must not clean .. into a candidate: %+v", withFlag)
	}
}

func TestPlanPruneMissingBehindHiddenSymlinkRecordOutIsOutside(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	writeScene(t, root, "host", hostScene("host"))
	hidden := filepath.Join(root, ".stash", "retired")
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	throughLink := filepath.Join(alias, "retired")
	recordOut := filepath.Join(root, "recordings", "retired")
	saveStage(t, store, "demo", map[string]string{
		"initial": imgInitial,
		"hidden":  imgReady,
		"linked":  imgOther,
		"cached":  "dddddddddddddddddddddddddddddddd",
	}, map[string]machine.SnapshotOrigin{
		"hidden": {Project: hidden, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
		"linked": {Project: throughLink, Scene: "make", Take: machine.TakeRecording, Image: imgOther},
		"cached": {Project: recordOut, Scene: "make", Take: machine.TakeRecording, Image: "dddddddddddddddddddddddddddddddd"},
	})
	plan := planPruneOpts(t, root, store, "demo", PruneOptions{IncludeMissingProjects: true})
	for _, name := range []string{"hidden", "linked", "cached"} {
		if !hasDecision(plan.Keep, name, PruneOutside) {
			t.Fatalf("%s behind a skipped component must stay outside: %+v", name, plan)
		}
	}
}

func TestPlanPruneHiddenProjectIsOutside(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	writeScene(t, root, "host", hostScene("host"))
	drafts := filepath.Join(root, ".drafts")
	writeFile(t, filepath.Join(drafts, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeScene(t, drafts, "make", cleanScene("make", "", "ready"))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: drafts, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
	})
	plan := planPrune(t, root, store, "demo")
	if !hasDecision(plan.Keep, "ready", PruneOutside) {
		t.Fatalf("hidden project must stay outside-workspace: %+v", plan)
	}
}

func TestPlanPruneNestedWorkspaceBothDirections(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	writeScene(t, root, "host", hostScene("host"))
	other := filepath.Join(root, "other")
	writeBaseProject(t, other)
	writeScene(t, other, "make", cleanScene("make", "", "ready"))
	retired := filepath.Join(other, "retired")
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady, "old": imgOther}, map[string]machine.SnapshotOrigin{
		"ready": {Project: other, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
		"old":   {Project: retired, Scene: "theme", Take: machine.TakeRecording, Image: imgOther},
	})
	fromRoot := planPrune(t, root, store, "demo")
	if !hasDecision(fromRoot.Keep, "ready", PruneOutside) || !hasDecision(fromRoot.Keep, "old", PruneOutside) {
		t.Fatalf("nested workspace from root must stay outside: %+v", fromRoot)
	}
	fromOther := planPrune(t, other, store, "demo")
	if hasSnapshot(fromOther.Remove, "ready") || hasSnapshot(fromOther.Keep, "ready") {
		t.Fatalf("declared producer from its own root is not a prune decision: %+v", fromOther)
	}
	if !hasDecision(fromOther.Keep, "old", PruneMissingProject) {
		t.Fatalf("deleted project under the nested root is missing-project by default: %+v", fromOther)
	}
	fromOtherFlag := planPruneOpts(t, other, store, "demo", PruneOptions{IncludeMissingProjects: true})
	if !hasDecision(fromOtherFlag.Remove, "old", PruneUndeclared) {
		t.Fatalf("include-missing-projects removes it from its own root: %+v", fromOtherFlag)
	}
}

func TestPlanPruneUnreadableConfigAboveIsOutside(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	writeScene(t, root, "host", hostScene("host"))
	mid := filepath.Join(root, "mid")
	writeFile(t, filepath.Join(mid, "backstage.json"), `{`)
	gone := filepath.Join(mid, "retired")
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: gone, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
	})
	plan := planPrune(t, root, store, "demo")
	if !hasDecision(plan.Keep, "ready", PruneOutside) {
		t.Fatalf("unreadable config above a deleted project is outside: %+v", plan)
	}
}

func TestPlanPruneTwoLeavesShareTheStage(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	en := filepath.Join(root, "en")
	pt := filepath.Join(root, "pt")
	writeFile(t, filepath.Join(en, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeFile(t, filepath.Join(pt, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeScene(t, en, "make", cleanScene("make", "", "ready"))
	writeScene(t, pt, "use", cleanScene("use", "ready", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady, "old": imgOther}, map[string]machine.SnapshotOrigin{
		"ready": {Project: en, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
		"old":   {Project: en, Scene: "theme", Take: machine.TakeRecording, Image: imgOther},
	})
	fromPT := planPrune(t, pt, store, "demo")
	if hasSnapshot(fromPT.Remove, "ready") {
		t.Fatalf("DIR must not hide the sibling producer: %+v", fromPT)
	}
	if !hasDecision(fromPT.Remove, "old", PruneUndeclared) {
		t.Fatalf("orphaned state is still a candidate from a leaf: %+v", fromPT)
	}

	writeScene(t, en, "make", hostScene("make"))
	after := planPrune(t, en, store, "demo")
	if !hasDecision(after.Keep, "ready", PruneConsumed) {
		t.Fatalf("consumer on the other leaf keeps the state: %+v", after)
	}
}

func planPrune(t *testing.T, dir string, store *machine.Store, stage string) PrunePlan {
	t.Helper()
	return planPruneOpts(t, dir, store, stage, PruneOptions{})
}

func planPruneOpts(t *testing.T, dir string, store *machine.Store, stage string, opts PruneOptions) PrunePlan {
	t.Helper()
	ev, err := Evaluate(Options{Dir: dir, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	res, err := ev.Result()
	if err != nil {
		t.Fatal(err)
	}
	if res.HasError() {
		t.Fatalf("workspace errors: %+v", res.Errors)
	}
	return ev.PlanPrune(stage, loadStage(t, store, stage), opts)
}

func loadStage(t *testing.T, store *machine.Store, name string) *machine.Record {
	t.Helper()
	rec, err := store.Load(name)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func hasDecision(list []PruneDecision, snapshot, reason string) bool {
	for _, d := range list {
		if d.Snapshot == snapshot && d.Reason == reason {
			return true
		}
	}
	return false
}

func hasSnapshot(list []PruneDecision, snapshot string) bool {
	for _, d := range list {
		if d.Snapshot == snapshot {
			return true
		}
	}
	return false
}
