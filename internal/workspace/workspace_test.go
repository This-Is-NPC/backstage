package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/take"
)

const (
	imgInitial = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	imgReady   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	imgOther   = "cccccccccccccccccccccccccccccccc"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testStore(t *testing.T) *machine.Store {
	t.Helper()
	return &machine.Store{Root: filepath.Join(t.TempDir(), "reg")}
}

func saveStage(t *testing.T, s *machine.Store, name string, snaps map[string]string, origins map[string]machine.SnapshotOrigin) {
	t.Helper()
	r := &machine.Record{
		Schema: machine.Schema, ID: strings.Repeat("ab", 16), Name: name,
		Domain: "backstage-test-" + name, URI: "qemu:///system",
		Spec: machine.DefaultSpec(), Status: "ready",
		Snapshots: snaps, SnapshotOrigins: origins,
	}
	if err := s.Save(r); err != nil {
		t.Fatal(err)
	}
}

func writeBaseProject(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {"laptop": {"stage": "demo"}}
	}`)
}

func writeScene(t *testing.T, dir, name, body string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "scenes", name+".json"), body)
}

func hostScene(name string) string {
	return `{"name":"` + name + `","layout":"solo","steps":[{"action":"wait","delay-after":0.05}]}`
}

func cleanScene(name, snap, end string) string {
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

func reuseScene(name string) string {
	return `{"name":"` + name + `","layout":"solo","vm":"laptop","vm-start":{"mode":"reuse"},"steps":[{"action":"wait","delay-after":0.05}]}`
}

func badLayoutProducer(name, end string) string {
	body := `{"name":"` + name + `","layout":"missing-layout","vm":"laptop","vm-start":{"mode":"clean"}`
	if end != "" {
		body += `,"vm-end":{"snapshot":"` + end + `"}`
	}
	body += `,"steps":[{"action":"wait","delay-after":0.05}]}`
	return body
}

func continueScene(name, after string) string {
	return `{"name":"` + name + `","layout":"solo","vm":"laptop","vm-start":{"mode":"continue","after":"` + after + `"},"steps":[{"action":"wait","delay-after":0.05}]}`
}

func load(t *testing.T, dir, name string) (*scene.Project, *scene.Scene) {
	t.Helper()
	p, err := scene.LoadProject(filepath.Join(dir, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := scene.LoadScene(filepath.Join(dir, "scenes", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return p, s
}

func publish(t *testing.T, p *scene.Project, s *scene.Scene, rec *machine.Record, f facts.Facts) take.Published {
	t.Helper()
	img := ""
	if s.VMStartMode() == "clean" {
		if f.StartImage != "" {
			img = f.StartImage
		} else if rec != nil {
			img = rec.Snapshots[startSnap(s)]
		}
	}
	dig, err := scene.InputsDigest(p, s, scene.DigestOptions{Speed: 1, ShowStaging: false, StartState: scene.StartStateToken(s, img)})
	if err != nil {
		t.Fatal(err)
	}
	if f.InputsSHA256 == "" {
		f.InputsSHA256 = dig
	}
	if f.Result == "" {
		f.Result = facts.ResultOK
	}
	if s.VMStartMode() == "clean" && f.StartImage == "" && img != "" {
		f.StartImage = img
		f.StartState = &facts.StartState{Snapshot: startSnap(s)}
	}
	if s.VMEnd != nil && f.EndState == nil && rec != nil {
		f.EndState = &facts.EndState{Snapshot: s.VMEnd.Snapshot, Image: rec.Snapshots[s.VMEnd.Snapshot]}
	}
	sess, err := take.Begin(p, s.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := facts.Write(sess.FactsFile(), f); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sess.Clip(), []byte("clip-"+s.Name), 0o644); err != nil {
		t.Fatal(err)
	}
	pub, err := sess.Publish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func startSnap(s *scene.Scene) string {
	if s.VMStart != nil && s.VMStart.Snapshot != "" {
		return s.VMStart.Snapshot
	}
	return "initial"
}

func report(t *testing.T, dir string, store *machine.Store) *Result {
	t.Helper()
	res, err := Report(Options{Dir: dir, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func statusOf(t *testing.T, res *Result, name string) SceneStatus {
	t.Helper()
	for _, s := range res.Scenes {
		if s.Scene == name {
			return s
		}
	}
	for _, s := range res.Errors {
		if s.Scene == name {
			return s
		}
	}
	t.Fatalf("scene %s not in report: scenes=%+v errors=%+v", name, res.Scenes, res.Errors)
	return SceneStatus{}
}

func configError(t *testing.T, res *Result, rel string) SceneStatus {
	t.Helper()
	for _, s := range res.Errors {
		if s.Kind == KindConfigError && s.ProjectRel == rel {
			return s
		}
	}
	t.Fatalf("config error %s not in errors: %+v", rel, res.Errors)
	return SceneStatus{}
}

func TestStatusTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status string
		setup  func(t *testing.T, dir string, store *machine.Store) string
	}{
		{"ok", OK, func(t *testing.T, dir string, store *machine.Store) string {
			writeBaseProject(t, dir)
			writeScene(t, dir, "host", hostScene("host"))
			p, s := load(t, dir, "host")
			publish(t, p, s, nil, facts.Facts{})
			return "host"
		}},
		{"missing", Missing, func(t *testing.T, dir string, store *machine.Store) string {
			writeBaseProject(t, dir)
			writeScene(t, dir, "host", hostScene("host"))
			return "host"
		}},
		{"stale-inputs", StaleInputs, func(t *testing.T, dir string, store *machine.Store) string {
			writeBaseProject(t, dir)
			writeScene(t, dir, "host", hostScene("host"))
			p, s := load(t, dir, "host")
			publish(t, p, s, nil, facts.Facts{InputsSHA256: "0"})
			return "host"
		}},
		{"stale-inputs-no-digest", StaleInputs, func(t *testing.T, dir string, store *machine.Store) string {
			writeBaseProject(t, dir)
			writeScene(t, dir, "host", hostScene("host"))
			p, s := load(t, dir, "host")
			sess, err := take.Begin(p, s.Name)
			if err != nil {
				t.Fatal(err)
			}
			if err := facts.Write(sess.FactsFile(), facts.Facts{Result: facts.ResultOK}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(sess.Clip(), []byte("clip"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := sess.Publish(context.Background()); err != nil {
				t.Fatal(err)
			}
			return "host"
		}},
		{"blocked-no-producer", BlockedNoProducer, func(t *testing.T, dir string, store *machine.Store) string {
			writeBaseProject(t, dir)
			writeScene(t, dir, "need", cleanScene("need", "ghost", ""))
			saveStage(t, store, "demo", map[string]string{"initial": imgInitial}, nil)
			return "need"
		}},
		{"blocked-state-missing", BlockedStateMissing, func(t *testing.T, dir string, store *machine.Store) string {
			writeBaseProject(t, dir)
			writeScene(t, dir, "make", cleanScene("make", "", "ready"))
			writeScene(t, dir, "use", cleanScene("use", "ready", ""))
			saveStage(t, store, "demo", map[string]string{"initial": imgInitial}, nil)
			return "use"
		}},
		{"blocked-rehearsal-state", BlockedRehearsalState, func(t *testing.T, dir string, store *machine.Store) string {
			writeBaseProject(t, dir)
			writeScene(t, dir, "use", cleanScene("use", "ready", ""))
			saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
				"ready": {Project: dir, Scene: "make", Take: machine.TakeRehearsal, Image: imgReady},
			})
			return "use"
		}},
		{"stale-state-mismatch", StaleStateMismatch, func(t *testing.T, dir string, store *machine.Store) string {
			writeBaseProject(t, dir)
			writeScene(t, dir, "make", cleanScene("make", "", "ready"))
			saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
				"ready": {Project: dir, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
			})
			p, s := load(t, dir, "make")
			rec, _ := store.Load("demo")
			publish(t, p, s, rec, facts.Facts{EndState: &facts.EndState{Snapshot: "ready", Image: imgOther}})
			return "make"
		}},
		{"stale-start-state", StaleStartState, func(t *testing.T, dir string, store *machine.Store) string {
			writeBaseProject(t, dir)
			writeScene(t, dir, "use", cleanScene("use", "", ""))
			saveStage(t, store, "demo", map[string]string{"initial": imgInitial}, nil)
			p, s := load(t, dir, "use")
			rec, _ := store.Load("demo")
			publish(t, p, s, rec, facts.Facts{StartImage: imgOther, StartState: &facts.StartState{Snapshot: "initial"}})
			return "use"
		}},
		{"stale-upstream", StaleUpstream, func(t *testing.T, dir string, store *machine.Store) string {
			writeBaseProject(t, dir)
			writeScene(t, dir, "make", cleanScene("make", "", "ready"))
			writeScene(t, dir, "use", cleanScene("use", "ready", ""))
			saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
				"ready": {Project: dir, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
			})
			p, s := load(t, dir, "use")
			rec, _ := store.Load("demo")
			publish(t, p, s, rec, facts.Facts{})
			return "use"
		}},
		{"unverifiable", Unverifiable, func(t *testing.T, dir string, store *machine.Store) string {
			writeBaseProject(t, dir)
			writeScene(t, dir, "live", reuseScene("live"))
			saveStage(t, store, "demo", map[string]string{"initial": imgInitial}, nil)
			p, s := load(t, dir, "live")
			publish(t, p, s, nil, facts.Facts{})
			return "live"
		}},
		{"stale-upstream-continue", StaleUpstream, func(t *testing.T, dir string, store *machine.Store) string {
			writeBaseProject(t, dir)
			writeScene(t, dir, "first", reuseScene("first"))
			writeScene(t, dir, "next", continueScene("next", "first"))
			saveStage(t, store, "demo", map[string]string{"initial": imgInitial}, nil)
			p, s := load(t, dir, "next")
			publish(t, p, s, nil, facts.Facts{})
			return "next"
		}},
		{"ok-clean-manual-root", OK, func(t *testing.T, dir string, store *machine.Store) string {
			writeBaseProject(t, dir)
			writeScene(t, dir, "make", cleanScene("make", "", "ready"))
			saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
				"ready": {Project: dir, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
			})
			p, s := load(t, dir, "make")
			rec, _ := store.Load("demo")
			publish(t, p, s, rec, facts.Facts{})
			return "make"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			store := testStore(t)
			sceneName := tc.setup(t, dir, store)
			got := statusOf(t, report(t, dir, store), sceneName)
			if got.Status != tc.status {
				t.Fatalf("status %s, want %s (reasons %v)", got.Status, tc.status, got.Reasons)
			}
		})
	}
}

func TestPrecedenceBlockedBeatsMissing(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "make", cleanScene("make", "", "ready"))
	writeScene(t, dir, "use", cleanScene("use", "ready", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial}, nil)
	got := statusOf(t, report(t, dir, store), "use")
	if got.Status != BlockedStateMissing {
		t.Fatalf("status %s, want %s", got.Status, BlockedStateMissing)
	}
	if !contains(got.Reasons, Missing) || !contains(got.Reasons, BlockedStateMissing) {
		t.Fatalf("reasons %v", got.Reasons)
	}
}

func TestPrecedenceInputsBeatsStartState(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "use", cleanScene("use", "", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial}, nil)
	p, s := load(t, dir, "use")
	rec, _ := store.Load("demo")
	publish(t, p, s, rec, facts.Facts{InputsSHA256: "0", StartImage: imgOther, StartState: &facts.StartState{Snapshot: "initial"}})
	got := statusOf(t, report(t, dir, store), "use")
	if got.Status != StaleInputs {
		t.Fatalf("status %s, want %s", got.Status, StaleInputs)
	}
	if !contains(got.Reasons, StaleStartState) {
		t.Fatalf("reasons %v", got.Reasons)
	}
}

func TestDirectoryArgumentLimitsReportNotDeps(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	en := filepath.Join(root, "en")
	pt := filepath.Join(root, "pt")
	writeFile(t, filepath.Join(en, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeFile(t, filepath.Join(pt, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeScene(t, en, "make", cleanScene("make", "", "ready"))
	writeScene(t, pt, "use", cleanScene("use", "ready", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: en, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
	})
	p, s := load(t, pt, "use")
	rec, _ := store.Load("demo")
	publish(t, p, s, rec, facts.Facts{})
	res := report(t, pt, store)
	if len(res.Scenes) != 1 || res.Scenes[0].Scene != "use" {
		t.Fatalf("limited report: %+v", res.Scenes)
	}
	if res.Scenes[0].Status != StaleUpstream {
		t.Fatalf("consumer without resolving producer: %s", res.Scenes[0].Status)
	}
}

func TestTwoLevelExtendsAndTwoLeaves(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	mid := filepath.Join(root, "mid")
	leaf := filepath.Join(mid, "leaf")
	sib := filepath.Join(root, "sib")
	writeFile(t, filepath.Join(mid, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeFile(t, filepath.Join(sib, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeScene(t, leaf, "a", hostScene("a"))
	writeScene(t, sib, "b", hostScene("b"))
	p, s := load(t, leaf, "a")
	publish(t, p, s, nil, facts.Facts{})
	res := report(t, root, store)
	if len(res.Scenes) != 2 {
		t.Fatalf("scenes %d: %+v", len(res.Scenes), res.Scenes)
	}
	names := map[string]string{}
	for _, sc := range res.Scenes {
		names[sc.Scene] = sc.Status
	}
	if names["a"] != OK || names["b"] != Missing {
		t.Fatalf("statuses %v", names)
	}
}

func TestDuplicateProducerAndCycle(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "one", cleanScene("one", "", "ready"))
	writeScene(t, dir, "two", cleanScene("two", "", "ready"))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial}, nil)
	_, err := Report(Options{Dir: dir, Store: store})
	var conflict *ConflictError
	if !errors.As(err, &conflict) || len(conflict.Duplicates) != 1 {
		t.Fatalf("duplicate: %v", err)
	}

	cycle := t.TempDir()
	writeBaseProject(t, cycle)
	writeScene(t, cycle, "one", cleanScene("one", "other", "ready"))
	writeScene(t, cycle, "two", cleanScene("two", "ready", "other"))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady, "other": imgOther}, nil)
	_, err = Report(Options{Dir: cycle, Store: store})
	if !errors.As(err, &conflict) || len(conflict.Cycles) == 0 {
		t.Fatalf("cycle: %v", err)
	}
}

func TestReadOnlyStoreAndRecordOut(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "host", hostScene("host"))
	p, s := load(t, dir, "host")
	publish(t, p, s, nil, facts.Facts{})
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial}, nil)
	before := listTree(t, dir, store.Root)
	chmodTree(t, dir, 0o555, 0o444)
	chmodTree(t, store.Root, 0o555, 0o444)
	restore := func() {
		_ = filepath.WalkDir(dir, func(path string, _ fs.DirEntry, _ error) error {
			return os.Chmod(path, 0o755)
		})
		_ = filepath.WalkDir(store.Root, func(path string, _ fs.DirEntry, _ error) error {
			return os.Chmod(path, 0o755)
		})
	}
	t.Cleanup(restore)
	defer restore()
	res, err := Report(Options{Dir: dir, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if statusOf(t, res, "host").Status != OK {
		t.Fatalf("read-only status: %+v", res.Scenes)
	}
	after := listTree(t, dir, store.Root)
	if after != before {
		t.Fatalf("created files:\nbefore %s\nafter %s", before, after)
	}
	if _, err := os.Stat(filepath.Join(store.Root, "locks")); !os.IsNotExist(err) {
		t.Fatalf("lock dir appeared: %v", err)
	}
}

func TestConcurrentPublishDoesNotMixGenerations(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "host", hostScene("host"))
	p, s := load(t, dir, "host")
	first := publish(t, p, s, nil, facts.Facts{})
	h, err := take.Open(p, s.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if h.Facts != first.Facts {
		t.Fatalf("lease %s, want %s", h.Facts, first.Facts)
	}
	second := publish(t, p, s, nil, facts.Facts{InputsSHA256: "0"})
	got, err := os.ReadFile(h.Facts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), `"inputs-sha256": "0"`) {
		t.Fatal("held lease saw the new generation")
	}
	var f facts.Facts
	if err := json.Unmarshal(got, &f); err != nil {
		t.Fatal(err)
	}
	if f.InputsSHA256 == "0" {
		t.Fatal("mixed generations")
	}
	st := statusOf(t, report(t, dir, store), "host")
	if st.Status != StaleInputs {
		t.Fatalf("report status %s, want %s", st.Status, StaleInputs)
	}
	if st.Facts != second.Facts {
		t.Fatalf("report facts %s, want generation %s", st.Facts, second.Facts)
	}
	if st.Facts == first.Facts || st.Facts == h.Facts {
		t.Fatal("report mixed the held generation")
	}
}

func TestAttemptsNeverReadAndLegacyFallback(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "host", hostScene("host"))
	p, s := load(t, dir, "host")
	sess, err := take.Begin(p, s.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := facts.Write(sess.FactsFile(), facts.Facts{InputsSHA256: "dead", Result: facts.ResultStepsFailed}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sess.Clip(), []byte("attempt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.KeepAttempt(); err != nil {
		t.Fatal(err)
	}
	if statusOf(t, report(t, dir, store), "host").Status != Missing {
		t.Fatal("read an attempt as the take")
	}
	pub := publish(t, p, s, nil, facts.Facts{})
	got := statusOf(t, report(t, dir, store), "host")
	if got.Status != OK {
		t.Fatalf("published: %s", got.Status)
	}
	if strings.Contains(got.Clip, "attempts") || strings.Contains(got.Facts, "attempts") {
		t.Fatalf("paths under attempts: %s %s", got.Clip, got.Facts)
	}
	if got.Facts != pub.Facts {
		t.Fatalf("facts %s, want generation %s", got.Facts, pub.Facts)
	}

	legacy := t.TempDir()
	writeBaseProject(t, legacy)
	writeScene(t, legacy, "old", hostScene("old"))
	lp, ls := load(t, legacy, "old")
	dig, err := scene.InputsDigest(lp, ls, scene.DigestOptions{Speed: 1, ShowStaging: false})
	if err != nil {
		t.Fatal(err)
	}
	paths, err := take.ForScene(lp, ls.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.OutDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StableClip(), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := facts.Write(paths.StableFacts(), facts.Facts{InputsSHA256: dig, Result: facts.ResultOK}); err != nil {
		t.Fatal(err)
	}
	st := statusOf(t, report(t, legacy, store), "old")
	if st.Status != OK {
		t.Fatalf("legacy: %s", st.Status)
	}
	if st.Clip != paths.StableClip() || st.Facts != paths.StableFacts() {
		t.Fatalf("legacy paths %+v", st)
	}
}

func TestVisualSceneOmitted(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeFile(t, filepath.Join(dir, "scenes", "pic.html"), "<p>x</p>")
	writeScene(t, dir, "pic", `{"type":"visual","entry":"scenes/pic.html","duration":1}`)
	writeScene(t, dir, "host", hostScene("host"))
	res := report(t, dir, store)
	if len(res.Scenes) != 1 || res.Scenes[0].Scene != "host" {
		t.Fatalf("visual leaked: %+v", res.Scenes)
	}
}

func TestManualStateLabel(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "make", cleanScene("make", "", "ready"))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: dir, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
	})
	p, s := load(t, dir, "make")
	rec, _ := store.Load("demo")
	publish(t, p, s, rec, facts.Facts{})
	res := report(t, dir, store)
	labels := map[string]string{}
	for _, st := range res.States {
		labels[st.Snapshot] = st.Label
	}
	if labels["initial"] != ManualLabel {
		t.Fatalf("initial label %q", labels["initial"])
	}
	if labels["ready"] != "make" {
		t.Fatalf("ready label %q", labels["ready"])
	}
}

func TestDiscoverySkipsHiddenRecordOutAndDirSymlink(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	writeScene(t, root, "host", hostScene("host"))
	hidden := filepath.Join(root, ".secret")
	writeFile(t, filepath.Join(hidden, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeScene(t, hidden, "ghost", hostScene("ghost"))
	outside := t.TempDir()
	writeBaseProject(t, outside)
	writeScene(t, outside, "link", hostScene("link"))
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	res := report(t, root, store)
	for _, s := range res.Scenes {
		if s.Scene == "ghost" || s.Scene == "link" {
			t.Fatalf("discovered skipped project: %+v", s)
		}
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func listTree(t *testing.T, roots ...string) string {
	t.Helper()
	var b strings.Builder
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			b.WriteString(root)
			b.WriteByte(':')
			b.WriteString(rel)
			b.WriteByte('\n')
			return nil
		})
	}
	return b.String()
}

func TestProducerRerecordLeavesConsumerStartState(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "make", cleanScene("make", "", "ready"))
	writeScene(t, dir, "use", cleanScene("use", "ready", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: dir, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
	})
	rec, _ := store.Load("demo")
	mp, ms := load(t, dir, "make")
	publish(t, mp, ms, rec, facts.Facts{})
	up, us := load(t, dir, "use")
	publish(t, up, us, rec, facts.Facts{})
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgOther}, map[string]machine.SnapshotOrigin{
		"ready": {Project: dir, Scene: "make", Take: machine.TakeRecording, Image: imgOther},
	})
	got := statusOf(t, report(t, dir, store), "use")
	if got.Status != StaleStartState {
		t.Fatalf("status %s, want %s (reasons %v)", got.Status, StaleStartState, got.Reasons)
	}
	if contains(got.Reasons, StaleInputs) {
		t.Fatalf("re-recorded producer must not look like stale:inputs: %v", got.Reasons)
	}
}

func TestDeclaredProducerDoesNotLabelManualSnapshot(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "make", cleanScene("make", "", "ready"))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, nil)
	res := report(t, dir, store)
	labels := map[string]string{}
	for _, st := range res.States {
		labels[st.Snapshot] = st.Label
	}
	if labels["ready"] != ManualLabel {
		t.Fatalf("ready without origin labeled %q, want %s", labels["ready"], ManualLabel)
	}
}

func TestManualSnapshotIsValidRoot(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "make", cleanScene("make", "", "ready"))
	writeScene(t, dir, "use", cleanScene("use", "ready", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, nil)
	up, us := load(t, dir, "use")
	rec, _ := store.Load("demo")
	publish(t, up, us, rec, facts.Facts{})
	got := statusOf(t, report(t, dir, store), "use")
	if got.Status != OK {
		t.Fatalf("manual root consumer %s, want %s (reasons %v)", got.Status, OK, got.Reasons)
	}
	if contains(got.Reasons, StaleUpstream) {
		t.Fatalf("manual snapshot leaked stale:upstream: %v", got.Reasons)
	}

	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: dir, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
	})
	stale := statusOf(t, report(t, dir, store), "use")
	if stale.Status != StaleUpstream {
		t.Fatalf("originated producer: %s, want %s", stale.Status, StaleUpstream)
	}
	if stale.Detail != "make is missing" {
		t.Fatalf("upstream detail %q", stale.Detail)
	}
}

func TestInvalidNestedConfigIsWarning(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "host", hostScene("host"))
	bad := filepath.Join(dir, "vendor", "x", "backstage.json")
	writeFile(t, bad, "{")
	res := report(t, dir, store)
	if statusOf(t, res, "host").Status != Missing {
		t.Fatalf("host: %+v", res.Scenes)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected a warning for the broken vendor config")
	}
	found := false
	for _, w := range res.Warnings {
		if w.Path == bad && w.Error != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings %+v", res.Warnings)
	}
	var buf bytes.Buffer
	if err := WriteText(&buf, res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Warnings:") || !strings.Contains(buf.String(), bad) {
		t.Fatalf("text warnings:\n%s", buf.String())
	}
}

func TestRecordOutSkippedBeforeDescend(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {"laptop": {"stage": "demo"}},
		"record": {"out": "Out"}
	}`)
	writeScene(t, dir, "host", hostScene("host"))
	nested := filepath.Join(dir, "Out", "backstage.json")
	writeFile(t, nested, "{")
	writeFile(t, filepath.Join(dir, "Out", "scenes", "ghost.json"), hostScene("ghost"))
	res := report(t, dir, store)
	if statusOf(t, res, "host").Scene != "host" {
		t.Fatalf("host missing: %+v", res.Scenes)
	}
	for _, s := range res.Scenes {
		if s.Scene == "ghost" {
			t.Fatal("walked record.out")
		}
	}
	for _, w := range res.Warnings {
		if strings.Contains(w.Path, string(filepath.Separator)+"Out"+string(filepath.Separator)) {
			t.Fatalf("loaded config inside record.out: %+v", w)
		}
	}
}

func TestSceneIDUsesRootRelativeProject(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "demo")
	child := filepath.Join(root, "demo")
	store := testStore(t)
	writeBaseProject(t, root)
	writeFile(t, filepath.Join(child, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeScene(t, root, "a", hostScene("a"))
	writeScene(t, child, "a", hostScene("a"))
	res := report(t, root, store)
	if len(res.Scenes) != 2 {
		t.Fatalf("scenes %d: %+v", len(res.Scenes), res.Scenes)
	}
	ids := map[string]bool{}
	for _, s := range res.Scenes {
		ids[s.ProjectRel+"/"+s.Scene] = true
	}
	if !ids["./a"] || !ids["demo/a"] {
		t.Fatalf("ids %v", ids)
	}
	var buf bytes.Buffer
	if err := WriteText(&buf, res); err != nil {
		t.Fatal(err)
	}
	text := buf.String()
	if !strings.Contains(text, "./a") || !strings.Contains(text, "demo/a") {
		t.Fatalf("text names:\n%s", text)
	}
}

func TestCorruptStageFailsReport(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "use", cleanScene("use", "", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial}, nil)
	path := filepath.Join(store.Dir("demo"), "stage.json")
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Report(Options{Dir: dir, Store: store})
	if err == nil {
		t.Fatal("corrupt stage.json must fail the report")
	}
	if !strings.Contains(err.Error(), "stage") && !strings.Contains(err.Error(), "invalid") && !strings.Contains(err.Error(), "json") {
		t.Fatalf("unclear stage error: %v", err)
	}
}

func TestUnreadableStageFailsReport(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "use", cleanScene("use", "", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial}, nil)
	path := filepath.Join(store.Dir("demo"), "stage.json")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("process can read mode 000")
	}
	_, err := Report(Options{Dir: dir, Store: store})
	if err == nil {
		t.Fatal("unreadable stage.json must fail the report")
	}
}

func TestInvalidTakeIsSceneError(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "need", cleanScene("need", "ghost", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial}, nil)
	p, _ := load(t, dir, "need")
	paths, err := take.ForScene(p, "need")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(paths.OutDir, ".takes", "need"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.OutDir, ".takes", "need", "lock"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Manifest(), []byte(`{"version":9}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Report(Options{Dir: dir, Store: store})
	if err != nil {
		t.Fatalf("invalid take must be a scene row, not a report error: %v", err)
	}
	got := statusOf(t, res, "need")
	if got.Status != Error || got.Kind != KindSceneError {
		t.Fatalf("status %s kind %s, want %s/%s (reasons %v)", got.Status, got.Kind, Error, KindSceneError, got.Reasons)
	}
	if !contains(got.Reasons, Error) || !contains(got.Reasons, BlockedNoProducer) {
		t.Fatalf("reasons %v", got.Reasons)
	}
	if got.Error == "" || !strings.Contains(got.Detail, "manifest") && !strings.Contains(got.Error, "manifest") {
		t.Fatalf("error detail %+v", got)
	}
	if !res.HasError() {
		t.Fatal("HasError")
	}
}

func TestDirectoryInsideProjectIsInScope(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "host", hostScene("host"))
	res, err := Report(Options{Dir: filepath.Join(dir, "scenes"), Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Scenes) != 1 || res.Scenes[0].Scene != "host" {
		t.Fatalf("filter inside project: %+v", res.Scenes)
	}
}

func TestSelfProducerIsCycle(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "loop", cleanScene("loop", "ready", "ready"))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, nil)
	_, err := Report(Options{Dir: dir, Store: store})
	var conflict *ConflictError
	if !errors.As(err, &conflict) || len(conflict.Cycles) == 0 {
		t.Fatalf("self-edge: %v", err)
	}
}

func TestPrecedenceAdjacentPairs(t *testing.T) {
	want := []string{
		Error, BlockedNoProducer, BlockedStateMissing, BlockedRehearsalState,
		Missing, StaleInputs, StaleStateMismatch, StaleStartState,
		StaleUpstream, Unverifiable, OK,
	}
	if len(precedence) != len(want) {
		t.Fatalf("precedence %v, want %v", precedence, want)
	}
	for i := range want {
		if precedence[i] != want[i] {
			t.Fatalf("precedence[%d]=%s, want %s (swap would hide a status)", i, precedence[i], want[i])
		}
	}
	for i := 0; i < len(want)-1; i++ {
		a, b := want[i], want[i+1]
		got := firstStatus(selectedReasons(map[string]bool{a: true, b: true}))
		if got != a {
			t.Fatalf("%s should precede %s, got %s", a, b, got)
		}
	}
	if got := selectedReasons(map[string]bool{OK: true, StaleUpstream: true}); len(got) != 1 || got[0] != StaleUpstream {
		t.Fatalf("ok leaked into reasons: %v", got)
	}
	if got := selectedReasons(map[string]bool{OK: true}); len(got) != 1 || got[0] != OK {
		t.Fatalf("ok alone: %v", got)
	}
}

func TestReuseDivergentDigestIsStaleInputs(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "live", reuseScene("live"))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial}, nil)
	p, s := load(t, dir, "live")
	publish(t, p, s, nil, facts.Facts{InputsSHA256: "0"})
	got := statusOf(t, report(t, dir, store), "live")
	if got.Status != StaleInputs {
		t.Fatalf("status %s, want %s (reasons %v)", got.Status, StaleInputs, got.Reasons)
	}
	if contains(got.Reasons, Unverifiable) {
		t.Fatalf("divergent reuse must not be unverifiable: %v", got.Reasons)
	}
}

func TestSkipSymlinkSameWorkspace(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	leaf := filepath.Join(root, "leaf")
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeScene(t, leaf, "a", hostScene("a"))
	if err := os.Symlink(leaf, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	res := report(t, root, store)
	if len(res.Scenes) != 1 || res.Scenes[0].Scene != "a" {
		t.Fatalf("symlink doubled a project: %+v", res.Scenes)
	}
	if res.Scenes[0].ProjectRel != "leaf" {
		t.Fatalf("project rel %q", res.Scenes[0].ProjectRel)
	}
}

func TestReportReleasesTakeLeases(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	var pubs []take.Published
	for i := 0; i < 60; i++ {
		name := fmt.Sprintf("s%02d", i)
		writeScene(t, dir, name, hostScene(name))
		p, s := load(t, dir, name)
		pubs = append(pubs, publish(t, p, s, nil, facts.Facts{}))
	}
	if _, err := Report(Options{Dir: dir, Store: store}); err != nil {
		t.Fatal(err)
	}
	for i, pub := range pubs {
		lease := filepath.Join(filepath.Dir(pub.Clip), "lease")
		f, err := os.Open(lease)
		if err != nil {
			t.Fatalf("scene s%02d lease: %v", i, err)
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		_ = f.Close()
		if err != nil {
			t.Fatalf("scene s%02d leaked a take lease: %v", i, err)
		}
	}
}

func TestReportCreatesNoFilesOnWritableTree(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "host", hostScene("host"))
	p, s := load(t, dir, "host")
	publish(t, p, s, nil, facts.Facts{})
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial}, nil)
	before := listTree(t, dir, store.Root)
	if _, err := Report(Options{Dir: dir, Store: store}); err != nil {
		t.Fatal(err)
	}
	after := listTree(t, dir, store.Root)
	if after != before {
		t.Fatalf("created files:\nbefore %s\nafter %s", before, after)
	}
}

func TestJSONStatesEmptyArrayAndStables(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "host", hostScene("host"))
	p, s := load(t, dir, "host")
	pub := publish(t, p, s, nil, facts.Facts{})
	res := report(t, dir, store)
	if res.States == nil {
		t.Fatal("states is nil")
	}
	got := statusOf(t, res, "host")
	paths, err := take.ForScene(p, "host")
	if err != nil {
		t.Fatal(err)
	}
	if got.StableClip != paths.StableClip() || got.StableFacts != paths.StableFacts() {
		t.Fatalf("stable paths %+v", got)
	}
	if got.Clip != pub.Clip || got.Facts != pub.Facts {
		t.Fatalf("generation paths %+v", got)
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, res); err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["states"]) != "[]" {
		t.Fatalf("states json %s", raw["states"])
	}
	if string(raw["errors"]) != "[]" {
		t.Fatalf("errors json %s", raw["errors"])
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"stable-clip"`)) || !bytes.Contains(buf.Bytes(), []byte(`"stable-facts"`)) {
		t.Fatalf("json missing stables:\n%s", buf.String())
	}
}

func TestMissingStageRecordIsAbsentNotError(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "need", cleanScene("need", "ghost", ""))
	writeScene(t, dir, "live", reuseScene("live"))
	p, s := load(t, dir, "live")
	publish(t, p, s, nil, facts.Facts{InputsSHA256: "0"})
	res, err := Report(Options{Dir: dir, Store: store})
	if err != nil {
		t.Fatalf("missing stage record must not fail the report: %v", err)
	}
	if statusOf(t, res, "need").Status != BlockedNoProducer {
		t.Fatalf("clean without stage: %+v", statusOf(t, res, "need"))
	}
	if statusOf(t, res, "live").Status != StaleInputs {
		t.Fatalf("reuse without stage: %+v", statusOf(t, res, "live"))
	}
}

func TestMissingStoreRootIsAbsentNotError(t *testing.T) {
	dir := t.TempDir()
	store := &machine.Store{Root: filepath.Join(t.TempDir(), "no-such-store")}
	writeBaseProject(t, dir)
	writeScene(t, dir, "need", cleanScene("need", "ghost", ""))
	writeScene(t, dir, "live", reuseScene("live"))
	p, s := load(t, dir, "live")
	publish(t, p, s, nil, facts.Facts{InputsSHA256: "0"})
	res, err := Report(Options{Dir: dir, Store: store})
	if err != nil {
		t.Fatalf("missing store must not fail the report: %v", err)
	}
	if statusOf(t, res, "need").Status != BlockedNoProducer {
		t.Fatalf("clean without store: %+v", statusOf(t, res, "need"))
	}
	if statusOf(t, res, "live").Status != StaleInputs {
		t.Fatalf("reuse without store: %+v", statusOf(t, res, "live"))
	}
}

func TestFilterUsesClosestProject(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	writeScene(t, root, "rootscene", hostScene("rootscene"))
	mid := filepath.Join(root, "mid")
	pt := filepath.Join(mid, "pt")
	writeFile(t, filepath.Join(mid, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeFile(t, filepath.Join(pt, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeScene(t, mid, "midscene", hostScene("midscene"))
	writeScene(t, pt, "ptscene", hostScene("ptscene"))

	onlyPT := report(t, pt, store)
	if names := sceneNames(onlyPT); len(names) != 1 || names[0] != "ptscene" {
		t.Fatalf("status mid/pt: %v", names)
	}
	midReport := report(t, mid, store)
	if names := sceneNames(midReport); !contains(names, "midscene") || !contains(names, "ptscene") || contains(names, "rootscene") {
		t.Fatalf("status mid: %v", names)
	}
	all := report(t, root, store)
	if names := sceneNames(all); !contains(names, "rootscene") || !contains(names, "midscene") || !contains(names, "ptscene") {
		t.Fatalf("status root: %v", names)
	}
}

func TestSymlinkStartDirDiscoversProjects(t *testing.T) {
	real := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, real)
	writeScene(t, real, "host", hostScene("host"))
	parent := t.TempDir()
	link := filepath.Join(parent, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	for _, start := range []string{link, filepath.Join(link, "scenes")} {
		res, err := Report(Options{Dir: start, Store: store})
		if err != nil {
			t.Fatalf("%s: %v", start, err)
		}
		if len(res.Scenes) != 1 || res.Scenes[0].Scene != "host" {
			t.Fatalf("%s: %+v", start, res.Scenes)
		}
	}
}

func TestInvalidConfigOnChainIsProjectError(t *testing.T) {
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
	writeScene(t, en, "make", cleanScene("make", "", "ready"))
	writeScene(t, pt, "use", cleanScene("use", "ready", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: en, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
	})
	p, s := load(t, pt, "use")
	rec, _ := store.Load("demo")
	publish(t, p, s, rec, facts.Facts{})
	res, err := Report(Options{Dir: root, Store: store})
	if err != nil {
		t.Fatalf("invalid chain config must be a row, not a report abort: %v", err)
	}
	if !res.HasError() {
		t.Fatal("HasError")
	}
	enRow := configError(t, res, "en")
	if !strings.Contains(enRow.Detail, "record.out") && !strings.Contains(enRow.Error, "record.out") {
		t.Fatalf("en error detail %+v", enRow)
	}
	if enRow.Kind != KindConfigError || enRow.Scene != "" {
		t.Fatalf("config error identity %+v", enRow)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w.Path, string(filepath.Separator)+"en"+string(filepath.Separator)) || strings.HasSuffix(w.Path, filepath.Join("en", "backstage.json")) {
			t.Fatalf("chain validation failure must not be a warning: %+v", w)
		}
	}
	use := statusOf(t, res, "use")
	if use.Status != StaleUpstream || use.Detail != "en is error" {
		t.Fatalf("consumer %+v", use)
	}
	limited, err := Report(Options{Dir: pt, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if !limited.HasError() {
		t.Fatal("filter must not hide the config error")
	}
	if configError(t, limited, "en").Kind != KindConfigError {
		t.Fatal("config error missing under status pt")
	}
	if len(limited.Scenes) != 1 || limited.Scenes[0].Scene != "use" {
		t.Fatalf("status pt scenes: %+v", limited.Scenes)
	}
}

func TestInvalidSceneJSONIsSceneError(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "host", hostScene("host"))
	writeFile(t, filepath.Join(dir, "scenes", "bad.json"), "{")
	res, err := Report(Options{Dir: dir, Store: store})
	if err != nil {
		t.Fatalf("invalid scene must be a row, not a report abort: %v", err)
	}
	got := statusOf(t, res, "bad")
	if got.Status != Error || got.Kind != KindSceneError {
		t.Fatalf("status %s kind %s, want %s/%s (reasons %v)", got.Status, got.Kind, Error, KindSceneError, got.Reasons)
	}
	if got.Error == "" || got.Detail == "" {
		t.Fatalf("missing error text %+v", got)
	}
	for _, s := range res.Scenes {
		if s.Scene == "bad" {
			t.Fatal("scene error must live in the errors section")
		}
	}
	if statusOf(t, res, "host").Scene != "host" {
		t.Fatalf("host dropped: %+v", res.Scenes)
	}
	if !res.HasError() {
		t.Fatal("HasError")
	}
}

func TestFilterDoesNotHideSceneErrors(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	en := filepath.Join(root, "en")
	pt := filepath.Join(root, "pt")
	writeFile(t, filepath.Join(en, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeFile(t, filepath.Join(pt, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeFile(t, filepath.Join(en, "scenes", "make.json"), "{")
	writeScene(t, pt, "use", cleanScene("use", "ready", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: en, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
	})
	p, s := load(t, pt, "use")
	rec, _ := store.Load("demo")
	publish(t, p, s, rec, facts.Facts{})
	res, err := Report(Options{Dir: pt, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasError() {
		t.Fatal("filter must not hide a scene error")
	}
	got := statusOf(t, res, "make")
	if got.Kind != KindSceneError || got.Status != Error {
		t.Fatalf("hidden producer %+v", got)
	}
	if len(res.Scenes) != 1 || res.Scenes[0].Scene != "use" {
		t.Fatalf("status pt scenes: %+v", res.Scenes)
	}
	if statusOf(t, res, "use").Status != StaleUpstream {
		t.Fatalf("use %+v", statusOf(t, res, "use"))
	}
}

func TestInvalidSceneValidateIsSceneError(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "host", hostScene("host"))
	writeScene(t, dir, "make", badLayoutProducer("make", "ready"))
	res, err := Report(Options{Dir: dir, Store: store})
	if err != nil {
		t.Fatalf("Validate failure must be a scene row, not a report abort: %v", err)
	}
	got := statusOf(t, res, "make")
	if got.Status != Error || got.Kind != KindSceneError {
		t.Fatalf("status %s kind %s (reasons %v)", got.Status, got.Kind, got.Reasons)
	}
	if statusOf(t, res, "host").Scene != "host" {
		t.Fatalf("host dropped: %+v", res.Scenes)
	}
	if !res.HasError() {
		t.Fatal("HasError")
	}
}

func TestInvalidProducerKeepsGraphRole(t *testing.T) {
	type setupFn func(t *testing.T, root, en, pt string)
	cases := []struct {
		name     string
		setup    setupFn
		withSnap bool
		want     string
		detail   string
	}{
		{"validate-with-snap", setupValidateFailProducer, true, StaleUpstream, "make is error"},
		{"validate-without-snap", setupValidateFailProducer, false, BlockedStateMissing, ""},
		{"json-with-snap", setupJSONFailProducer, true, StaleUpstream, "make is error"},
		{"json-without-snap", setupJSONFailProducer, false, BlockedNoProducer, ""},
		{"config-with-snap", setupConfigFailProducer, true, StaleUpstream, "en is error"},
		{"config-without-snap", setupConfigFailProducer, false, BlockedNoProducer, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store := testStore(t)
			writeBaseProject(t, root)
			en := filepath.Join(root, "en")
			pt := filepath.Join(root, "pt")
			writeFile(t, filepath.Join(pt, "backstage.json"), `{"extends":"../backstage.json"}`)
			tc.setup(t, root, en, pt)
			writeScene(t, pt, "use", cleanScene("use", "ready", ""))
			snaps := map[string]string{"initial": imgInitial}
			var origins map[string]machine.SnapshotOrigin
			if tc.withSnap {
				snaps["ready"] = imgReady
				origins = map[string]machine.SnapshotOrigin{
					"ready": {Project: en, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
				}
			}
			saveStage(t, store, "demo", snaps, origins)
			if tc.withSnap {
				p, s := load(t, pt, "use")
				rec, _ := store.Load("demo")
				publish(t, p, s, rec, facts.Facts{})
			}
			res, err := Report(Options{Dir: pt, Store: store})
			if err != nil {
				t.Fatal(err)
			}
			got := statusOf(t, res, "use")
			if got.Status != tc.want {
				t.Fatalf("status %s, want %s (reasons %v detail %q)", got.Status, tc.want, got.Reasons, got.Detail)
			}
			if tc.detail != "" && got.Detail != tc.detail {
				t.Fatalf("detail %q, want %q", got.Detail, tc.detail)
			}
			if !res.HasError() {
				t.Fatal("producer error must keep exit non-zero")
			}
		})
	}
}

func setupValidateFailProducer(t *testing.T, _, en, _ string) {
	t.Helper()
	writeFile(t, filepath.Join(en, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeScene(t, en, "make", badLayoutProducer("make", "ready"))
}

func setupJSONFailProducer(t *testing.T, _, en, _ string) {
	t.Helper()
	writeFile(t, filepath.Join(en, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeFile(t, filepath.Join(en, "scenes", "make.json"), "{")
}

func setupConfigFailProducer(t *testing.T, _, en, _ string) {
	t.Helper()
	writeFile(t, filepath.Join(en, "backstage.json"), `{
		"extends": "../backstage.json",
		"record": {"out": "../escape"}
	}`)
	writeScene(t, en, "make", cleanScene("make", "", "ready"))
}

func TestInvalidDraftDoesNotAbortDuplicate(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "one", cleanScene("one", "", "ready"))
	writeScene(t, dir, "two", badLayoutProducer("two", "ready"))
	writeScene(t, dir, "use", cleanScene("use", "ready", ""))
	res, err := Report(Options{Dir: dir, Store: store})
	if err != nil {
		t.Fatalf("invalid draft must not abort the report: %v", err)
	}
	got := statusOf(t, res, "two")
	if got.Status != Error || !strings.Contains(got.Detail, "also declares vm-end ready") {
		t.Fatalf("draft error row: %+v", got)
	}
	if one := statusOf(t, res, "one"); one.Status == Error {
		t.Fatalf("valid producer must stay a scene row: %+v", one)
	}
	if statusOf(t, res, "use").Status != BlockedStateMissing {
		t.Fatalf("valid producer still owns ready: %+v", statusOf(t, res, "use"))
	}
}

func TestErrorSceneDoesNotAbortGraph(t *testing.T) {
	cases := []struct {
		name string
		file string
		body string
		snap bool
	}{
		{"vm-end-initial", "make", `{
			"name":"make","layout":"solo","vm":"laptop",
			"vm-start":{"mode":"clean"},
			"vm-end":{"snapshot":"initial"},
			"steps":[{"action":"wait","delay-after":0.05}]
		}`, false},
		{"vm-end-initial-with-snap", "make", `{
			"name":"make","layout":"solo","vm":"laptop",
			"vm-start":{"mode":"clean"},
			"vm-end":{"snapshot":"initial"},
			"steps":[{"action":"wait","delay-after":0.05}]
		}`, true},
		{"continue-self", "x", `{
			"name":"x","layout":"solo","vm":"laptop",
			"vm-start":{"mode":"continue","after":"x"},
			"steps":[{"action":"wait","delay-after":0.05}]
		}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store := testStore(t)
			writeBaseProject(t, root)
			en := filepath.Join(root, "en")
			pt := filepath.Join(root, "pt")
			writeFile(t, filepath.Join(en, "backstage.json"), `{"extends":"../backstage.json"}`)
			writeFile(t, filepath.Join(pt, "backstage.json"), `{"extends":"../backstage.json"}`)
			writeFile(t, filepath.Join(en, "scenes", tc.file+".json"), tc.body)
			writeScene(t, pt, "use", cleanScene("use", "ready", ""))
			snaps := map[string]string{"initial": imgInitial}
			if tc.snap {
				snaps["ready"] = imgReady
			}
			saveStage(t, store, "demo", snaps, nil)
			wantUse := BlockedNoProducer
			if tc.snap {
				p, s := load(t, pt, "use")
				rec, _ := store.Load("demo")
				publish(t, p, s, rec, facts.Facts{})
				wantUse = OK
			}
			res, err := Report(Options{Dir: root, Store: store})
			if err != nil {
				t.Fatalf("error scene must not abort: %v", err)
			}
			got := statusOf(t, res, tc.file)
			if got.Status != Error {
				t.Fatalf("status %s, want error", got.Status)
			}
			if use := statusOf(t, res, "use"); use.Status != wantUse {
				t.Fatalf("consumer of ready: %+v, want %s", use, wantUse)
			}
			if !res.HasError() {
				t.Fatal("HasError")
			}
		})
	}
}

func TestInvalidInitialEndIsNotProducer(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	pt := filepath.Join(root, "pt")
	writeFile(t, filepath.Join(pt, "backstage.json"), `{"extends":"../backstage.json"}`)
	writeScene(t, root, "bad", `{
		"name":"bad","layout":"solo","vm":"laptop",
		"vm-start":{"mode":"clean"},
		"vm-end":{"snapshot":"initial"},
		"steps":[{"action":"wait","delay-after":0.05}]
	}`)
	writeScene(t, pt, "use", cleanScene("use", "", ""))
	res, err := Report(Options{Dir: pt, Store: store})
	if err != nil {
		t.Fatalf("invalid initial end must not abort: %v", err)
	}
	if statusOf(t, res, "bad").Status != Error {
		t.Fatalf("bad %+v", statusOf(t, res, "bad"))
	}
	got := statusOf(t, res, "use")
	if got.Status != BlockedNoProducer {
		t.Fatalf("status %s, want %s (reasons %v)", got.Status, BlockedNoProducer, got.Reasons)
	}
	if contains(got.Reasons, BlockedStateMissing) {
		t.Fatalf("initial end must not count as a producer: %v", got.Reasons)
	}
}

func TestOriginErrorRequiresMatchingProject(t *testing.T) {
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
	writeFile(t, filepath.Join(fr, "scenes", "make.json"), "{")
	writeScene(t, pt, "use", cleanScene("use", "ready", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: en, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
	})
	ep, es := load(t, en, "make")
	rec, _ := store.Load("demo")
	publish(t, ep, es, rec, facts.Facts{})
	up, us := load(t, pt, "use")
	publish(t, up, us, rec, facts.Facts{})
	res := report(t, pt, store)
	if !res.HasError() {
		t.Fatal("fr/make must remain a workspace error")
	}
	got := statusOf(t, res, "use")
	if got.Status != OK {
		t.Fatalf("fr/make must not taint origin en/make: %s (reasons %v)", got.Status, got.Reasons)
	}
	if contains(got.Reasons, StaleUpstream) {
		t.Fatalf("stale:upstream leaked from another project: %v", got.Reasons)
	}
}

func TestJSONOmitsSceneOnConfigError(t *testing.T) {
	root := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, root)
	en := filepath.Join(root, "en")
	writeFile(t, filepath.Join(en, "backstage.json"), `{
		"extends": "../backstage.json",
		"record": {"out": "../escape"}
	}`)
	res, err := Report(Options{Dir: root, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, res); err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Errors []map[string]any `json:"errors"`
	}
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Errors) != 1 {
		t.Fatalf("errors %v", parsed.Errors)
	}
	row := parsed.Errors[0]
	if _, ok := row["scene"]; ok {
		t.Fatalf("config error must omit scene: %v", row)
	}
	if row["kind"] != KindConfigError {
		t.Fatalf("kind %v", row["kind"])
	}
}

func TestOriginOtherProjectIsNotUpstream(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "make", cleanScene("make", "", "ready"))
	writeScene(t, dir, "use", cleanScene("use", "ready", ""))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: "/other/proj", Scene: "make", Take: machine.TakeRecording, Image: imgReady},
	})
	p, s := load(t, dir, "use")
	rec, _ := store.Load("demo")
	publish(t, p, s, rec, facts.Facts{})
	got := statusOf(t, report(t, dir, store), "use")
	if got.Status != OK {
		t.Fatalf("status %s, want %s (reasons %v)", got.Status, OK, got.Reasons)
	}
	if contains(got.Reasons, StaleUpstream) {
		t.Fatalf("same scene name in another project leaked upstream: %v", got.Reasons)
	}
}

func sceneNames(res *Result) []string {
	var names []string
	for _, s := range res.Scenes {
		names = append(names, s.Scene)
	}
	return names
}

func chmodTree(t *testing.T, root string, dirMode, fileMode os.FileMode) {
	t.Helper()
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		return os.Chmod(path, fileMode)
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i], dirMode); err != nil {
			t.Fatal(err)
		}
	}
}
