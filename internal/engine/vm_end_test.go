package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/guest"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/take"
)

func managedEngine(t *testing.T, dir string) (*Engine, *bool) {
	t.Helper()
	e := playEngine(t, dir, nil)
	root := t.TempDir()
	store := &machine.Store{Root: filepath.Join(root, "reg"), Cache: filepath.Join(root, "cache"), Storage: filepath.Join(root, "storage")}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	r := &machine.Record{
		Schema: machine.Schema,
		ID:     "12345678901234567890123456789012",
		Name:   "demo",
		Status: "ready",
		Source: machine.Source{Image: "source-at-start"},
		Snapshots: map[string]string{
			"initial": "restored-initial",
			"ready":   "restored-ready",
		},
	}
	if err := store.Save(r); err != nil {
		t.Fatal(err)
	}
	e.Managed = &machine.Manager{Store: store}
	e.ManagedName = "demo"
	finished := false
	prevBegin := beginManaged
	beginManaged = func(_ *machine.Manager, _ context.Context, _ *machine.Record, mode, snapshot, _, _ string, _ bool) (*guest.Guest, error) {
		g := guest.New("dom", "user", "", "", "")
		g.StartMode = mode
		g.Snapshot = snapshot
		return g, nil
	}
	prevFinish := finishManaged
	finishManaged = func(_ *machine.Manager, rec *machine.Record, _ *guest.Guest, project, name string, recording bool) error {
		finished = true
		rec.Continuity = &machine.Continuity{Project: project, Scene: name, Recording: recording, Session: "sess"}
		return nil
	}
	t.Cleanup(func() {
		beginManaged = prevBegin
		finishManaged = prevFinish
	})
	return e, &finished
}

func vmEndScene(mode, snapshot, after string) *scene.Scene {
	s := &scene.Scene{
		Name:   "demo",
		Layout: "solo",
		VMEnd:  &scene.VMEnd{Snapshot: "saved"},
		Steps:  []scene.Step{{Action: "wait"}},
	}
	if mode != "" {
		s.VMStart = &scene.VMStart{Mode: mode, Snapshot: snapshot, After: after}
	}
	return s
}

func TestCheckVMEndPolicies(t *testing.T) {
	dir := t.TempDir()
	e := playEngine(t, dir, nil)
	s := &scene.Scene{Name: "demo", VMEnd: &scene.VMEnd{Snapshot: "ready"}}
	r := &machine.Record{Snapshots: map[string]string{}}
	if err := e.checkVMEnd(r, s, dir, Options{Record: true}); err != nil {
		t.Fatalf("missing snapshot: %v", err)
	}
	r.Snapshots["ready"] = "img"
	if err := e.checkVMEnd(r, s, dir, Options{Record: true}); err == nil || !strings.Contains(err.Error(), "no origin") {
		t.Fatalf("manual: %v", err)
	}
	asked := false
	if err := e.checkVMEnd(r, s, dir, Options{Record: true, Adopt: true, ConfirmAdopt: func(name string) error {
		asked = true
		if name != "ready" {
			t.Fatalf("confirm %s", name)
		}
		return nil
	}}); err != nil || !asked {
		t.Fatalf("adopt: %v asked=%v", err, asked)
	}
	if err := e.checkVMEnd(r, s, dir, Options{Record: true, Adopt: true, ConfirmAdopt: func(string) error {
		return errors.New("adoption cancelled")
	}}); err == nil {
		t.Fatal("cancelled adopt")
	}
	r.SnapshotOrigins = map[string]machine.SnapshotOrigin{"ready": {Project: "/other", Scene: "x", Take: machine.TakeRecording}}
	if err := e.checkVMEnd(r, s, dir, Options{Record: true}); err == nil || !strings.Contains(err.Error(), "another scene") {
		t.Fatalf("other: %v", err)
	}
	r.SnapshotOrigins["ready"] = machine.SnapshotOrigin{Project: dir, Scene: "demo", Take: machine.TakeRecording}
	if err := e.checkVMEnd(r, s, dir, Options{Record: false}); err == nil || !strings.Contains(err.Error(), "rehearsal cannot replace") {
		t.Fatalf("rehearse recording: %v", err)
	}
	if err := e.checkVMEnd(r, s, dir, Options{Record: false, ReplaceState: true}); err != nil {
		t.Fatalf("replace-state: %v", err)
	}
}

func TestRunRefusesVMEndBeforeBegin(t *testing.T) {
	dir := t.TempDir()
	var ord []string
	e := playEngine(t, dir, &fakeRec{order: &ord})
	root := t.TempDir()
	store := &machine.Store{Root: filepath.Join(root, "reg"), Cache: filepath.Join(root, "cache"), Storage: filepath.Join(root, "storage")}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	r := &machine.Record{
		Schema:    machine.Schema,
		ID:        "12345678901234567890123456789012",
		Name:      "demo",
		Status:    "ready",
		Snapshots: map[string]string{"ready": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}
	if err := store.Save(r); err != nil {
		t.Fatal(err)
	}
	e.Managed = &machine.Manager{Store: store}
	e.ManagedName = "demo"
	s := &scene.Scene{Name: "demo", Layout: "solo", VMEnd: &scene.VMEnd{Snapshot: "ready"}, Steps: []scene.Step{{Action: "wait"}}}
	err := e.Run(s, Options{Record: true, Speed: 0.0001})
	if err == nil || !strings.Contains(err.Error(), "no origin") {
		t.Fatalf("want refuse before begin: %v", err)
	}
	for _, step := range ord {
		if step == "rec" {
			t.Fatalf("recorder started: %v", ord)
		}
	}
}

func TestSaveEndStateRewritesFactsAndKeepsStartImage(t *testing.T) {
	dir := t.TempDir()
	e := playEngine(t, dir, nil)
	e.Managed = &machine.Manager{}
	e.managedRec = &machine.Record{}
	e.leafProject = dir
	e.startImage = "source-at-start"
	e.inputsSHA256 = "digest"
	clip := filepath.Join(dir, "work.mp4")
	if err := os.WriteFile(clip, []byte("clip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.writeClipFacts(clip, &scene.Scene{Name: "demo", VMStart: &scene.VMStart{Mode: "continue", After: "one"}}, "v", facts.ResultOK, nil); err != nil {
		t.Fatal(err)
	}
	if got := readFacts(t, clip); got.EndState != nil {
		t.Fatalf("facts already had end-state: %+v", got.EndState)
	}
	var origin machine.SnapshotOrigin
	prev := replaceTakeState
	replaceTakeState = func(_ *machine.Manager, _ context.Context, _ *machine.Record, name string, o machine.SnapshotOrigin, _ bool) (machine.ReplaceResult, error) {
		origin = o
		if name != "ready" {
			t.Fatalf("snapshot %s", name)
		}
		return machine.ReplaceResult{Image: &machine.Image{ID: "new-image"}}, nil
	}
	t.Cleanup(func() { replaceTakeState = prev })
	s := &scene.Scene{Name: "demo", VMEnd: &scene.VMEnd{Snapshot: "ready"}}
	if err := e.saveEndState(context.Background(), s, clip, Options{Record: true, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	if !e.stateSaved {
		t.Fatal("Finish should be skipped after a committed save")
	}
	if origin.StartImage != "source-at-start" || origin.Take != machine.TakeRecording || origin.InputsSHA256 != "digest" || origin.Project != dir || origin.Scene != "demo" {
		t.Fatalf("origin: %+v", origin)
	}
	got := readFacts(t, clip)
	if got.Result != facts.ResultOK || got.EndState == nil || got.EndState.Snapshot != "ready" || got.EndState.Image != "new-image" || got.EndState.Status != "" {
		t.Fatalf("rewritten facts: %+v", got)
	}
}

func TestSaveEndStateCleanUsesRestoredImage(t *testing.T) {
	e := playEngine(t, t.TempDir(), nil)
	e.Managed = &machine.Manager{}
	e.managedRec = &machine.Record{}
	e.startImage = "restored-snapshot"
	var origin machine.SnapshotOrigin
	prev := replaceTakeState
	replaceTakeState = func(_ *machine.Manager, _ context.Context, _ *machine.Record, _ string, o machine.SnapshotOrigin, _ bool) (machine.ReplaceResult, error) {
		origin = o
		return machine.ReplaceResult{Image: &machine.Image{ID: "img"}}, nil
	}
	t.Cleanup(func() { replaceTakeState = prev })
	s := &scene.Scene{Name: "demo", VMEnd: &scene.VMEnd{Snapshot: "ready"}}
	if err := e.saveEndState(context.Background(), s, "", Options{Record: false, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	if origin.StartImage != "restored-snapshot" || origin.Take != machine.TakeRehearsal {
		t.Fatalf("origin: %+v", origin)
	}
}

func TestSaveEndStateCaptureFailureRewritesFacts(t *testing.T) {
	dir := t.TempDir()
	e := playEngine(t, dir, nil)
	e.Managed = &machine.Manager{}
	e.managedRec = &machine.Record{}
	clip := filepath.Join(dir, "work.mp4")
	if err := os.WriteFile(clip, []byte("clip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.writeClipFacts(clip, &scene.Scene{Name: "demo"}, "v", facts.ResultOK, nil); err != nil {
		t.Fatal(err)
	}
	prev := replaceTakeState
	replaceTakeState = func(*machine.Manager, context.Context, *machine.Record, string, machine.SnapshotOrigin, bool) (machine.ReplaceResult, error) {
		return machine.ReplaceResult{}, errors.New("capture failed")
	}
	t.Cleanup(func() { replaceTakeState = prev })
	s := &scene.Scene{Name: "demo", VMEnd: &scene.VMEnd{Snapshot: "ready"}}
	err := e.saveEndState(context.Background(), s, clip, Options{Record: true, Version: "v"})
	if !errors.Is(err, ErrCaptureFailed) {
		t.Fatalf("expected capture failure: %v", err)
	}
	if e.stateSaved {
		t.Fatal("failed capture marked the state saved")
	}
	got := readFacts(t, clip)
	if got.Result != facts.ResultCaptureFailed || got.EndState == nil || got.EndState.Snapshot != "ready" || got.EndState.Status != "failed" || got.EndState.Image != "" {
		t.Fatalf("capture-failed facts: %+v", got)
	}
}

func TestSaveEndStateCollectWarningKeepsOK(t *testing.T) {
	dir := t.TempDir()
	e := playEngine(t, dir, nil)
	e.Managed = &machine.Manager{}
	e.managedRec = &machine.Record{}
	clip := filepath.Join(dir, "work.mp4")
	if err := os.WriteFile(clip, []byte("clip"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := replaceTakeState
	replaceTakeState = func(*machine.Manager, context.Context, *machine.Record, string, machine.SnapshotOrigin, bool) (machine.ReplaceResult, error) {
		return machine.ReplaceResult{Image: &machine.Image{ID: "new"}, Warning: "pending cleanup: busy"}, nil
	}
	t.Cleanup(func() { replaceTakeState = prev })
	s := &scene.Scene{Name: "demo", VMEnd: &scene.VMEnd{Snapshot: "ready"}}
	if err := e.saveEndState(context.Background(), s, clip, Options{Record: true, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	got := readFacts(t, clip)
	if got.Result != facts.ResultOK || got.EndState == nil || got.EndState.Status == "failed" || got.EndState.Image != "new" {
		t.Fatalf("collect warning must not rewrite success: %+v", got)
	}
}

func TestSaveEndStateFactsAfterCommitFail(t *testing.T) {
	dir := t.TempDir()
	e := playEngine(t, dir, nil)
	e.Managed = &machine.Manager{}
	e.managedRec = &machine.Record{}
	clip := filepath.Join(dir, "work.mp4")
	if err := os.WriteFile(clip, []byte("clip"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := replaceTakeState
	replaceTakeState = func(*machine.Manager, context.Context, *machine.Record, string, machine.SnapshotOrigin, bool) (machine.ReplaceResult, error) {
		if err := os.Chmod(dir, 0o500); err != nil {
			return machine.ReplaceResult{}, err
		}
		return machine.ReplaceResult{Image: &machine.Image{ID: "new"}}, nil
	}
	t.Cleanup(func() {
		replaceTakeState = prev
		_ = os.Chmod(dir, 0o700)
	})
	s := &scene.Scene{Name: "demo", VMEnd: &scene.VMEnd{Snapshot: "ready"}}
	err := e.saveEndState(context.Background(), s, clip, Options{Record: true, Version: "v"})
	_ = os.Chmod(dir, 0o700)
	if err == nil {
		t.Fatal("expected facts rewrite failure")
	}
	if !e.stateSaved {
		t.Fatal("committed snapshot must stay after a facts rewrite failure")
	}
}

func TestInterruptDuringCaptureWaitsForCleanup(t *testing.T) {
	dir := t.TempDir()
	e := playEngine(t, dir, nil)
	e.Managed = &machine.Manager{}
	e.managedRec = &machine.Record{}
	e.leafProject = dir
	sess, err := take.Begin(e.Project, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sess.Clip(), []byte("clip"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.takeSess.Store(sess)
	partial := filepath.Join(t.TempDir(), "partial.qcow2")
	started := make(chan struct{})
	waitEntered := make(chan struct{})
	release := make(chan struct{})
	prevWait := onWaitCapture
	onWaitCapture = func() {
		select {
		case <-waitEntered:
		default:
			close(waitEntered)
		}
	}
	prev := replaceTakeState
	replaceTakeState = func(_ *machine.Manager, ctx context.Context, _ *machine.Record, _ string, _ machine.SnapshotOrigin, _ bool) (machine.ReplaceResult, error) {
		if err := os.WriteFile(partial, []byte("partial"), 0o600); err != nil {
			return machine.ReplaceResult{}, err
		}
		close(started)
		<-ctx.Done()
		select {
		case <-waitEntered:
		case <-release:
		}
		if err := os.Remove(partial); err != nil {
			return machine.ReplaceResult{}, err
		}
		return machine.ReplaceResult{}, ctx.Err()
	}
	t.Cleanup(func() {
		close(release)
		replaceTakeState = prev
		onWaitCapture = prevWait
	})
	s := &scene.Scene{Name: "demo", Layout: "solo", VMEnd: &scene.VMEnd{Snapshot: "ready"}, Steps: []scene.Step{{Action: "wait"}}}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- e.saveEndState(ctx, s, sess.Clip(), Options{Record: true, Version: "t"})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("capture did not start")
	}
	returned := make(chan struct{})
	go func() {
		cancel()
		e.cleanupOnInterrupt(nil)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("onInterrupt did not return after cleanup")
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatalf("partial remains: %v", err)
	}
	attempts, err := filepath.Glob(filepath.Join(dir, "recordings", ".takes", "demo", "attempts", "*", "clip.mp4"))
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts: %v %v", attempts, err)
	}
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("saveEndState succeeded after interrupt")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("saveEndState did not return")
	}
}

func TestRunUsesStartImageFromSourceUnlessClean(t *testing.T) {
	for _, tc := range []struct {
		name, mode, snapshot, after, want string
	}{
		{"reuse", "reuse", "", "", "source-at-start"},
		{"continue", "continue", "", "one", "source-at-start"},
		{"clean", "clean", "ready", "", "restored-ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			e, _ := managedEngine(t, dir)
			var origin machine.SnapshotOrigin
			prev := replaceTakeState
			replaceTakeState = func(_ *machine.Manager, _ context.Context, _ *machine.Record, _ string, o machine.SnapshotOrigin, _ bool) (machine.ReplaceResult, error) {
				origin = o
				return machine.ReplaceResult{Image: &machine.Image{ID: "new-image"}}, nil
			}
			t.Cleanup(func() { replaceTakeState = prev })
			s := vmEndScene(tc.mode, tc.snapshot, tc.after)
			if err := e.Run(s, Options{Record: true, Speed: 0.0001, Version: "v"}); err != nil {
				t.Fatal(err)
			}
			if origin.StartImage != tc.want {
				t.Fatalf("start-image = %q, want %q", origin.StartImage, tc.want)
			}
		})
	}
}

func TestRunSkipsFinishAfterVMEndSave(t *testing.T) {
	dir := t.TempDir()
	e, finished := managedEngine(t, dir)
	prev := replaceTakeState
	replaceTakeState = func(_ *machine.Manager, _ context.Context, _ *machine.Record, _ string, _ machine.SnapshotOrigin, _ bool) (machine.ReplaceResult, error) {
		return machine.ReplaceResult{Image: &machine.Image{ID: "new-image"}}, nil
	}
	t.Cleanup(func() { replaceTakeState = prev })
	if err := e.Run(vmEndScene("reuse", "", ""), Options{Record: true, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	if *finished {
		t.Fatal("Finish wrote continuity after a committed vm-end")
	}
	if e.managedRec != nil && e.managedRec.Continuity != nil {
		t.Fatalf("continuity: %+v", e.managedRec.Continuity)
	}
}

func TestRunOutPathRewritesEndState(t *testing.T) {
	dir := t.TempDir()
	e, _ := managedEngine(t, dir)
	clip := filepath.Join(dir, "work.mp4")
	prev := replaceTakeState
	replaceTakeState = func(_ *machine.Manager, _ context.Context, _ *machine.Record, _ string, _ machine.SnapshotOrigin, _ bool) (machine.ReplaceResult, error) {
		return machine.ReplaceResult{Image: &machine.Image{ID: "new-image"}}, nil
	}
	t.Cleanup(func() { replaceTakeState = prev })
	if err := e.Run(vmEndScene("reuse", "", ""), Options{Record: true, OutPath: clip, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	got := readFacts(t, clip)
	if got.Result != facts.ResultOK || got.EndState == nil || got.EndState.Snapshot != "saved" || got.EndState.Image != "new-image" {
		t.Fatalf("outpath facts: %+v", got)
	}
}

func TestPlayReadonlyWorkDirDoesNotPublish(t *testing.T) {
	dir := t.TempDir()
	e, _ := managedEngine(t, dir)
	var work string
	prev := replaceTakeState
	replaceTakeState = func(_ *machine.Manager, _ context.Context, _ *machine.Record, _ string, _ machine.SnapshotOrigin, _ bool) (machine.ReplaceResult, error) {
		if sess := e.takeSess.Load(); sess != nil {
			work = filepath.Dir(sess.Clip())
			if err := os.Chmod(work, 0o555); err != nil {
				return machine.ReplaceResult{}, err
			}
		}
		return machine.ReplaceResult{}, errors.New("no space left on device")
	}
	t.Cleanup(func() {
		replaceTakeState = prev
		if work != "" {
			_ = os.Chmod(work, 0o755)
		}
		_ = filepath.Walk(filepath.Join(dir, "recordings"), func(path string, _ os.FileInfo, err error) error {
			if err == nil {
				_ = os.Chmod(path, 0o755)
			}
			return nil
		})
	})
	err := e.Run(vmEndScene("reuse", "", ""), Options{Record: true, Speed: 0.0001, Version: "v"})
	if !errors.Is(err, ErrCaptureFailed) {
		t.Fatalf("capture failed: %v", err)
	}
	if _, err := take.Open(e.Project, "demo"); err == nil {
		t.Fatal("published a take whose capture failed")
	}
	attempts, err := filepath.Glob(filepath.Join(dir, "recordings", ".takes", "demo", "attempts", "*", "clip.mp4"))
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts: %v %v", attempts, err)
	}
	got := readFacts(t, attempts[0])
	if got.Result != facts.ResultCaptureFailed || got.EndState == nil || got.EndState.Status != "failed" {
		t.Fatalf("attempt facts: %+v", got)
	}
}

func TestPlayReadonlyWorkDirAfterCommitDoesNotPublish(t *testing.T) {
	dir := t.TempDir()
	e, finished := managedEngine(t, dir)
	var work string
	prev := replaceTakeState
	replaceTakeState = func(_ *machine.Manager, _ context.Context, _ *machine.Record, _ string, _ machine.SnapshotOrigin, _ bool) (machine.ReplaceResult, error) {
		if sess := e.takeSess.Load(); sess != nil {
			work = filepath.Dir(sess.Clip())
			if err := os.Chmod(work, 0o555); err != nil {
				return machine.ReplaceResult{}, err
			}
		}
		return machine.ReplaceResult{Image: &machine.Image{ID: "new-image"}}, nil
	}
	t.Cleanup(func() {
		replaceTakeState = prev
		_ = filepath.Walk(filepath.Join(dir, "recordings"), func(path string, _ os.FileInfo, err error) error {
			if err == nil {
				_ = os.Chmod(path, 0o755)
			}
			return nil
		})
	})
	err := e.Run(vmEndScene("reuse", "", ""), Options{Record: true, Speed: 0.0001, Version: "v"})
	if err == nil {
		t.Fatal("expected facts rewrite failure")
	}
	if errors.Is(err, ErrCaptureFailed) {
		t.Fatalf("committed capture should not be ErrCaptureFailed: %v", err)
	}
	if *finished {
		t.Fatal("Finish ran after a committed save")
	}
	if _, err := take.Open(e.Project, "demo"); err == nil {
		t.Fatal("published after a failed facts rewrite")
	}
}

func TestRehearsalWithVMEndComputesDigest(t *testing.T) {
	dir := t.TempDir()
	e := playEngine(t, dir, nil)
	s := &scene.Scene{
		Name:   "demo",
		Layout: "solo",
		Inputs: []string{"missing.txt"},
		VMEnd:  &scene.VMEnd{Snapshot: "ready"},
		Steps:  []scene.Step{{Action: "wait"}},
	}
	err := e.Run(s, Options{Record: false, Speed: 0.0001})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("rehearsal with vm-end should digest inputs: %v", err)
	}
}
