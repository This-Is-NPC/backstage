package workspace

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/machine"
)

func writeGroupProject(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {
			"laptop": {"stage": "house-laptop"},
			"server": {"stage": "house-server"}
		},
		"state-groups": {"household": ["laptop", "server"]}
	}`)
}

func groupScene(name, vm, start, end, group string) string {
	body := `{"name":"` + name + `","layout":"solo","vm":"` + vm + `","vm-start":{"mode":"clean"`
	if start != "" {
		body += `,"snapshot":"` + start + `"`
	}
	if group != "" && start != "" {
		body += `,"group":"` + group + `"`
	}
	body += `}`
	if end != "" {
		body += `,"vm-end":{"snapshot":"` + end + `"`
		if group != "" {
			body += `,"group":"` + group + `"`
		}
		body += `}`
	}
	body += `,"steps":[{"action":"wait","delay-after":0.05}]}`
	return body
}

func groupOrigin(dir, scene, gen, take string) machine.SnapshotOrigin {
	return machine.SnapshotOrigin{
		Project: dir, Scene: scene, Take: take, Image: imgReady,
		Group: "household", Generation: gen,
	}
}

func TestGroupConsumerStatusAndJSON(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeGroupProject(t, dir)
	writeScene(t, dir, "use", groupScene("use", "laptop", "linked", "", "household"))
	saveStage(t, store, "house-laptop", map[string]string{"initial": imgInitial, "linked": imgReady}, map[string]machine.SnapshotOrigin{
		"linked": groupOrigin(dir, "make-laptop", "gen-a", machine.TakeRecording),
	})
	saveStage(t, store, "house-server", map[string]string{"initial": imgInitial, "linked": imgOther}, map[string]machine.SnapshotOrigin{
		"linked": {Project: dir, Scene: "make-server", Take: machine.TakeRecording, Image: imgOther, Group: "household", Generation: "gen-a"},
	})
	st := statusOf(t, report(t, dir, store), "use")
	if st.Status != Missing {
		t.Fatalf("complete group: %s %v", st.Status, st.Reasons)
	}
	if st.Group != "household" || st.Generation != "gen-a" || st.GroupMember != "" {
		t.Fatalf("json fields: %+v", st)
	}
	if st.Stage != "house-laptop" || st.StartSnapshot != "linked" || len(st.Reasons) == 0 {
		t.Fatalf("old fields dropped: %+v", st)
	}
	body, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"status"`, `"reasons"`, `"stage"`, `"group"`, `"generation"`} {
		if !strings.Contains(string(body), key) {
			t.Fatalf("json missing %s: %s", key, body)
		}
	}
	if strings.Contains(string(body), `"group-member"`) {
		t.Fatalf("empty group-member should omit: %s", body)
	}
}

func TestGroupIncompleteNamesSilentMember(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeGroupProject(t, dir)
	writeScene(t, dir, "use", groupScene("use", "laptop", "linked", "", "household"))
	saveStage(t, store, "house-laptop", map[string]string{"initial": imgInitial, "linked": imgReady}, map[string]machine.SnapshotOrigin{
		"linked": groupOrigin(dir, "make-laptop", "gen-a", machine.TakeRecording),
	})
	saveStage(t, store, "house-server", map[string]string{"initial": imgInitial}, nil)
	st := statusOf(t, report(t, dir, store), "use")
	if st.Status != BlockedGroupIncomplete || st.GroupMember != "server" {
		t.Fatalf("incomplete: %+v", st)
	}
	if !strings.Contains(st.Detail, "server") || !strings.Contains(st.Detail, "house-server") {
		t.Fatalf("detail: %s", st.Detail)
	}
}

func TestGroupPlayRefusesSilentRehearsal(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeGroupProject(t, dir)
	writeScene(t, dir, "use", groupScene("use", "laptop", "linked", "", "household"))
	saveStage(t, store, "house-laptop", map[string]string{"initial": imgInitial, "linked": imgReady}, map[string]machine.SnapshotOrigin{
		"linked": groupOrigin(dir, "make-laptop", "gen-a", machine.TakeRecording),
	})
	saveStage(t, store, "house-server", map[string]string{"initial": imgInitial, "linked": imgOther}, map[string]machine.SnapshotOrigin{
		"linked": {Project: dir, Scene: "make-server", Take: machine.TakeRehearsal, Image: imgOther, Group: "household", Generation: "gen-a"},
	})
	st := statusOf(t, report(t, dir, store), "use")
	if st.Status != BlockedRehearsalState || st.GroupMember != "server" {
		t.Fatalf("silent rehearsal: %+v", st)
	}
	if !strings.Contains(st.Detail, "server") {
		t.Fatalf("detail: %s", st.Detail)
	}
}

func TestGroupIncompleteAfterRehearsalInPrecedence(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeGroupProject(t, dir)
	// Film server so the missing silent (laptop) is visited first and both
	// reasons apply. rehearsal-state must still win.
	writeScene(t, dir, "use", groupScene("use", "server", "linked", "", "household"))
	saveStage(t, store, "house-server", map[string]string{"initial": imgInitial, "linked": imgReady}, map[string]machine.SnapshotOrigin{
		"linked": groupOrigin(dir, "make-server", "gen-a", machine.TakeRehearsal),
	})
	saveStage(t, store, "house-laptop", map[string]string{"initial": imgInitial}, nil)
	st := statusOf(t, report(t, dir, store), "use")
	if st.Status != BlockedRehearsalState {
		t.Fatalf("rehearsal must beat group-incomplete: %s reasons %v", st.Status, st.Reasons)
	}
	if !contains(st.Reasons, BlockedRehearsalState) || !contains(st.Reasons, BlockedGroupIncomplete) {
		t.Fatalf("both reasons: %v", st.Reasons)
	}
	if !strings.Contains(st.Detail, "laptop") || !strings.Contains(st.Detail, "server") {
		t.Fatalf("detail must name every member: %s", st.Detail)
	}
	if st.GroupMember != "server" {
		t.Fatalf("winner: %s", st.GroupMember)
	}
}

func TestGroupDivergentGenerationIsIncomplete(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeGroupProject(t, dir)
	writeScene(t, dir, "use", groupScene("use", "laptop", "linked", "", "household"))
	saveStage(t, store, "house-laptop", map[string]string{"initial": imgInitial, "linked": imgReady}, map[string]machine.SnapshotOrigin{
		"linked": groupOrigin(dir, "make-laptop", "fresh-gen", machine.TakeRecording),
	})
	saveStage(t, store, "house-server", map[string]string{"initial": imgInitial, "linked": imgOther}, map[string]machine.SnapshotOrigin{
		"linked": {Project: dir, Scene: "make-server", Take: machine.TakeRecording, Image: imgOther, Group: "household", Generation: "old-gen"},
	})
	st := statusOf(t, report(t, dir, store), "use")
	if st.Status != BlockedGroupIncomplete {
		t.Fatalf("divergent: %+v", st)
	}
}

func TestGroupWithDepsPullsEveryMemberProducer(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeGroupProject(t, dir)
	writeScene(t, dir, "make-laptop", groupScene("make-laptop", "laptop", "", "linked", "household"))
	writeScene(t, dir, "make-server", groupScene("make-server", "server", "", "linked", "household"))
	writeScene(t, dir, "use", groupScene("use", "laptop", "linked", "", "household"))
	saveStage(t, store, "house-laptop", map[string]string{"initial": imgInitial}, nil)
	saveStage(t, store, "house-server", map[string]string{"initial": imgInitial}, nil)
	got := planScene(t, filepath.Join(dir, "scenes", "use.json"), store, KindPlay)
	want := []string{"./make-laptop missing", "./make-server missing", "./use requested"}
	if names := stepSummary(got); !equal(names, want) {
		t.Fatalf("plan %v, want %v", names, want)
	}
	if !contains(got.Stages, "house-laptop") || !contains(got.Stages, "house-server") {
		t.Fatalf("reserved stages: %v", got.Stages)
	}
}

func TestPlanPruneKeepsMemberConsumedByOtherStage(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeGroupProject(t, root)
	writeScene(t, root, "use", groupScene("use", "laptop", "linked", "", "household"))
	saveStage(t, store, "house-laptop", map[string]string{"initial": imgInitial, "linked": imgReady}, map[string]machine.SnapshotOrigin{
		"linked": groupOrigin(root, "gone-laptop", "gen-a", machine.TakeRecording),
	})
	saveStage(t, store, "house-server", map[string]string{"initial": imgInitial, "linked": imgOther}, map[string]machine.SnapshotOrigin{
		"linked": {Project: root, Scene: "gone-server", Take: machine.TakeRecording, Image: imgOther, Group: "household", Generation: "gen-a"},
	})
	plan := planPrune(t, root, store, "house-server")
	if !hasDecision(plan.Keep, "linked", PruneConsumed) {
		t.Fatalf("group consumer on another stage must keep the member: %+v", plan)
	}
	if hasSnapshot(plan.Remove, "linked") {
		t.Fatalf("removed a consumed member: %+v", plan)
	}
}

func publishGroupProducer(t *testing.T, dir string, store *machine.Store, name, stage string, f facts.Facts) {
	t.Helper()
	p, s := load(t, dir, name)
	rec, err := store.Load(stage)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, p, s, rec, f)
}

func writeGroupProducers(t *testing.T, dir string, store *machine.Store) {
	t.Helper()
	writeGroupProject(t, dir)
	writeScene(t, dir, "make-laptop", groupScene("make-laptop", "laptop", "", "linked", "household"))
	writeScene(t, dir, "make-server", groupScene("make-server", "server", "", "linked", "household"))
	writeScene(t, dir, "use", groupScene("use", "laptop", "linked", "", "household"))
	saveStage(t, store, "house-laptop", map[string]string{"initial": imgInitial, "linked": imgReady}, map[string]machine.SnapshotOrigin{
		"linked": groupOrigin(dir, "make-laptop", "gen-1", machine.TakeRecording),
	})
	saveStage(t, store, "house-server", map[string]string{"initial": imgInitial, "linked": imgOther}, map[string]machine.SnapshotOrigin{
		"linked": {Project: dir, Scene: "make-server", Take: machine.TakeRecording, Image: imgOther, Group: "household", Generation: "gen-1"},
	})
}

func TestGroupSiblingSelectedWhenMemberStale(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeGroupProducers(t, dir, store)
	publishGroupProducer(t, dir, store, "make-laptop", "house-laptop", facts.Facts{InputsSHA256: "not-the-digest"})
	publishGroupProducer(t, dir, store, "make-server", "house-server", facts.Facts{})
	got := planScene(t, filepath.Join(dir, "scenes", "use.json"), store, KindPlay)
	want := []string{"./make-laptop stale:inputs", "./make-server group-sibling", "./use requested"}
	if names := stepSummary(got); !equal(names, want) {
		t.Fatalf("plan %v, want %v", names, want)
	}
	if !contains(got.Stages, "house-laptop") || !contains(got.Stages, "house-server") {
		t.Fatalf("reserved stages: %v", got.Stages)
	}
}

func TestPlanStaleSelectsGroupSibling(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeGroupProducers(t, dir, store)
	publishGroupProducer(t, dir, store, "make-laptop", "house-laptop", facts.Facts{InputsSHA256: "not-the-digest"})
	publishGroupProducer(t, dir, store, "make-server", "house-server", facts.Facts{})
	got, err := PlanStale(DepsOptions{Options: Options{Dir: dir, Store: store}, Kind: KindPlay})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]string{}
	for _, s := range got.Steps {
		byID[s.ID] = s.Reason
	}
	if byID["./make-laptop"] != StaleInputs || byID["./make-server"] != ReasonGroupSibling {
		t.Fatalf("stale plan: %v", stepSummary(got))
	}
	if byID["./use"] == "" {
		t.Fatalf("use missing from stale plan: %v", stepSummary(got))
	}
}

func TestPlanStagesIncludeSilentMemberWhenOnlyConsumerRuns(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeGroupProducers(t, dir, store)
	publishGroupProducer(t, dir, store, "make-laptop", "house-laptop", facts.Facts{})
	publishGroupProducer(t, dir, store, "make-server", "house-server", facts.Facts{})
	got := planScene(t, filepath.Join(dir, "scenes", "use.json"), store, KindPlay)
	want := []string{"./use requested"}
	if names := stepSummary(got); !equal(names, want) {
		t.Fatalf("plan %v, want %v", names, want)
	}
	if !contains(got.Stages, "house-laptop") || !contains(got.Stages, "house-server") {
		t.Fatalf("silent member stage missing: %v", got.Stages)
	}
}

func writeGhostSiblingProject(t *testing.T, dir string, store *machine.Store) {
	t.Helper()
	writeGroupProject(t, dir)
	writeScene(t, dir, "make-laptop", groupScene("make-laptop", "laptop", "", "linked", "household"))
	writeScene(t, dir, "make-server", `{"name":"make-server","layout":"solo","vm":"server","vm-start":{"mode":"clean","snapshot":"ghost"},"vm-end":{"snapshot":"linked","group":"household"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeScene(t, dir, "view", groupScene("view", "laptop", "linked", "", ""))
	saveStage(t, store, "house-laptop", map[string]string{"initial": imgInitial, "linked": imgReady}, map[string]machine.SnapshotOrigin{
		"linked": groupOrigin(dir, "make-laptop", "gen-1", machine.TakeRecording),
	})
	saveStage(t, store, "house-server", map[string]string{"initial": imgInitial}, nil)
	publishGroupProducer(t, dir, store, "make-laptop", "house-laptop", facts.Facts{InputsSHA256: "not-the-digest"})
}

func TestGroupSiblingNoProducerRefusesWithDeps(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeGhostSiblingProject(t, dir, store)
	_, err := PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: filepath.Join(dir, "scenes", "view.json"), Kind: KindPlay})
	if err == nil || !strings.Contains(err.Error(), "make-server") || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("sibling no-producer: %v", err)
	}
}

func TestGroupSiblingNoProducerRefusesStale(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeGhostSiblingProject(t, dir, store)
	_, err := PlanStale(DepsOptions{Options: Options{Dir: dir, Store: store}, Kind: KindPlay})
	if err == nil || !strings.Contains(err.Error(), "make-server") || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("stale sibling no-producer: %v", err)
	}
}

func TestGroupSiblingsStayInSameProject(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeFile(t, filepath.Join(root, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {
			"laptop": {"stage": "root-laptop"},
			"server": {"stage": "root-server"}
		},
		"state-groups": {"household": ["laptop", "server"]}
	}`)
	en := filepath.Join(root, "en")
	pt := filepath.Join(root, "pt")
	writeFile(t, filepath.Join(en, "backstage.json"), `{
		"extends": "../backstage.json",
		"vms": {
			"laptop": {"stage": "en-laptop"},
			"server": {"stage": "en-server"}
		}
	}`)
	writeFile(t, filepath.Join(pt, "backstage.json"), `{
		"extends": "../backstage.json",
		"vms": {
			"laptop": {"stage": "pt-laptop"},
			"server": {"stage": "pt-server"}
		}
	}`)
	for _, leaf := range []string{en, pt} {
		writeScene(t, leaf, "make-laptop", groupScene("make-laptop", "laptop", "", "linked", "household"))
		writeScene(t, leaf, "make-server", groupScene("make-server", "server", "", "linked", "household"))
		writeScene(t, leaf, "use", groupScene("use", "laptop", "linked", "", "household"))
	}
	saveStage(t, store, "en-laptop", map[string]string{"initial": imgInitial, "linked": imgReady}, map[string]machine.SnapshotOrigin{
		"linked": groupOrigin(en, "make-laptop", "gen-1", machine.TakeRecording),
	})
	saveStage(t, store, "en-server", map[string]string{"initial": imgInitial, "linked": imgOther}, map[string]machine.SnapshotOrigin{
		"linked": {Project: en, Scene: "make-server", Take: machine.TakeRecording, Image: imgOther, Group: "household", Generation: "gen-1"},
	})
	saveStage(t, store, "pt-laptop", map[string]string{"initial": imgInitial}, nil)
	saveStage(t, store, "pt-server", map[string]string{"initial": imgInitial}, nil)
	publishGroupProducer(t, en, store, "make-laptop", "en-laptop", facts.Facts{InputsSHA256: "not-the-digest"})
	publishGroupProducer(t, en, store, "make-server", "en-server", facts.Facts{})
	got := planScene(t, filepath.Join(en, "scenes", "use.json"), store, KindPlay)
	var ids []string
	for _, s := range got.Steps {
		ids = append(ids, s.ID)
		if strings.HasPrefix(s.ID, "pt/") {
			t.Fatalf("crossed project: %v", stepSummary(got))
		}
	}
	want := []string{"en/make-laptop stale:inputs", "en/make-server group-sibling", "en/use requested"}
	if names := stepSummary(got); !equal(names, want) {
		t.Fatalf("plan %v, want %v", names, want)
	}
	if ids[len(ids)-1] != "en/use" {
		t.Fatalf("requested must be last: %v", ids)
	}
}

func writeSiblingProducerProject(t *testing.T, dir string, store *machine.Store) {
	t.Helper()
	writeGroupProject(t, dir)
	writeScene(t, dir, "prep-server", groupScene("prep-server", "server", "", "base", ""))
	writeScene(t, dir, "make-laptop", groupScene("make-laptop", "laptop", "", "linked", "household"))
	writeScene(t, dir, "make-server", `{"name":"make-server","layout":"solo","vm":"server","vm-start":{"mode":"clean","snapshot":"base"},"vm-end":{"snapshot":"linked","group":"household"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeScene(t, dir, "view", groupScene("view", "laptop", "linked", "", ""))
	saveStage(t, store, "house-laptop", map[string]string{"initial": imgInitial, "linked": imgReady}, map[string]machine.SnapshotOrigin{
		"linked": groupOrigin(dir, "make-laptop", "gen-1", machine.TakeRecording),
	})
	saveStage(t, store, "house-server", map[string]string{"initial": imgInitial, "base": imgReady, "linked": imgOther}, map[string]machine.SnapshotOrigin{
		"base":   {Project: dir, Scene: "prep-server", Take: machine.TakeRecording, Image: imgReady},
		"linked": {Project: dir, Scene: "make-server", Take: machine.TakeRecording, Image: imgOther, Group: "household", Generation: "gen-1"},
	})
	publishGroupProducer(t, dir, store, "prep-server", "house-server", facts.Facts{InputsSHA256: "not-the-digest"})
	publishGroupProducer(t, dir, store, "make-laptop", "house-laptop", facts.Facts{InputsSHA256: "not-the-digest"})
	publishGroupProducer(t, dir, store, "make-server", "house-server", facts.Facts{})
	p, s := load(t, dir, "view")
	rec, err := store.Load("house-laptop")
	if err != nil {
		t.Fatal(err)
	}
	publish(t, p, s, rec, facts.Facts{})
}

func assertPrepServerBeforeMakeServer(t *testing.T, got *Plan) {
	t.Helper()
	var prep, make int
	for i, s := range got.Steps {
		switch s.Scene {
		case "prep-server":
			prep = i + 1
			if s.Reason != StaleInputs {
				t.Fatalf("prep-server reason %q, want %s", s.Reason, StaleInputs)
			}
		case "make-server":
			make = i + 1
			if s.Reason != ReasonGroupSibling {
				t.Fatalf("make-server reason %q, want %s", s.Reason, ReasonGroupSibling)
			}
		}
	}
	if prep == 0 || make == 0 {
		t.Fatalf("plan missing prep-server or make-server: %v", stepSummary(got))
	}
	if prep > make {
		t.Fatalf("prep-server must run before make-server: %v", stepSummary(got))
	}
}

func TestWithDepsRemakesStaleProducerOfSibling(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeSiblingProducerProject(t, dir, store)
	got := planScene(t, filepath.Join(dir, "scenes", "view.json"), store, KindPlay)
	want := []string{"./make-laptop stale:inputs", "./prep-server stale:inputs", "./make-server group-sibling", "./view requested"}
	if names := stepSummary(got); !equal(names, want) {
		t.Fatalf("plan %v, want %v", names, want)
	}
	assertPrepServerBeforeMakeServer(t, got)
}

func TestStaleRemakesStaleProducerOfSibling(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeSiblingProducerProject(t, dir, store)
	got, err := PlanStale(DepsOptions{Options: Options{Dir: dir, Store: store}, Kind: KindPlay})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"./make-laptop stale:inputs", "./prep-server stale:inputs", "./make-server group-sibling", "./view downstream"}
	if names := stepSummary(got); !equal(names, want) {
		t.Fatalf("plan %v, want %v", names, want)
	}
	assertPrepServerBeforeMakeServer(t, got)
}

func writeRehearsalSiblingProducerProject(t *testing.T, dir string, store *machine.Store) {
	t.Helper()
	writeGroupProject(t, dir)
	writeScene(t, dir, "prep-server", groupScene("prep-server", "server", "", "base", ""))
	writeScene(t, dir, "make-laptop", groupScene("make-laptop", "laptop", "", "linked", "household"))
	writeScene(t, dir, "make-server", `{"name":"make-server","layout":"solo","vm":"server","vm-start":{"mode":"clean","snapshot":"base"},"vm-end":{"snapshot":"linked","group":"household"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeScene(t, dir, "view", groupScene("view", "laptop", "linked", "", ""))
	saveStage(t, store, "house-laptop", map[string]string{"initial": imgInitial, "linked": imgReady}, map[string]machine.SnapshotOrigin{
		"linked": groupOrigin(dir, "make-laptop", "gen-1", machine.TakeRecording),
	})
	saveStage(t, store, "house-server", map[string]string{"initial": imgInitial, "base": imgReady, "linked": imgOther}, map[string]machine.SnapshotOrigin{
		"base":   {Project: dir, Scene: "prep-server", Take: machine.TakeRehearsal, Image: imgReady},
		"linked": {Project: dir, Scene: "make-server", Take: machine.TakeRecording, Image: imgOther, Group: "household", Generation: "gen-1"},
	})
	publishGroupProducer(t, dir, store, "prep-server", "house-server", facts.Facts{})
	publishGroupProducer(t, dir, store, "make-laptop", "house-laptop", facts.Facts{InputsSHA256: "not-the-digest"})
	publishGroupProducer(t, dir, store, "make-server", "house-server", facts.Facts{})
	p, s := load(t, dir, "view")
	rec, err := store.Load("house-laptop")
	if err != nil {
		t.Fatal(err)
	}
	publish(t, p, s, rec, facts.Facts{})
}

func TestWithDepsPlaySeedsRehearsalProducerOfSibling(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeRehearsalSiblingProducerProject(t, dir, store)
	view := filepath.Join(dir, "scenes", "view.json")
	got := planScene(t, view, store, KindPlay)
	want := []string{"./make-laptop stale:inputs", "./prep-server " + BlockedRehearsalState, "./make-server group-sibling", "./view requested"}
	if names := stepSummary(got); !equal(names, want) {
		t.Fatalf("play plan %v, want %v", names, want)
	}
	rehearse, err := PlanDeps(DepsOptions{
		Options: Options{Dir: dir, Store: store}, ScenePath: view, Kind: KindRehearse, ReplaceState: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantRehearse := []string{"./make-laptop stale:inputs", "./make-server group-sibling", "./view requested"}
	if names := stepSummary(rehearse); !equal(names, wantRehearse) {
		t.Fatalf("rehearse --replace-state plan %v, want %v", names, wantRehearse)
	}
}

func TestWithDepsOfGroupProducerSelectsSibling(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeGroupProducers(t, dir, store)
	publishGroupProducer(t, dir, store, "make-laptop", "house-laptop", facts.Facts{})
	publishGroupProducer(t, dir, store, "make-server", "house-server", facts.Facts{})
	got := planScene(t, filepath.Join(dir, "scenes", "make-laptop.json"), store, KindPlay)
	want := []string{"./make-server group-sibling", "./make-laptop requested"}
	if names := stepSummary(got); !equal(names, want) {
		t.Fatalf("plan %v, want %v", names, want)
	}
	if !got.Steps[len(got.Steps)-1].Requested || got.Steps[len(got.Steps)-1].Scene != "make-laptop" {
		t.Fatalf("requested must be last: %v", stepSummary(got))
	}
}

func writeDivergentOKPair(t *testing.T, dir string, store *machine.Store) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {
			"a": {"stage": "stage-a"},
			"b": {"stage": "stage-b"}
		},
		"state-groups": {"pair": ["a", "b"]}
	}`)
	writeScene(t, dir, "make-a", groupScene("make-a", "a", "", "linked", "pair"))
	writeScene(t, dir, "make-b", groupScene("make-b", "b", "", "linked", "pair"))
	writeScene(t, dir, "use", groupScene("use", "a", "linked", "", "pair"))
	saveStage(t, store, "stage-a", map[string]string{"initial": imgInitial, "linked": imgReady}, map[string]machine.SnapshotOrigin{
		"linked": {Project: dir, Scene: "make-a", Take: machine.TakeRecording, Image: imgReady, Group: "pair", Generation: "gen-a"},
	})
	saveStage(t, store, "stage-b", map[string]string{"initial": imgInitial, "linked": imgOther}, map[string]machine.SnapshotOrigin{
		"linked": {Project: dir, Scene: "make-b", Take: machine.TakeRecording, Image: imgOther, Group: "pair", Generation: "gen-b"},
	})
	publishGroupProducer(t, dir, store, "make-a", "stage-a", facts.Facts{})
	publishGroupProducer(t, dir, store, "make-b", "stage-b", facts.Facts{})
}

func TestWithDepsRemakesIncompleteGroupMembers(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeDivergentOKPair(t, dir, store)
	if st := statusOf(t, report(t, dir, store), "use"); st.Status != BlockedGroupIncomplete {
		t.Fatalf("use status %s, want %s", st.Status, BlockedGroupIncomplete)
	}
	got := planScene(t, filepath.Join(dir, "scenes", "use.json"), store, KindPlay)
	want := []string{"./make-a " + ReasonGroupIncomplete, "./make-b " + ReasonGroupIncomplete, "./use requested"}
	if names := stepSummary(got); !equal(names, want) {
		t.Fatalf("plan %v, want %v", names, want)
	}
	if !got.Steps[len(got.Steps)-1].Requested || got.Steps[len(got.Steps)-1].Scene != "use" {
		t.Fatalf("requested must be last: %v", stepSummary(got))
	}
}

func TestStaleRemakesIncompleteGroupMembers(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeDivergentOKPair(t, dir, store)
	got, err := PlanStale(DepsOptions{Options: Options{Dir: dir, Store: store}, Kind: KindPlay})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"./make-a " + ReasonGroupIncomplete, "./make-b " + ReasonGroupIncomplete, "./use " + BlockedGroupIncomplete}
	if names := stepSummary(got); !equal(names, want) {
		t.Fatalf("plan %v, want %v", names, want)
	}
}

func TestIncompleteGroupRefusesBlockedMemberProducer(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {
			"a": {"stage": "stage-a"},
			"b": {"stage": "stage-b"}
		},
		"state-groups": {"pair": ["a", "b"]}
	}`)
	writeScene(t, dir, "make-a", groupScene("make-a", "a", "", "linked", "pair"))
	writeScene(t, dir, "make-b", `{"name":"make-b","layout":"solo","vm":"b","vm-start":{"mode":"clean","snapshot":"ghost"},"vm-end":{"snapshot":"linked","group":"pair"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeScene(t, dir, "use", groupScene("use", "a", "linked", "", "pair"))
	saveStage(t, store, "stage-a", map[string]string{"initial": imgInitial, "linked": imgReady}, map[string]machine.SnapshotOrigin{
		"linked": {Project: dir, Scene: "make-a", Take: machine.TakeRecording, Image: imgReady, Group: "pair", Generation: "gen-a"},
	})
	saveStage(t, store, "stage-b", map[string]string{"initial": imgInitial, "linked": imgOther}, map[string]machine.SnapshotOrigin{
		"linked": {Project: dir, Scene: "make-b", Take: machine.TakeRecording, Image: imgOther, Group: "pair", Generation: "gen-b"},
	})
	publishGroupProducer(t, dir, store, "make-a", "stage-a", facts.Facts{})
	_, err := PlanDeps(DepsOptions{Options: Options{Dir: dir, Store: store}, ScenePath: filepath.Join(dir, "scenes", "use.json"), Kind: KindPlay})
	if err == nil || !strings.Contains(err.Error(), "make-b") || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("blocked member producer: %v", err)
	}
}

func TestPlanStepLanesIncludeStartGroupMembers(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeGroupProducers(t, dir, store)
	publishGroupProducer(t, dir, store, "make-laptop", "house-laptop", facts.Facts{})
	publishGroupProducer(t, dir, store, "make-server", "house-server", facts.Facts{})
	got := planScene(t, filepath.Join(dir, "scenes", "use.json"), store, KindPlay)
	if len(got.Steps) != 1 || got.Steps[0].Group != "household" {
		t.Fatalf("step: %+v", got.Steps)
	}
	if !contains(got.Steps[0].Lanes, "house-laptop") || !contains(got.Steps[0].Lanes, "house-server") {
		t.Fatalf("lanes: %v", got.Steps[0].Lanes)
	}
}
