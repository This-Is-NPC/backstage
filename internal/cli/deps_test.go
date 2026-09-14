package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/engine"
	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/take"
)

func TestPlayAndRehearseWithDepsFlag(t *testing.T) {
	if playCmd().Flags().Lookup("with-deps") == nil {
		t.Fatal("play needs --with-deps")
	}
	if rehearseCmd().Flags().Lookup("with-deps") == nil {
		t.Fatal("rehearse needs --with-deps")
	}
}

func TestWithDepsRunsABCWhenAStaleBOk(t *testing.T) {
	dir, store := cliChainABC(t)
	saveCLIChain(t, store, dir, true, true)
	publishCLI(t, dir, store, "a", facts.Facts{InputsSHA256: "not-the-digest"})
	publishCLI(t, dir, store, "b", facts.Facts{})
	var ran []string
	var reserved map[string]bool
	out := &bytes.Buffer{}
	err := runWithDeps(out, filepath.Join(dir, "scenes", "c.json"), engine.Options{Record: true, Speed: 1}, store, func(path string, opts engine.Options) error {
		ran = append(ran, filepath.Base(path))
		reserved = opts.ReservedStages
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ran, ",") != "a.json,b.json,c.json" {
		t.Fatalf("ran %v", ran)
	}
	if !reserved["demo"] {
		t.Fatalf("reserved: %v", reserved)
	}
	if !strings.Contains(out.String(), "upstream re-run") || !strings.Contains(out.String(), ">> ran") {
		t.Fatalf("output: %s", out.String())
	}
}

func TestWithDepsSkipsOKProducer(t *testing.T) {
	dir, store := cliChainABC(t)
	saveCLIChain(t, store, dir, true, false)
	publishCLI(t, dir, store, "a", facts.Facts{})
	var ran []string
	err := runWithDeps(&bytes.Buffer{}, filepath.Join(dir, "scenes", "c.json"), engine.Options{Record: true, Speed: 1}, store, func(path string, opts engine.Options) error {
		ran = append(ran, strings.TrimSuffix(filepath.Base(path), ".json"))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ran, ",") != "b,c" {
		t.Fatalf("ran %v", ran)
	}
}

func TestWithDepsBusyBeforeFirstTake(t *testing.T) {
	dir, store := cliChainABC(t)
	hold, err := store.LockMany("demo")
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	called := false
	err = runWithDeps(&bytes.Buffer{}, filepath.Join(dir, "scenes", "c.json"), engine.Options{Record: true, Speed: 1}, store, func(string, engine.Options) error {
		called = true
		return nil
	})
	if err == nil || !errors.Is(err, machine.ErrBusy) && !strings.Contains(err.Error(), "busy") {
		t.Fatalf("busy: %v", err)
	}
	if called {
		t.Fatal("must not start a take while the stage is busy")
	}
}

func TestWithDepsStopsOnFailureAndListsSaved(t *testing.T) {
	dir, store := cliChainABC(t)
	saveCLIChain(t, store, dir, false, false)
	var ran []string
	out := &bytes.Buffer{}
	err := runWithDeps(out, filepath.Join(dir, "scenes", "c.json"), engine.Options{Record: true, Speed: 1}, store, func(path string, opts engine.Options) error {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		ran = append(ran, name)
		if name == "a" {
			rec, _ := store.Load("demo")
			if rec == nil {
				rec = &machine.Record{
					Schema: machine.Schema, ID: strings.Repeat("ab", 16), Name: "demo",
					Domain: "backstage-test-demo", URI: "qemu:///system",
					Spec: machine.DefaultSpec(), Status: "ready",
					Snapshots: map[string]string{"initial": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
				}
			}
			if rec.Snapshots == nil {
				rec.Snapshots = map[string]string{}
			}
			rec.Snapshots["ready"] = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			if rec.SnapshotOrigins == nil {
				rec.SnapshotOrigins = map[string]machine.SnapshotOrigin{}
			}
			rec.SnapshotOrigins["ready"] = machine.SnapshotOrigin{Project: dir, Scene: "a", Take: machine.TakeRecording, Image: rec.Snapshots["ready"]}
			if err := store.Save(rec); err != nil {
				return err
			}
			return nil
		}
		if name == "b" {
			return errors.New("boom")
		}
		return nil
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err %v", err)
	}
	if strings.Join(ran, ",") != "a,b" {
		t.Fatalf("ran %v", ran)
	}
	text := out.String()
	if !strings.Contains(text, ">> ran ./a") || !strings.Contains(text, ">> failed ./b") || !strings.Contains(text, ">> not run ./c") {
		t.Fatalf("report: %s", text)
	}
	if !strings.Contains(text, ">> saved demo ready bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb") {
		t.Fatalf("saved snapshots: %s", text)
	}
	if strings.Contains(text, ">> saved demo initial") {
		t.Fatalf("unchanged initial must not be listed: %s", text)
	}
}

func TestWithDepsSavedListsImageReplacement(t *testing.T) {
	dir, store := cliChainABC(t)
	saveCLIChain(t, store, dir, true, false)
	out := &bytes.Buffer{}
	replaced := "dddddddddddddddddddddddddddddddd"
	err := runWithDeps(out, filepath.Join(dir, "scenes", "c.json"), engine.Options{Record: true, Speed: 1}, store, func(path string, opts engine.Options) error {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		if name != "a" {
			return errors.New("boom")
		}
		rec, err := store.Load("demo")
		if err != nil {
			return err
		}
		rec.Snapshots["ready"] = replaced
		rec.SnapshotOrigins["ready"] = machine.SnapshotOrigin{Project: dir, Scene: "a", Take: machine.TakeRecording, Image: replaced}
		return store.Save(rec)
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err %v", err)
	}
	text := out.String()
	if !strings.Contains(text, ">> saved demo ready "+replaced) {
		t.Fatalf("replaced image: %s", text)
	}
	if strings.Contains(text, ">> saved demo initial") {
		t.Fatalf("unchanged initial must not be listed: %s", text)
	}
}

func TestWithDepsInterruptDoesNotStartNext(t *testing.T) {
	dir, store := cliChainABC(t)
	ctx, cancel := context.WithCancel(context.Background())
	var ran []string
	out := &bytes.Buffer{}
	err := runWithDeps(out, filepath.Join(dir, "scenes", "c.json"), engine.Options{Context: ctx, Record: true, Speed: 1}, store, func(path string, opts engine.Options) error {
		ran = append(ran, strings.TrimSuffix(filepath.Base(path), ".json"))
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if ExitStatus(err) != 130 {
		t.Fatalf("exit %d, want 130", ExitStatus(err))
	}
	if strings.Join(ran, ",") != "a" {
		t.Fatalf("next producer started: %v", ran)
	}
	text := out.String()
	if !strings.Contains(text, ">> interrupted before ./b") || !strings.Contains(text, ">> not run ./b") || !strings.Contains(text, ">> not run ./c") {
		t.Fatalf("interrupted before: %s", text)
	}
	if strings.Contains(text, ">> failed ./b") {
		t.Fatalf("step that never started is not failed: %s", text)
	}
}

func TestWithDepsEngineInterruptPrintsReportOnce(t *testing.T) {
	dir, store := cliChainABC(t)
	var ran []string
	out := &bytes.Buffer{}
	defer func() {
		if recover() == nil {
			t.Fatal("runner must exit without returning to the loop")
		}
		if strings.Join(ran, ",") != "a" {
			t.Fatalf("next producer started: %v", ran)
		}
		text := out.String()
		if strings.Count(text, ">> interrupted ./a") != 1 {
			t.Fatalf("hook must print the report before exit: %s", text)
		}
		if strings.Contains(text, ">> failed ./a") {
			t.Fatalf("in-progress take is interrupted, not failed: %s", text)
		}
		if !strings.Contains(text, ">> not run ./b") || !strings.Contains(text, ">> not run ./c") {
			t.Fatalf("not run: %s", text)
		}
	}()
	_ = runWithDeps(out, filepath.Join(dir, "scenes", "c.json"), engine.Options{Record: true, Speed: 1}, store, func(path string, opts engine.Options) error {
		ran = append(ran, strings.TrimSuffix(filepath.Base(path), ".json"))
		if opts.OnInterrupt == nil {
			t.Fatal("engine interrupt hook not installed")
		}
		opts.OnInterrupt()
		panic("exit 130")
	})
	t.Fatal("returned to the loop")
}

func TestWithDepsInterruptReportOnceWhenCanceled(t *testing.T) {
	dir, store := cliChainABC(t)
	out := &bytes.Buffer{}
	err := runWithDeps(out, filepath.Join(dir, "scenes", "c.json"), engine.Options{Record: true, Speed: 1}, store, func(path string, opts engine.Options) error {
		if opts.OnInterrupt == nil {
			t.Fatal("engine interrupt hook not installed")
		}
		opts.OnInterrupt()
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	text := out.String()
	if got := strings.Count(text, ">> interrupted ./a"); got != 1 {
		t.Fatalf("report once, got %d: %s", got, text)
	}
	if strings.Contains(text, ">> failed ./a") {
		t.Fatalf("canceled take must not be failed: %s", text)
	}
}

func TestWithDepsCanceledTakeIsInterrupted(t *testing.T) {
	dir, store := cliChainABC(t)
	out := &bytes.Buffer{}
	err := runWithDeps(out, filepath.Join(dir, "scenes", "c.json"), engine.Options{Record: true, Speed: 1}, store, func(string, engine.Options) error {
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if ExitStatus(err) != 130 {
		t.Fatalf("exit %d, want 130", ExitStatus(err))
	}
	text := out.String()
	if !strings.Contains(text, ">> interrupted ./a") {
		t.Fatalf("canceled take is interrupted: %s", text)
	}
	if strings.Contains(text, ">> failed ./a") {
		t.Fatalf("canceled take must not be failed: %s", text)
	}
	if !strings.Contains(text, ">> not run ./b") {
		t.Fatalf("not run: %s", text)
	}
}

func TestExitStatus(t *testing.T) {
	if ExitStatus(nil) != 0 {
		t.Fatal("nil")
	}
	if ExitStatus(errors.New("boom")) != 1 {
		t.Fatal("plain error")
	}
	if ExitStatus(context.Canceled) != 1 {
		t.Fatal("plain canceled")
	}
	hook := fmt.Errorf("hook reset: %w", runExit(t, 3))
	if got := ExitStatus(hook); got != 1 {
		t.Fatalf("wrapped ExitError %d, want 1", got)
	}
	killed := fmt.Errorf("step 1: %w", runSignaled(t))
	if got := ExitStatus(killed); got != 1 {
		t.Fatalf("signaled child %d, want 1", got)
	}
	err := interrupted(context.Canceled)
	if ExitStatus(err) != 130 {
		t.Fatalf("interrupt %d", ExitStatus(err))
	}
	if ExitStatus(fmt.Errorf("wrap: %w", err)) != 130 {
		t.Fatal("wrapped interrupt")
	}
	if ExitStatus(errors.Join(errors.New("other"), err)) != 130 {
		t.Fatal("joined interrupt")
	}
}

func runExit(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("want ExitError, got %v", err)
	}
	if ee.ExitCode() != code {
		t.Fatalf("child exit %d, want %d", ee.ExitCode(), code)
	}
	return err
}

func runSignaled(t *testing.T) error {
	t.Helper()
	err := exec.Command("sh", "-c", "kill -s KILL $$").Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("want ExitError, got %v", err)
	}
	if ee.ExitCode() >= 0 {
		t.Fatalf("signaled child ExitCode %d, want negative", ee.ExitCode())
	}
	return err
}

func TestWithDepsAdoptInterruptBeforeLock(t *testing.T) {
	dir := t.TempDir()
	store := cliStore(t)
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {"laptop": {"stage": "demo"}}
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "make.json"), `{"name":"make","layout":"solo","vm":"laptop","vm-start":{"mode":"clean"},"vm-end":{"snapshot":"ready"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	saveCLIChain(t, store, dir, true, false)
	rec, err := store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	delete(rec.SnapshotOrigins, "ready")
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &blockReader{started: make(chan struct{}), block: make(chan struct{})}
	defer close(r.block)
	var locked, startedTake bool
	d := &depsExec{
		Store: store,
		Out:   &bytes.Buffer{},
		Lock: func(names ...string) (func(), error) {
			locked = true
			return func() {}, nil
		},
		Run: func(string, engine.Options) error {
			startedTake = true
			return nil
		},
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.run(filepath.Join(dir, "scenes", "make.json"), engine.Options{
			Context: ctx, Record: true, Speed: 1, Adopt: true,
			ConfirmAdopt: func(snapshot string) error {
				return confirmSnapshotName(ctx, r, io.Discard, snapshot)
			},
		})
	}()
	select {
	case <-r.started:
	case <-time.After(2 * time.Second):
		t.Fatal("confirm never read")
	}
	cancel()
	select {
	case err := <-errCh:
		if ExitStatus(err) != 130 {
			t.Fatalf("exit %d (%v), want 130", ExitStatus(err), err)
		}
		if locked || startedTake {
			t.Fatal("interrupt at the adopt prompt must not lock or take")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run ignored adopt cancel")
	}
}

func TestWithDepsUsesEachProjectConfig(t *testing.T) {
	root := t.TempDir()
	store := cliStore(t)
	writeFile(t, filepath.Join(root, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {"laptop": {"stage": "demo"}}
	}`)
	en := filepath.Join(root, "en")
	pt := filepath.Join(root, "pt")
	writeFile(t, filepath.Join(en, "backstage.json"), `{"extends":"../backstage.json","env":{"LOCALE":"en"}}`)
	writeFile(t, filepath.Join(pt, "backstage.json"), `{"extends":"../backstage.json","env":{"LOCALE":"pt"}}`)
	writeFile(t, filepath.Join(en, "scenes", "make.json"), `{"name":"make","layout":"solo","vm":"laptop","vm-start":{"mode":"clean"},"vm-end":{"snapshot":"ready"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeFile(t, filepath.Join(pt, "scenes", "use.json"), `{"name":"use","layout":"solo","vm":"laptop","vm-start":{"mode":"clean","snapshot":"ready"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	saveCLIChain(t, store, root, false, false)
	var projects []string
	err := runWithDeps(&bytes.Buffer{}, filepath.Join(pt, "scenes", "use.json"), engine.Options{Record: true, Speed: 1}, store, func(path string, opts engine.Options) error {
		p, err := scene.LoadProject(filepath.Join(filepath.Dir(filepath.Dir(path)), "backstage.json"))
		if err != nil {
			return err
		}
		projects = append(projects, p.Dir+"="+p.Env["LOCALE"])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 || !strings.HasSuffix(projects[0], "=en") || !strings.HasSuffix(projects[1], "=pt") {
		t.Fatalf("projects %v", projects)
	}
}

func TestWithDepsAdoptStaysOnRequested(t *testing.T) {
	dir, store := cliChainABC(t)
	saveCLIChain(t, store, dir, true, true)
	publishCLI(t, dir, store, "a", facts.Facts{InputsSHA256: "not-the-digest"})
	publishCLI(t, dir, store, "b", facts.Facts{})
	var adopt []bool
	var confirm []bool
	err := runWithDeps(&bytes.Buffer{}, filepath.Join(dir, "scenes", "c.json"), engine.Options{
		Record: true, Speed: 1, Adopt: true,
		ConfirmAdopt: func(string) error { return nil },
	}, store, func(path string, opts engine.Options) error {
		adopt = append(adopt, opts.Adopt)
		confirm = append(confirm, opts.ConfirmAdopt != nil)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(adopt) != 3 || adopt[0] || adopt[1] || !adopt[2] {
		t.Fatalf("adopt flags %v", adopt)
	}
	if len(confirm) != 3 || confirm[0] || confirm[1] {
		t.Fatalf("ConfirmAdopt must not reach producers: %v", confirm)
	}
}

func TestWithDepsConfirmAdoptBeforeLock(t *testing.T) {
	dir := t.TempDir()
	store := cliStore(t)
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {"laptop": {"stage": "demo"}}
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "make.json"), `{"name":"make","layout":"solo","vm":"laptop","vm-start":{"mode":"clean"},"vm-end":{"snapshot":"ready"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	saveCLIChain(t, store, dir, true, false)
	rec, err := store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	delete(rec.SnapshotOrigins, "ready")
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}
	var locked bool
	var confirms int
	var runConfirms int
	d := &depsExec{
		Store: store,
		Out:   &bytes.Buffer{},
		Lock: func(names ...string) (func(), error) {
			locked = true
			return func() {}, nil
		},
		Run: func(path string, opts engine.Options) error {
			if opts.ConfirmAdopt != nil {
				if err := opts.ConfirmAdopt("ready"); err != nil {
					return err
				}
				runConfirms++
			}
			return nil
		},
	}
	err = d.run(filepath.Join(dir, "scenes", "make.json"), engine.Options{
		Record: true, Speed: 1, Adopt: true,
		ConfirmAdopt: func(snapshot string) error {
			if locked {
				t.Fatal("typed adopt confirmation must happen before the lock")
			}
			if snapshot != "ready" {
				t.Fatalf("confirm %q", snapshot)
			}
			confirms++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if confirms != 1 {
		t.Fatalf("confirm calls %d, want 1", confirms)
	}
	if runConfirms != 1 {
		t.Fatalf("engine confirm after plan must be a no-op, got %d original-side calls via %d runner calls", confirms, runConfirms)
	}
	if !locked {
		t.Fatal("expected lock after confirm")
	}
}

func TestWithDepsPlanErrorPrintedOnce(t *testing.T) {
	dir := t.TempDir()
	store := cliStore(t)
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {"laptop": {"stage": "demo"}}
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "use.json"), `{"name":"use","layout":"solo","vm":"laptop","vm-start":{"mode":"clean","snapshot":"ghost"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	out := &bytes.Buffer{}
	err := runWithDeps(out, filepath.Join(dir, "scenes", "use.json"), engine.Options{Record: true, Speed: 1}, store, func(string, engine.Options) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected plan error")
	}
	if strings.Contains(out.String(), err.Error()) {
		t.Fatalf("plan error must not be printed before cobra returns it: %s", out.String())
	}
}

func cliChainABC(t *testing.T) (string, *machine.Store) {
	t.Helper()
	dir := t.TempDir()
	store := cliStore(t)
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {"laptop": {"stage": "demo"}}
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "a.json"), `{"name":"a","layout":"solo","vm":"laptop","vm-start":{"mode":"clean"},"vm-end":{"snapshot":"ready"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeFile(t, filepath.Join(dir, "scenes", "b.json"), `{"name":"b","layout":"solo","vm":"laptop","vm-start":{"mode":"clean","snapshot":"ready"},"vm-end":{"snapshot":"done"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeFile(t, filepath.Join(dir, "scenes", "c.json"), `{"name":"c","layout":"solo","vm":"laptop","vm-start":{"mode":"clean","snapshot":"done"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	saveCLIChain(t, store, dir, false, false)
	return dir, store
}

func cliStore(t *testing.T) *machine.Store {
	t.Helper()
	return &machine.Store{Root: filepath.Join(t.TempDir(), "reg"), Cache: filepath.Join(t.TempDir(), "cache")}
}

func saveCLIChain(t *testing.T, store *machine.Store, dir string, ready, done bool) {
	t.Helper()
	snaps := map[string]string{"initial": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	origins := map[string]machine.SnapshotOrigin{}
	if ready {
		snaps["ready"] = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		origins["ready"] = machine.SnapshotOrigin{Project: dir, Scene: "a", Take: machine.TakeRecording, Image: snaps["ready"]}
	}
	if done {
		snaps["done"] = "cccccccccccccccccccccccccccccccc"
		origins["done"] = machine.SnapshotOrigin{Project: dir, Scene: "b", Take: machine.TakeRecording, Image: snaps["done"]}
	}
	r := &machine.Record{
		Schema: machine.Schema, ID: strings.Repeat("ab", 16), Name: "demo",
		Domain: "backstage-test-demo", URI: "qemu:///system",
		Spec: machine.DefaultSpec(), Status: "ready",
		Snapshots: snaps, SnapshotOrigins: origins,
	}
	if err := store.Save(r); err != nil {
		t.Fatal(err)
	}
}

func publishCLI(t *testing.T, dir string, store *machine.Store, name string, f facts.Facts) {
	t.Helper()
	p, err := scene.LoadProject(filepath.Join(dir, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := scene.LoadScene(filepath.Join(dir, "scenes", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := store.Load("demo")
	img := ""
	if s.VMStartMode() == "clean" && rec != nil {
		snap := "initial"
		if s.VMStart != nil && s.VMStart.Snapshot != "" {
			snap = s.VMStart.Snapshot
		}
		img = rec.Snapshots[snap]
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
		snap := "initial"
		if s.VMStart != nil && s.VMStart.Snapshot != "" {
			snap = s.VMStart.Snapshot
		}
		f.StartState = &facts.StartState{Snapshot: snap}
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
	if _, err := sess.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
}
