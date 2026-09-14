package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/machine"
)

func TestPlanTwoProducersInOrder(t *testing.T) {
	dir, store := chainABC(t)
	got := planScene(t, filepath.Join(dir, "scenes", "c.json"), store, KindPlay)
	want := []string{"./a missing", "./b blocked:state-missing", "./c requested"}
	if names := stepSummary(got); !equal(names, want) {
		t.Fatalf("plan %v, want %v", names, want)
	}
}

func TestPlanSkipsOKProducer(t *testing.T) {
	dir, store := chainABC(t)
	saveChain(t, store, dir, true, false)
	publishNamed(t, dir, store, "a", facts.Facts{})
	got := planScene(t, filepath.Join(dir, "scenes", "c.json"), store, KindPlay)
	want := []string{"./b missing", "./c requested"}
	if names := stepSummary(got); !equal(names, want) {
		t.Fatalf("plan %v, want %v", names, want)
	}
}

func TestPlanStaleAndMissingRun(t *testing.T) {
	dir, store := chainABC(t)
	saveChain(t, store, dir, true, false)
	publishNamed(t, dir, store, "a", facts.Facts{InputsSHA256: "not-the-digest"})
	got := planScene(t, filepath.Join(dir, "scenes", "c.json"), store, KindPlay)
	want := []string{"./a stale:inputs", "./b missing", "./c requested"}
	if names := stepSummary(got); !equal(names, want) {
		t.Fatalf("plan %v, want %v", names, want)
	}
}

func TestPlanUpstreamRerunWhenAncestorRuns(t *testing.T) {
	dir, store := chainABC(t)
	saveChain(t, store, dir, true, true)
	publishNamed(t, dir, store, "a", facts.Facts{InputsSHA256: "not-the-digest"})
	publishNamed(t, dir, store, "b", facts.Facts{})
	got := planScene(t, filepath.Join(dir, "scenes", "c.json"), store, KindPlay)
	want := []string{"./a stale:inputs", "./b upstream re-run", "./c requested"}
	if names := stepSummary(got); !equal(names, want) {
		t.Fatalf("plan %v, want %v", names, want)
	}
}

func TestPlanRefusesCycleAndDuplicate(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "loop", cleanScene("loop", "ready", "ready"))
	_, err := PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: filepath.Join(dir, "scenes", "loop.json")})
	var conflict *ConflictError
	if !errors.As(err, &conflict) || len(conflict.Cycles) == 0 {
		t.Fatalf("cycle: %v", err)
	}

	dir = t.TempDir()
	store = testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "one", cleanScene("one", "", "ready"))
	writeScene(t, dir, "two", cleanScene("two", "", "ready"))
	_, err = PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: filepath.Join(dir, "scenes", "one.json")})
	if !errors.As(err, &conflict) || len(conflict.Duplicates) == 0 {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestPlanRefusesBlockedNoProducer(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "use", cleanScene("use", "ghost", ""))
	_, err := PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: filepath.Join(dir, "scenes", "use.json")})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("blocked:no-producer: %v", err)
	}
}

func TestPlanContinueKeepsPredecessorAdjacent(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "live", `{"name":"live","layout":"solo","vm":"laptop","vm-start":{"mode":"clean"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeScene(t, dir, "next", `{"name":"next","layout":"solo","vm":"laptop","vm-start":{"mode":"continue","after":"live"},"vm-end":{"snapshot":"ready"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeScene(t, dir, "use", cleanScene("use", "ready", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial}, nil)
	p, s := load(t, dir, "live")
	rec, _ := store.Load("demo")
	publish(t, p, s, rec, facts.Facts{})
	got := planScene(t, filepath.Join(dir, "scenes", "use.json"), store, KindPlay)
	want := []string{"./live continue session", "./next missing", "./use requested"}
	if names := stepSummary(got); !equal(names, want) {
		t.Fatalf("plan %v, want %v", names, want)
	}
	if got.Steps[0].Scene != "live" || got.Steps[1].Scene != "next" {
		t.Fatalf("continue pair must be adjacent: %v", stepSummary(got))
	}
}

func TestPlanTwoContinuesAfterSameParentRefuse(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "live", `{"name":"live","layout":"solo","vm":"laptop","vm-start":{"mode":"clean"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeScene(t, dir, "b", `{"name":"b","layout":"solo","vm":"laptop","vm-start":{"mode":"continue","after":"live"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeScene(t, dir, "c", `{"name":"c","layout":"solo","vm":"laptop","vm-start":{"mode":"continue","after":"live"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	ev, err := Evaluate(Options{Dir: dir, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	var steps []node
	for _, n := range ev.nodes {
		switch n.scene.Name {
		case "live", "b", "c":
			steps = append(steps, n)
		}
	}
	_, err = arrangeContinues(steps, ev.nodes)
	if err == nil || !strings.Contains(err.Error(), "both continue") {
		t.Fatalf("two continues after live: %v", err)
	}
}

func TestPlanManualStartIsValidRoot(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "use", cleanScene("use", "ready", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, nil)
	got := planScene(t, filepath.Join(dir, "scenes", "use.json"), store, KindPlay)
	if len(got.Steps) != 1 || got.Steps[0].Scene != "use" || got.Steps[0].Reason != ReasonRequested {
		t.Fatalf("manual start should not pull a producer: %v", stepSummary(got))
	}
}

func TestPlanProducerWouldReplaceManual(t *testing.T) {
	dir, store := chainABC(t)
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, nil)
	_, err := PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: filepath.Join(dir, "scenes", "c.json"), Kind: KindPlay})
	if err == nil || !strings.Contains(err.Error(), "play") || !strings.Contains(err.Error(), "--adopt") {
		t.Fatalf("producer replacing manual: %v", err)
	}
	if !strings.Contains(err.Error(), "a") {
		t.Fatalf("must name the producer: %v", err)
	}
	_, err = PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: filepath.Join(dir, "scenes", "c.json"), Kind: KindPlay, Adopt: true})
	if err == nil || !strings.Contains(err.Error(), "--adopt") {
		t.Fatalf("command --adopt must not authorize a producer: %v", err)
	}
}

func TestPlanRequestedAdopt(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "make", cleanScene("make", "", "ready"))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, nil)
	_, err := PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: filepath.Join(dir, "scenes", "make.json"), Kind: KindPlay})
	if err == nil || !strings.Contains(err.Error(), "has no origin") {
		t.Fatalf("requested without adopt: %v", err)
	}
	got, err := PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: filepath.Join(dir, "scenes", "make.json"), Kind: KindPlay, Adopt: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Steps) != 1 || !got.Steps[0].Requested {
		t.Fatalf("adopted requested: %v", stepSummary(got))
	}
	if got.AdoptSnapshot != "ready" {
		t.Fatalf("adopt snapshot %q, want ready", got.AdoptSnapshot)
	}
}

func TestPlanRehearseStopsOnRecordingState(t *testing.T) {
	dir, store := chainABC(t)
	saveChain(t, store, dir, true, false)
	publishNamed(t, dir, store, "a", facts.Facts{InputsSHA256: "not-the-digest"})
	_, err := PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: filepath.Join(dir, "scenes", "c.json"), Kind: KindRehearse})
	if err == nil || !strings.Contains(err.Error(), "--replace-state") {
		t.Fatalf("rehearse without replace-state: %v", err)
	}
	got, err := PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: filepath.Join(dir, "scenes", "c.json"), Kind: KindRehearse, ReplaceState: true})
	if err != nil {
		t.Fatal(err)
	}
	if stepSummary(got)[0] != "./a stale:inputs" {
		t.Fatalf("with flag: %v", stepSummary(got))
	}
}

func TestPlanContinueReordersNonAdjacent(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "live", `{"name":"live","layout":"solo","vm":"laptop","vm-start":{"mode":"clean"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeScene(t, dir, "extra", cleanScene("extra", "", "other"))
	writeScene(t, dir, "next", `{"name":"next","layout":"solo","vm":"laptop","vm-start":{"mode":"continue","after":"live"},"vm-end":{"snapshot":"ready"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	ev, err := Evaluate(Options{Dir: dir, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	live := testNode(t, ev, "live")
	extra := testNode(t, ev, "extra")
	next := testNode(t, ev, "next")
	if live.stage() != extra.stage() || extra.stage() != next.stage() {
		t.Fatalf("need the same stage: %s %s %s", live.stage(), extra.stage(), next.stage())
	}
	got, err := arrangeContinues([]node{live, extra, next}, ev.nodes)
	if err != nil {
		t.Fatal(err)
	}
	i := nodeIndex(got, live.id)
	j := nodeIndex(got, next.id)
	if i < 0 || j != i+1 {
		t.Fatalf("continue pair must become adjacent from non-adjacent order: %v", nodeNames(got))
	}
}

func TestPlanContinueRefusesImpossibleOrder(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "live", `{"name":"live","layout":"solo","vm":"laptop","vm-start":{"mode":"clean"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeScene(t, dir, "next", `{"name":"next","layout":"solo","vm":"laptop","vm-start":{"mode":"continue","after":"live"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	ev, err := Evaluate(Options{Dir: dir, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	live := testNode(t, ev, "live")
	next := testNode(t, ev, "next")
	_, err = arrangeContinues([]node{next, live}, ev.nodes)
	if err == nil || !strings.Contains(err.Error(), "predecessor later") {
		t.Fatalf("impossible continue order: %v", err)
	}
}

func TestPlanUnverifiableDoesNotSeed(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "live", reuseProducer("live", "ready"))
	writeScene(t, dir, "use", cleanScene("use", "ready", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: dir, Scene: "live", Take: machine.TakeRecording, Image: imgReady},
	})
	p, s := load(t, dir, "live")
	rec, _ := store.Load("demo")
	publish(t, p, s, rec, facts.Facts{})
	st := statusOf(t, report(t, dir, store), "live")
	if st.Status != Unverifiable {
		t.Fatalf("live status %s, want %s", st.Status, Unverifiable)
	}
	got := planScene(t, filepath.Join(dir, "scenes", "use.json"), store, KindPlay)
	if len(got.Steps) != 1 || got.Steps[0].Scene != "use" || got.Steps[0].Reason != ReasonRequested {
		t.Fatalf("unverifiable must not seed: %v", stepSummary(got))
	}
}

func TestPlanPlaySeedsRehearsalMade(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "a", cleanScene("a", "", "ready"))
	writeScene(t, dir, "c", cleanScene("c", "ready", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: dir, Scene: "a", Take: machine.TakeRehearsal, Image: imgReady},
	})
	publishNamed(t, dir, store, "a", facts.Facts{})
	st := statusOf(t, report(t, dir, store), "a")
	if st.Status != OK {
		t.Fatalf("producer status %s, want %s", st.Status, OK)
	}
	got := planScene(t, filepath.Join(dir, "scenes", "c.json"), store, KindPlay)
	want := []string{"./a " + BlockedRehearsalState, "./c requested"}
	if names := stepSummary(got); !equal(names, want) {
		t.Fatalf("rehearsal seed %v, want %v", names, want)
	}
}

func TestPlanOriginIsNotAnEdge(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "a", cleanScene("a", "", "ready"))
	writeScene(t, dir, "zz", hostScene("zz"))
	writeScene(t, dir, "b", cleanScene("b", "ready", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: dir, Scene: "zz", Take: machine.TakeRecording, Image: imgReady},
	})
	ev, err := Evaluate(Options{Dir: dir, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	b := testNode(t, ev, "b")
	var preds []string
	for _, p := range ev.predecessors(b) {
		preds = append(preds, p.scene.Name)
	}
	if !contains(preds, "a") || contains(preds, "zz") {
		t.Fatalf("declared producer is the edge, origin is not: %v", preds)
	}
	_, err = PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: filepath.Join(dir, "scenes", "b.json"), Kind: KindPlay})
	if err == nil || !strings.Contains(err.Error(), "belongs to another scene") {
		t.Fatalf("foreign origin: %v", err)
	}
	if !strings.Contains(err.Error(), "a") {
		t.Fatalf("must name the declared producer: %v", err)
	}
	if strings.Contains(err.Error(), "zz") && strings.Contains(err.Error(), "requested") {
		t.Fatalf("origin scene must not be a run step: %v", err)
	}
}

func TestPlanNeedsSceneUnderScenes(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	path := filepath.Join(dir, "other", "use.json")
	writeFile(t, path, cleanScene("use", "", ""))
	_, err := PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: path, Kind: KindPlay})
	if err == nil || !strings.Contains(err.Error(), "--with-deps needs a scene under") || !strings.Contains(err.Error(), filepath.Join(dir, "scenes")) {
		t.Fatalf("outside scenes/: %v", err)
	}
}

func TestPlanDraftsNamesakeIsRefused(t *testing.T) {
	dir, store := chainABC(t)
	draft := filepath.Join(dir, "drafts", "c.json")
	writeFile(t, draft, cleanScene("c", "done", ""))
	_, err := PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: draft, Kind: KindPlay})
	if err == nil || !strings.Contains(err.Error(), "--with-deps needs a scene under") {
		t.Fatalf("drafts/c.json must not plan scenes/c.json: %v", err)
	}
}

func TestPlanDuplicateSceneNameRefuses(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "c", cleanScene("c", "", ""))
	writeFile(t, filepath.Join(dir, "scenes", "c-copy.json"), cleanScene("c", "", ""))
	_, err := PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: filepath.Join(dir, "scenes", "c-copy.json"), Kind: KindPlay})
	if err == nil || !strings.Contains(err.Error(), "duplicate scene name c") || !strings.Contains(err.Error(), "c-copy.json") {
		t.Fatalf("duplicate name: %v", err)
	}
}

func TestPlanCopyUsesFilePathNotName(t *testing.T) {
	dir, store := chainABC(t)
	copyPath := filepath.Join(dir, "c-copy.json")
	writeFile(t, copyPath, cleanScene("c", "done", ""))
	_, err := PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: copyPath, Kind: KindPlay})
	if err == nil || !strings.Contains(err.Error(), "--with-deps needs a scene under") {
		t.Fatalf("c-copy.json must not plan scenes/c.json: %v", err)
	}
}

func TestPlanOriginSceneErrorIsChainError(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "a", cleanScene("a", "", "ready"))
	writeFile(t, filepath.Join(dir, "scenes", "zz.json"), "{")
	writeScene(t, dir, "b", cleanScene("b", "ready", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: dir, Scene: "zz", Take: machine.TakeRecording, Image: imgReady},
	})
	_, err := PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: filepath.Join(dir, "scenes", "b.json"), Kind: KindPlay})
	var cerr *ChainError
	if !errors.As(err, &cerr) || !strings.Contains(err.Error(), "zz") {
		t.Fatalf("origin scene error must be on the chain: %v", err)
	}
}

func TestPlanReplaceSameOwnerViaSymlink(t *testing.T) {
	real := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, real)
	writeScene(t, real, "a", cleanScene("a", "", "ready"))
	writeScene(t, real, "c", cleanScene("c", "ready", ""))
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		resolved = real
	}
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: resolved, Scene: "a", Take: machine.TakeRecording, Image: imgReady},
	})
	link := filepath.Join(t.TempDir(), "proj")
	if err := os.Symlink(resolved, link); err != nil {
		t.Fatal(err)
	}
	ev, err := Evaluate(Options{Dir: real, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	for i := range ev.nodes {
		if ev.nodes[i].project != nil {
			ev.nodes[i].project.Dir = link
		}
	}
	if _, err := ev.plan(testNode(t, ev, "c"), DepsOptions{Kind: KindPlay}); err != nil {
		t.Fatalf("same owner via symlink: %v", err)
	}
}

func TestPlanBlockedNoProducerIncludesConfigFaults(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	en := filepath.Join(root, "en")
	pt := filepath.Join(root, "pt")
	writeFile(t, filepath.Join(en, "backstage.json"), `{
		"extends": "../backstage.json",
		"record": {"out": "../escape"}
	}`)
	writeFile(t, filepath.Join(pt, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeScene(t, pt, "use", cleanScene("use", "ghost", ""))
	_, err := PlanDeps(DepsOptions{Options: Options{Dir: pt, Store: store}, ScenePath: filepath.Join(pt, "scenes", "use.json")})
	var cerr *ChainError
	if !errors.As(err, &cerr) {
		t.Fatalf("blocked with config fault: %v", err)
	}
	text := err.Error()
	if !strings.Contains(text, "does not exist") {
		t.Fatalf("must list the blocked scene: %v", err)
	}
	if !strings.Contains(text, "record.out") && !strings.Contains(text, "en") {
		t.Fatalf("must list the config fault as a possible cause: %v", err)
	}
}

func TestPlanChainErrorRefuses(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	en := filepath.Join(root, "en")
	pt := filepath.Join(root, "pt")
	writeFile(t, filepath.Join(en, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeFile(t, filepath.Join(pt, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeScene(t, en, "make", badLayoutProducer("make", "ready"))
	writeScene(t, pt, "use", cleanScene("use", "ready", ""))
	_, err := PlanDeps(DepsOptions{Options: Options{Dir: pt, Store: store}, ScenePath: filepath.Join(pt, "scenes", "use.json")})
	var cerr *ChainError
	if !errors.As(err, &cerr) || len(cerr.Errors) == 0 {
		t.Fatalf("chain error: %v", err)
	}
}

func TestPlanOffChainErrorIsWarning(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	en := filepath.Join(root, "en")
	fr := filepath.Join(root, "fr")
	pt := filepath.Join(root, "pt")
	writeFile(t, filepath.Join(en, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeFile(t, filepath.Join(fr, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeFile(t, filepath.Join(pt, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeScene(t, en, "make", cleanScene("make", "", "ready"))
	writeFile(t, filepath.Join(fr, "scenes", "other.json"), "{")
	writeScene(t, pt, "use", cleanScene("use", "ready", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: en, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
	})
	publishNamed(t, en, store, "make", facts.Facts{})
	got := planScene(t, filepath.Join(pt, "scenes", "use.json"), store, KindPlay)
	if len(got.Steps) != 1 || got.Steps[0].Scene != "use" {
		t.Fatalf("off-chain error must not abort: %v", stepSummary(got))
	}
	found := false
	for _, w := range got.Warnings {
		if strings.Contains(w.Error, "json") || strings.Contains(w.Path, "other") {
			found = true
		}
	}
	if !found {
		t.Fatalf("off-chain error should be a warning: %+v", got.Warnings)
	}
}

func chainABC(t *testing.T) (string, *machine.Store) {
	t.Helper()
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "a", cleanScene("a", "", "ready"))
	writeScene(t, dir, "b", cleanScene("b", "ready", "done"))
	writeScene(t, dir, "c", cleanScene("c", "done", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial}, nil)
	return dir, store
}

func saveChain(t *testing.T, store *machine.Store, dir string, ready, done bool) {
	t.Helper()
	snaps := map[string]string{"initial": imgInitial}
	origins := map[string]machine.SnapshotOrigin{}
	if ready {
		snaps["ready"] = imgReady
		origins["ready"] = machine.SnapshotOrigin{Project: dir, Scene: "a", Take: machine.TakeRecording, Image: imgReady}
	}
	if done {
		snaps["done"] = imgOther
		origins["done"] = machine.SnapshotOrigin{Project: dir, Scene: "b", Take: machine.TakeRecording, Image: imgOther}
	}
	saveStage(t, store, "demo", snaps, origins)
}

func publishNamed(t *testing.T, dir string, store *machine.Store, name string, f facts.Facts) {
	t.Helper()
	p, s := load(t, dir, name)
	rec, _ := store.Load("demo")
	publish(t, p, s, rec, f)
}

func testNode(t *testing.T, ev *Evaluation, name string) node {
	t.Helper()
	for _, n := range ev.nodes {
		if n.scene != nil && n.scene.Name == name {
			return n
		}
	}
	t.Fatalf("node %s not found", name)
	return node{}
}

func nodeNames(steps []node) []string {
	var out []string
	for _, n := range steps {
		out = append(out, n.scene.Name)
	}
	return out
}

func planScene(t *testing.T, path string, store *machine.Store, kind Kind) *Plan {
	t.Helper()
	got, err := PlanDeps(DepsOptions{Options: Options{Dir: path, Store: store}, ScenePath: path, Kind: kind})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func stepSummary(p *Plan) []string {
	var out []string
	for _, s := range p.Steps {
		out = append(out, s.ID+" "+s.Reason)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
