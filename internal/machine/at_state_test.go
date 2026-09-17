package machine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedCapturedState(t *testing.T, snapshot string) (*Manager, *Record, string) {
	t.Helper()
	m, r := captureReady(t, "demo")
	result, err := m.ReplaceSnapshot(context.Background(), r, snapshot, testOrigin("/p", "s"), false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Image == nil || r.AtState == nil {
		t.Fatalf("at-state after capture: %+v %+v", result, r.AtState)
	}
	return m, r, result.Image.ID
}

func attachStageRunner(m *Manager, r *Record, state *string, creates *int) {
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			if len(args) > 0 && args[0] == "info" {
				return `[{"filename":"` + r.Disk + `"}]`, nil
			}
			if len(args) > 0 && args[0] == "create" && creates != nil {
				*creates++
			}
			return "", os.WriteFile(args[len(args)-1], []byte("overlay"), 0o600)
		}
		switch args[2] {
		case "domuuid":
			return uuid(r.ID), nil
		case "domstate":
			return *state, nil
		case "define", "undefine":
			return "", nil
		case "shutdown", "destroy":
			*state = "shut off"
			return "", nil
		case "list":
			return "", nil
		default:
			return "", nil
		}
	})
}

func loadedAtState(t *testing.T, m *Manager) *AtState {
	t.Helper()
	got, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	return got.AtState
}

func TestCaptureDoesNotMutateActiveFingerprint(t *testing.T) {
	m, r := captureReady(t, "demo")
	before, err := readAtState(r, "x", "y")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureStageImage(m, context.Background(), r, true, 8); err != nil {
		t.Fatal(err)
	}
	after, err := readAtState(r, "x", "y")
	if err != nil {
		t.Fatal(err)
	}
	if before.Disk != after.Disk || before.NVRAM != after.NVRAM {
		t.Fatalf("capture changed live files: %+v -> %+v", before, after)
	}
}

func TestBeginSkipRestoreAtMatchingState(t *testing.T) {
	stubBootGuest(t)
	m, r, image := seedCapturedState(t, "ready")
	creates := 0
	state := "shut off"
	attachStageRunner(m, r, &state, &creates)
	if _, err := m.Begin(context.Background(), r, "clean", "ready", "", "/p", true); err != nil {
		t.Fatal(err)
	}
	if creates != 0 {
		t.Fatalf("activate ran %d times", creates)
	}
	if m.StartTimes.RestoreSkipped == nil || !*m.StartTimes.RestoreSkipped {
		t.Fatalf("restore-skipped: %+v", m.StartTimes)
	}
	if m.StartTimes.RestoreStopSeconds != nil || m.StartTimes.RestoreActivateSeconds != nil {
		t.Fatalf("restore timings: %+v", m.StartTimes)
	}
	if r.Snapshots["ready"] != image {
		t.Fatalf("start-image mapping %s want %s", r.Snapshots["ready"], image)
	}
	log := readProvisionLog(t, m, "demo")
	if !strings.Contains(log, "timing restore-skipped true") {
		t.Fatalf("log: %s", log)
	}
	if loadedAtState(t, m) != nil {
		t.Fatal("skip left at-state")
	}
}

func TestBeginMappingMismatchRestores(t *testing.T) {
	stubBootGuest(t)
	m, r, image := seedCapturedState(t, "ready")
	other := randomID()
	writeCatalogImage(t, m, other)
	r.Snapshots["ready"] = other
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if r.AtState == nil || r.AtState.Image != image || !r.AtState.match(r) {
		t.Fatalf("precondition: %+v", r.AtState)
	}
	creates := 0
	state := "shut off"
	attachStageRunner(m, r, &state, &creates)
	if _, err := m.Begin(context.Background(), r, "clean", "ready", "", "/p", true); err != nil {
		t.Fatal(err)
	}
	if creates == 0 {
		t.Fatal("expected restore when the mapping no longer names at-state.image")
	}
	if loadedAtState(t, m) != nil {
		t.Fatal("mapping mismatch left at-state")
	}
}

func TestBeginSkipStillRefusesRehearsalOrigin(t *testing.T) {
	m, r, _ := seedCapturedState(t, "ready")
	r.SnapshotOrigins["ready"] = SnapshotOrigin{Project: "/p", Scene: "s", Take: TakeRehearsal, Image: r.Snapshots["ready"]}
	r.Continuity = &Continuity{Project: "/p", Scene: "prev", Recording: true, Session: "boot\nhypr"}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	creates := 0
	state := "shut off"
	attachStageRunner(m, r, &state, &creates)
	_, err := m.Begin(context.Background(), r, "clean", "ready", "", "/p", true)
	if err == nil || !strings.Contains(err.Error(), "rehearsal") {
		t.Fatalf("recording from rehearsal: %v", err)
	}
	if creates != 0 {
		t.Fatal("refused begin still activated")
	}
	if r.Continuity == nil || r.Continuity.Scene != "prev" {
		t.Fatal("consumed continuity before refusing")
	}
	if loadedAtState(t, m) == nil {
		t.Fatal("cleared at-state while refusing")
	}
}

func TestBeginSkipDoesNotWaitForCatalog(t *testing.T) {
	stubBootGuest(t)
	m, r, _ := seedCapturedState(t, "ready")
	state := "shut off"
	attachStageRunner(m, r, &state, nil)
	hold, err := m.Store.LockMany("image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	done := make(chan error, 1)
	go func() {
		_, err := m.Begin(context.Background(), r, "clean", "ready", "", "/p", true)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("skip waited for image-catalog")
	}
}

func TestBeginFingerprintMismatchRestores(t *testing.T) {
	stubBootGuest(t)
	for _, tc := range []struct {
		name string
		mut  func(*AtState)
	}{
		{"inode", func(a *AtState) { a.Disk.Inode++ }},
		{"size", func(a *AtState) { a.Disk.Size++ }},
		{"mtime", func(a *AtState) { a.Disk.MtimeNs++ }},
		{"ctime", func(a *AtState) { a.Disk.CtimeNs++ }},
		{"nvram", func(a *AtState) { a.NVRAM.CtimeNs++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, r, _ := seedCapturedState(t, "ready")
			tc.mut(r.AtState)
			if err := m.Store.Save(r); err != nil {
				t.Fatal(err)
			}
			creates := 0
			state := "shut off"
			attachStageRunner(m, r, &state, &creates)
			if _, err := m.Begin(context.Background(), r, "clean", "ready", "", "/p", true); err != nil {
				t.Fatal(err)
			}
			if creates == 0 {
				t.Fatal("expected restore")
			}
			if loadedAtState(t, m) != nil {
				t.Fatal("mismatch left at-state")
			}
		})
	}
}

func TestBeginCtimeChmodForcesRestore(t *testing.T) {
	stubBootGuest(t)
	m, r, _ := seedCapturedState(t, "ready")
	if err := os.Chmod(r.Disk, 0o640); err != nil {
		t.Fatal(err)
	}
	creates := 0
	state := "shut off"
	attachStageRunner(m, r, &state, &creates)
	if _, err := m.Begin(context.Background(), r, "clean", "ready", "", "/p", true); err != nil {
		t.Fatal(err)
	}
	if creates == 0 {
		t.Fatal("expected restore after chmod")
	}
	if loadedAtState(t, m) != nil {
		t.Fatal("chmod left at-state")
	}
}

func TestBeginRunningDomainForcesRestore(t *testing.T) {
	stubBootGuest(t)
	m, r, _ := seedCapturedState(t, "ready")
	creates := 0
	state := "running"
	attachStageRunner(m, r, &state, &creates)
	if _, err := m.Begin(context.Background(), r, "clean", "ready", "", "/p", true); err != nil {
		t.Fatal(err)
	}
	if creates == 0 {
		t.Fatal("expected restore")
	}
	if loadedAtState(t, m) != nil {
		t.Fatal("running domain left at-state")
	}
}

func TestOldBinaryStartTouchesDiskThenBeginRestores(t *testing.T) {
	stubBootGuest(t)
	m, r, _ := seedCapturedState(t, "ready")
	if err := os.WriteFile(r.Disk, append([]byte("disk"), 'x'), 0o600); err != nil {
		t.Fatal(err)
	}
	creates := 0
	state := "shut off"
	attachStageRunner(m, r, &state, &creates)
	if _, err := m.Begin(context.Background(), r, "clean", "ready", "", "/p", true); err != nil {
		t.Fatal(err)
	}
	if creates == 0 {
		t.Fatal("expected restore after old start")
	}
	if loadedAtState(t, m) != nil {
		t.Fatal("old start left at-state")
	}
}

func TestClearAtStateEvents(t *testing.T) {
	stubBootGuest(t)
	t.Run("start", func(t *testing.T) {
		m, r, _ := seedCapturedState(t, "ready")
		state := "shut off"
		attachStageRunner(m, r, &state, nil)
		if _, err := m.Start(context.Background(), r); err != nil {
			t.Fatal(err)
		}
		if loadedAtState(t, m) != nil {
			t.Fatal("Start left at-state")
		}
	})
	t.Run("restore", func(t *testing.T) {
		m, r, _ := seedCapturedState(t, "ready")
		state := "shut off"
		attachStageRunner(m, r, &state, nil)
		if err := m.Restore(context.Background(), r, "ready"); err != nil {
			t.Fatal(err)
		}
		if loadedAtState(t, m) != nil {
			t.Fatal("Restore left at-state")
		}
	})
	t.Run("snapshot", func(t *testing.T) {
		m, r, _ := seedCapturedState(t, "ready")
		if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
			t.Fatal(err)
		}
		if loadedAtState(t, m) != nil {
			t.Fatal("manual snapshot left at-state")
		}
	})
	t.Run("snapshot-delete", func(t *testing.T) {
		m, r, _ := seedCapturedState(t, "ready")
		if _, err := m.DeleteSnapshot(r, "ready"); err != nil {
			t.Fatal(err)
		}
		if loadedAtState(t, m) != nil {
			t.Fatal("DeleteSnapshot left at-state")
		}
	})
	t.Run("snapshot-delete-other", func(t *testing.T) {
		m, r, image := seedCapturedState(t, "ready")
		other := randomID()
		writeCatalogImage(t, m, other)
		r.Snapshots["other"] = other
		if err := m.Store.Save(r); err != nil {
			t.Fatal(err)
		}
		if _, err := m.DeleteSnapshot(r, "other"); err != nil {
			t.Fatal(err)
		}
		got := loadedAtState(t, m)
		if got == nil || got.Snapshot != "ready" || got.Image != image {
			t.Fatalf("other delete cleared at-state: %+v", got)
		}
	})
	t.Run("replace-other", func(t *testing.T) {
		m, r, _ := seedCapturedState(t, "ready")
		result, err := m.ReplaceSnapshot(context.Background(), r, "next", testOrigin("/p", "s"), false)
		if err != nil {
			t.Fatal(err)
		}
		got := loadedAtState(t, m)
		if got == nil || got.Snapshot != "next" || got.Image != result.Image.ID {
			t.Fatalf("replace at-state: %+v", got)
		}
	})
	t.Run("reuse", func(t *testing.T) {
		m, r, _ := seedCapturedState(t, "ready")
		state := "shut off"
		attachStageRunner(m, r, &state, nil)
		if _, err := m.Begin(context.Background(), r, "reuse", "", "", "/p", true); err != nil {
			t.Fatal(err)
		}
		if loadedAtState(t, m) != nil {
			t.Fatal("reuse left at-state")
		}
	})
	t.Run("clean-other", func(t *testing.T) {
		m, r, _ := seedCapturedState(t, "ready")
		initial := randomID()
		writeCatalogImage(t, m, initial)
		r.Snapshots["initial"] = initial
		if err := m.Store.Save(r); err != nil {
			t.Fatal(err)
		}
		state := "shut off"
		attachStageRunner(m, r, &state, nil)
		if _, err := m.Begin(context.Background(), r, "clean", "initial", "", "/p", true); err != nil {
			t.Fatal(err)
		}
		if loadedAtState(t, m) != nil {
			t.Fatal("clean of another snapshot left at-state")
		}
	})
	t.Run("delete", func(t *testing.T) {
		m, r, _ := seedCapturedState(t, "ready")
		state := "shut off"
		attachStageRunner(m, r, &state, nil)
		if err := m.Delete(context.Background(), r); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Store.Load("demo"); err == nil {
			t.Fatal("delete left record")
		}
	})
}

func TestRecoverJournalClearsAtState(t *testing.T) {
	m, r := restoreReady(t)
	next := *r
	next.Disk = m.diskPath(r.ID, "-next.qcow2")
	next.NVRAM = m.diskPath(r.ID, "-next.fd")
	for _, p := range []string{next.Disk, next.NVRAM} {
		if err := os.WriteFile(p, []byte("next"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	next.AtState = &AtState{Snapshot: "ready", Image: "img"}
	creds, err := m.Store.Credentials(r.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(m.Store.Dir(r.Name), "activate.json"), activation{Old: *r, Next: next, Credentials: creds}); err != nil {
		t.Fatal(err)
	}
	m.Runner = virshState(r, "shut off", nil)
	if err := m.Recover(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if r.AtState != nil {
		t.Fatalf("in-memory: %+v", r.AtState)
	}
	if loadedAtState(t, m) != nil {
		t.Fatal("journal recover left at-state")
	}
}

func TestReplaceSnapshotErrCommittedWritesAtState(t *testing.T) {
	m, r := captureReady(t, "demo")
	commitStageRecord = func(s *Store, rec *Record) error {
		prev := syncParentDir
		syncParentDir = func(*os.File) error { return errors.New("dir sync failed") }
		defer func() { syncParentDir = prev }()
		return s.Save(rec)
	}
	t.Cleanup(func() { commitStageRecord = (*Store).Save })
	result, err := m.ReplaceSnapshot(context.Background(), r, "ready", testOrigin("/p", "s"), false)
	if err != nil {
		t.Fatal(err)
	}
	got := loadedAtState(t, m)
	if got == nil || got.Snapshot != "ready" || got.Image != result.Image.ID {
		t.Fatalf("ErrCommitted at-state: %+v", got)
	}
}

func TestReplaceSnapshotFailedCommitOmitsAtState(t *testing.T) {
	m, r := captureReady(t, "demo")
	commitStageRecord = func(*Store, *Record) error {
		return errors.New("disk full")
	}
	t.Cleanup(func() { commitStageRecord = (*Store).Save })
	if _, err := m.ReplaceSnapshot(context.Background(), r, "ready", testOrigin("/p", "s"), false); err == nil {
		t.Fatal("expected commit failure")
	}
	if r.AtState != nil {
		t.Fatalf("in-memory at-state: %+v", r.AtState)
	}
	got, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got.AtState != nil {
		t.Fatalf("disk at-state: %+v", got.AtState)
	}
}

func TestManualSnapshotDoesNotWriteAtState(t *testing.T) {
	m, r := captureReady(t, "demo")
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	if loadedAtState(t, m) != nil {
		t.Fatal("manual snapshot wrote at-state")
	}
}

func TestDeltaAfterSkipUsesRealBacking(t *testing.T) {
	stubBootGuest(t)
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	var usedB bool
	attachDeltaRunner(m, r, []string{parent.Disk}, func(args []string) error {
		if strings.Contains(strings.Join(args, " "), "-B "+parent.Disk) {
			usedB = true
		}
		return os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
	})
	if _, err := m.ReplaceSnapshot(context.Background(), r, "ready", testOrigin("/p", "s"), false); err != nil {
		t.Fatal(err)
	}
	state := "shut off"
	creates := 0
	attachStageRunner(m, r, &state, &creates)
	if _, err := m.Begin(context.Background(), r, "clean", "ready", "", "/p", true); err != nil {
		t.Fatal(err)
	}
	if creates != 0 {
		t.Fatal("skip activated")
	}
	usedB = false
	attachDeltaRunner(m, r, []string{parent.Disk}, func(args []string) error {
		if strings.Contains(strings.Join(args, " "), "-B "+parent.Disk) {
			usedB = true
		}
		return os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
	})
	if _, err := m.ReplaceSnapshot(context.Background(), r, "next", testOrigin("/p", "s"), false); err != nil {
		t.Fatal(err)
	}
	if !usedB {
		t.Fatal("delta after skip did not use the live backing")
	}
	child, err := m.Store.Image(r.Snapshots["next"])
	if err != nil || child.Parent != parent.ID {
		t.Fatalf("parent: %+v %v", child, err)
	}
}

func TestDoctorReportsAtState(t *testing.T) {
	m, r, _ := seedCapturedState(t, "ready")
	ctx := context.Background()
	if got := doctorAtState(m, ctx); got != "ready (fingerprint ok)" {
		t.Fatalf("ok: %q", got)
	}
	state := "running"
	attachStageRunner(m, r, &state, nil)
	if got := doctorAtState(m, ctx); got != "ready (domain running; next clean start restores)" {
		t.Fatalf("running: %q", got)
	}
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, _ string, args ...string) (string, error) {
		if len(args) > 2 && args[2] == "domuuid" {
			return uuid(r.ID), nil
		}
		return "", errors.New("domstate failed")
	})
	if got := doctorAtState(m, ctx); got != "ready (state unknown)" {
		t.Fatalf("unknown: %q", got)
	}
	state = "shut off"
	attachStageRunner(m, r, &state, nil)
	r.AtState.Disk.CtimeNs++
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if got := doctorAtState(m, ctx); got != "ready (fingerprint stale)" {
		t.Fatalf("stale: %q", got)
	}
}

func doctorAtState(m *Manager, ctx context.Context) string {
	for _, c := range m.atStateChecks(ctx) {
		if c.Name == "at-state" {
			return c.Detail
		}
	}
	return ""
}
