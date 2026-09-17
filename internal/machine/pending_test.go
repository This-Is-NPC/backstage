package machine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func readyStage(t *testing.T, m *Manager, name string) *Record {
	t.Helper()
	r := testRecord(name)
	r.Disk = m.diskPath(r.ID, ".qcow2")
	r.NVRAM = m.diskPath(r.ID, ".fd")
	for _, path := range []string{r.Disk, r.NVRAM} {
		if err := os.WriteFile(path, []byte("disk"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	key := filepath.Join(m.Store.Dir(name), "id_ed25519")
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
	if err := m.Store.SaveCredentials(name, Credentials{Password: "pw", Key: key}); err != nil {
		t.Fatal(err)
	}
	return r
}

func writePendingMarker(t *testing.T, m *Manager, p pendingCapture) {
	t.Helper()
	if err := m.writePending(p); err != nil {
		t.Fatal(err)
	}
}

func managedPending(stage, id string, m *Manager) pendingCapture {
	if id == "" {
		id = randomID()
	}
	return pendingCapture{
		ID: id, Stage: stage,
		Disk: m.diskPath(id, "-image.qcow2"), NVRAM: m.diskPath(id, "-image.fd"),
		Keys: filepath.Join(m.Store.Root, "images", id), Created: time.Now().UTC(),
	}
}

func writeManagedLeftovers(t *testing.T, p pendingCapture) {
	t.Helper()
	if err := os.WriteFile(p.Disk, []byte("leftover"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.NVRAM, []byte("vars"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.Keys, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotReadsBackingChainAfterStop(t *testing.T) {
	m := testManager(t)
	r := readyStage(t, m, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	state := "running"
	var mu sync.Mutex
	var qemuWhileRunning bool
	var firstInfoState string
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		mu.Lock()
		cur := state
		mu.Unlock()
		if bin == "qemu-img" {
			if cur == "running" {
				mu.Lock()
				qemuWhileRunning = true
				mu.Unlock()
			}
			if len(args) > 0 && args[0] == "info" {
				mu.Lock()
				if firstInfoState == "" {
					firstInfoState = cur
				}
				mu.Unlock()
				return chainInfo(r.Disk, parent.Disk), nil
			}
			return "", os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
		}
		switch args[2] {
		case "domuuid":
			return uuid(r.ID), nil
		case "domstate":
			return cur, nil
		case "shutdown", "destroy":
			mu.Lock()
			state = "shut off"
			mu.Unlock()
			return "", nil
		default:
			return "", fmtUnexpected(bin, args)
		}
	})
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	if qemuWhileRunning {
		t.Fatal("qemu-img ran while the domain was running")
	}
	if firstInfoState != "shut off" {
		t.Fatalf("first qemu-img info at state %q", firstInfoState)
	}
}

func fmtUnexpected(bin string, args []string) error {
	return errors.New("unexpected " + bin + " " + strings.Join(args, " "))
}

func TestCollectKeepsPendingParentAndFiles(t *testing.T) {
	m := testManager(t)
	readyStage(t, m, "demo")
	parent := catalogImage(t, m, "")
	unused := catalogImage(t, m, "")
	p := managedPending("demo", "", m)
	p.Parent = parent.ID
	if err := os.WriteFile(p.Disk, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.NVRAM, []byte("vars"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.Keys, 0o700); err != nil {
		t.Fatal(err)
	}
	writePendingMarker(t, m, p)
	if err := m.Collect(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.Image(parent.ID); err != nil {
		t.Fatal("pending parent collected")
	}
	if _, err := os.Stat(p.Disk); err != nil {
		t.Fatal("pending disk collected")
	}
	if _, err := os.Stat(m.Store.pendingJSON(p.ID)); err != nil {
		t.Fatal("pending marker collected")
	}
	if _, err := m.Store.Image(unused.ID); !os.IsNotExist(err) {
		t.Fatal("unused image kept")
	}
}

func TestCollectUnreadablePendingStopsBeforeRemove(t *testing.T) {
	m := testManager(t)
	unused := catalogImage(t, m, "")
	if err := os.MkdirAll(m.Store.pendingDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.Store.pendingJSON("bad"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Collect(); err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("unreadable pending: %v", err)
	}
	if _, err := m.Store.Image(unused.ID); err != nil {
		t.Fatal("removed images after unreadable pending")
	}
}

func TestCollectDuringConvertKeepsParent(t *testing.T) {
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	attachDeltaRunner(m, r, []string{parent.Disk}, func(args []string) error {
		if err := os.WriteFile(args[len(args)-1], []byte("image"), 0o600); err != nil {
			return err
		}
		close(started)
		<-release
		return nil
	})
	errc := make(chan error, 1)
	go func() { errc <- m.Snapshot(context.Background(), r, "hand") }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("convert did not start")
	}
	if err := m.Collect(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.Image(parent.ID); err != nil {
		t.Fatal("parent collected during convert")
	}
	pendings, err := m.listPending()
	if err != nil || len(pendings) != 1 {
		t.Fatalf("pending during convert: %v %v", pendings, err)
	}
	if _, err := os.Stat(pendings[0].Disk); err != nil {
		t.Fatal("pending disk collected during convert")
	}
	close(release)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestTwoStagesConvertConcurrently(t *testing.T) {
	m := testManager(t)
	a := readyStage(t, m, "alpha")
	b := readyStage(t, m, "beta")
	var converting atomic.Int32
	both := make(chan struct{})
	unblock := make(chan struct{})
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			if len(args) > 0 && args[0] == "info" {
				return `[{"filename":"x"}]`, nil
			}
			if converting.Add(1) == 2 {
				close(both)
			}
			select {
			case <-both:
			case <-time.After(2 * time.Second):
				return "", errors.New("second convert never started")
			}
			<-unblock
			return "", os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
		}
		switch args[2] {
		case "domuuid":
			for _, rec := range []*Record{a, b} {
				if len(args) > 3 && args[3] == rec.Domain {
					return uuid(rec.ID), nil
				}
			}
			return "", errors.New("unknown domain")
		case "domstate":
			return "shut off", nil
		default:
			return "", fmtUnexpected(bin, args)
		}
	})
	errc := make(chan error, 2)
	go func() { errc <- m.Snapshot(context.Background(), a, "hand") }()
	go func() { errc <- m.Snapshot(context.Background(), b, "hand") }()
	select {
	case <-both:
	case <-time.After(3 * time.Second):
		t.Fatal("converts did not overlap")
	}
	close(unblock)
	for i := 0; i < 2; i++ {
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	}
}

func TestSnapshotTimesOutWhenCallerHoldsCatalog(t *testing.T) {
	m, r := captureReady(t, "demo")
	hold, err := m.Store.LockMany("image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	err = m.Snapshot(ctx, r, "hand")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want timeout holding catalog, got %v", err)
	}
}

func TestSnapshotCancelDuringConvertRemovesPending(t *testing.T) {
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	attachDeltaRunner(m, r, []string{parent.Disk}, func(args []string) error {
		if err := os.WriteFile(args[len(args)-1], []byte("image"), 0o600); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	if err := m.Snapshot(ctx, r, "hand"); err == nil {
		t.Fatal("expected cancel")
	}
	pendings, err := m.listPending()
	if err != nil || len(pendings) != 0 {
		t.Fatalf("marker remains: %v %v", pendings, err)
	}
	files, err := filepath.Glob(filepath.Join(m.Store.Root, "images", "*-image.qcow2"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if filepath.Base(f) != filepath.Base(parent.Disk) {
			t.Fatalf("leftover child disk: %s", f)
		}
	}
}

func TestRecoverPendingNoJSONDeletesLeftovers(t *testing.T) {
	m, r := captureReady(t, "demo")
	p := managedPending(r.Name, "", m)
	writeManagedLeftovers(t, p)
	writePendingMarker(t, m, p)
	if err := m.Recover(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Disk); !os.IsNotExist(err) {
		t.Fatal("case 1 left disk")
	}
	if _, err := os.Stat(m.Store.pendingJSON(p.ID)); !os.IsNotExist(err) {
		t.Fatal("case 1 left marker")
	}
}

func TestRecoverPendingWithJSONRemovesMarkerOnly(t *testing.T) {
	m, r := captureReady(t, "demo")
	child := catalogImage(t, m, "")
	p := pendingCapture{
		ID: child.ID, Stage: r.Name, Disk: child.Disk, NVRAM: child.NVRAM,
		Keys: filepath.Join(m.Store.Root, "images", child.ID), Created: time.Now().UTC(),
	}
	writePendingMarker(t, m, p)
	if mappingPointsAt(r, child.ID) {
		t.Fatal("mapping should not point at uncommitted child")
	}
	if err := m.Recover(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Store.pendingJSON(p.ID)); !os.IsNotExist(err) {
		t.Fatal("case 2 left marker")
	}
	if _, err := m.Store.Image(child.ID); err != nil {
		t.Fatal("case 2 deleted child JSON")
	}
}

func TestRecoverPendingLeftoverMarkerAfterCommit(t *testing.T) {
	m, r := captureReady(t, "demo")
	child := catalogImage(t, m, "")
	r.Snapshots["hand"] = child.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	p := pendingCapture{
		ID: child.ID, Stage: r.Name, Disk: child.Disk, NVRAM: child.NVRAM,
		Keys: filepath.Join(m.Store.Root, "images", child.ID), Created: time.Now().UTC(),
	}
	writePendingMarker(t, m, p)
	if !mappingPointsAt(r, child.ID) {
		t.Fatal("expected mapping to point at child")
	}
	if err := m.Recover(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Store.pendingJSON(p.ID)); !os.IsNotExist(err) {
		t.Fatal("leftover marker remains")
	}
	if r.Snapshots["hand"] != child.ID {
		t.Fatal("mapping changed")
	}
	if _, err := m.Store.Image(child.ID); err != nil {
		t.Fatal("committed image removed")
	}
}

func TestDeleteCleansPendingMarkers(t *testing.T) {
	m, r := captureReady(t, "demo")
	p := managedPending(r.Name, "", m)
	if err := os.WriteFile(p.Disk, []byte("leftover"), 0o600); err != nil {
		t.Fatal(err)
	}
	writePendingMarker(t, m, p)
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			return "", nil
		}
		switch args[2] {
		case "domuuid":
			return "", errors.New("missing")
		case "list":
			return "", nil
		default:
			return "", fmtUnexpected(bin, args)
		}
	})
	if err := m.Delete(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Store.pendingJSON(p.ID)); !os.IsNotExist(err) {
		t.Fatal("delete left pending marker")
	}
	if _, err := os.Stat(p.Disk); !os.IsNotExist(err) {
		t.Fatal("delete left pending disk")
	}
}

func TestSnapshotDoesNotCollect(t *testing.T) {
	m, r := captureReady(t, "demo")
	orphan := catalogImage(t, m, "")
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.Image(orphan.ID); err != nil {
		t.Fatal("manual snapshot collected unused images")
	}
}

func TestCatalogWaitSecondsLoggedIncludingZero(t *testing.T) {
	m, r := captureReady(t, "demo")
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	log := readProvisionLog(t, m, "demo")
	if !strings.Contains(log, "timing catalog-wait-seconds 0.000") {
		t.Fatalf("missing zero catalog wait: %s", log)
	}
	result, err := m.ReplaceSnapshot(context.Background(), r, "saved", testOrigin("/p", "s"), false)
	if err != nil {
		t.Fatal(err)
	}
	if result.CatalogWaitSeconds == nil {
		t.Fatal("ReplaceResult omitted catalog-wait-seconds")
	}
	if *result.CatalogWaitSeconds != 0 {
		t.Fatalf("catalog wait: %v", *result.CatalogWaitSeconds)
	}
	log = readProvisionLog(t, m, "demo")
	if strings.Count(log, "timing catalog-wait-seconds") < 2 {
		t.Fatalf("replace did not log catalog wait: %s", log)
	}
}

func TestPendingMarkerLivesOutsideImages(t *testing.T) {
	m := testManager(t)
	p := pendingCapture{ID: randomID(), Stage: "demo", Disk: "/tmp/d", NVRAM: "/tmp/n", Created: time.Now().UTC()}
	writePendingMarker(t, m, p)
	if !strings.HasPrefix(m.Store.pendingJSON(p.ID), m.Store.pendingDir()) {
		t.Fatal("marker not under pending/")
	}
	if strings.Contains(m.Store.pendingJSON(p.ID), filepath.Join("images", p.ID)) {
		t.Fatal("marker under images/")
	}
	images, err := filepath.Glob(filepath.Join(m.Store.Root, "images", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 {
		t.Fatalf("images glob saw marker: %v", images)
	}
	st, err := os.Stat(m.Store.pendingDir())
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("pending dir mode %v", st.Mode().Perm())
	}
}

func writeUnreadablePending(t *testing.T, m *Manager, id string) {
	t.Helper()
	if err := os.MkdirAll(m.Store.pendingDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.Store.pendingJSON(id), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotCommitSyncFailureKeepsNewImage(t *testing.T) {
	m, r := captureReady(t, "demo")
	var warn bytes.Buffer
	m.Output = &warn
	commitStageRecord = func(s *Store, rec *Record) error {
		prev := syncParentDir
		syncParentDir = func(*os.File) error { return errors.New("dir sync failed") }
		defer func() { syncParentDir = prev }()
		return s.Save(rec)
	}
	t.Cleanup(func() { commitStageRecord = (*Store).Save })
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatalf("sync after rename is a warning: %v", err)
	}
	if !strings.Contains(warn.String(), "dir sync failed") {
		t.Fatalf("warning: %s", warn.String())
	}
	id := r.Snapshots["hand"]
	if id == "" {
		t.Fatal("in-memory mapping rolled back")
	}
	got, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Snapshots["hand"] != id {
		t.Fatalf("disk mapping: %s", got.Snapshots["hand"])
	}
	if _, err := m.Store.Image(id); err != nil {
		t.Fatalf("new image: %v", err)
	}
	if _, err := os.Stat(m.Store.pendingJSON(id)); !os.IsNotExist(err) {
		t.Fatal("marker remains after committed mapping")
	}
}

func TestSnapshotWriteFailureRemovesCapturedImage(t *testing.T) {
	m, r := captureReady(t, "demo")
	var orphan string
	commitStageRecord = func(*Store, *Record) error {
		return errors.New("disk full")
	}
	t.Cleanup(func() { commitStageRecord = (*Store).Save })
	stubCapture(func(mgr *Manager, ctx context.Context, rec *Record) (*Image, error) {
		img, err := mgr.capture(ctx, rec)
		if err == nil {
			orphan = img.ID
		}
		return img, err
	})
	t.Cleanup(restoreDefaultCapture)
	if err := m.Snapshot(context.Background(), r, "hand"); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("write: %v", err)
	}
	if r.Snapshots["hand"] != "" {
		t.Fatal("in-memory mapping kept")
	}
	if orphan == "" {
		t.Fatal("capture did not produce an image")
	}
	if _, err := m.Store.Image(orphan); !os.IsNotExist(err) {
		t.Fatal("failed save left the image")
	}
}

func TestFailPendingCancelWhileCatalogHeldReturnsQuickly(t *testing.T) {
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	unblock := make(chan struct{})
	attachDeltaRunner(m, r, []string{parent.Disk}, func(args []string) error {
		if err := os.WriteFile(args[len(args)-1], []byte("image"), 0o600); err != nil {
			return err
		}
		close(started)
		<-unblock
		cancel()
		return ctx.Err()
	})
	errc := make(chan error, 1)
	go func() { errc <- m.Snapshot(ctx, r, "hand") }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("convert did not start")
	}
	hold, err := m.Store.LockMany("image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	close(unblock)
	began := time.Now()
	select {
	case err = <-errc:
	case <-time.After(200 * time.Millisecond):
		hold()
		t.Fatal("cleanup waited on the catalog")
	}
	if time.Since(began) > 200*time.Millisecond {
		hold()
		t.Fatalf("cleanup waited %s", time.Since(began))
	}
	if !errors.Is(err, context.Canceled) {
		hold()
		t.Fatalf("cancel: %v", err)
	}
	pendings, err := m.scanPending()
	if err != nil || len(pendings.Found) != 1 || len(pendings.Unread) != 0 {
		hold()
		t.Fatalf("marker after cancel: %+v %v", pendings, err)
	}
	hold()
	if err := m.Recover(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Store.pendingJSON(pendings.Found[0].ID)); !os.IsNotExist(err) {
		t.Fatal("Recover left marker")
	}
}

func TestRecoverIgnoresUnreadablePending(t *testing.T) {
	m, r := captureReady(t, "demo")
	other := readyStage(t, m, "other")
	writeUnreadablePending(t, m, "bad")
	var warn bytes.Buffer
	m.Output = &warn
	if err := m.Recover(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if err := m.Recover(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn.String(), "unreadable pending marker") {
		t.Fatalf("warning: %s", warn.String())
	}
	if _, err := os.Stat(m.Store.pendingJSON("bad")); err != nil {
		t.Fatal("unreadable marker removed")
	}
}

func TestRestoreIgnoresUnreadablePending(t *testing.T) {
	m, r := restoreReady(t)
	writeUnreadablePending(t, m, "bad")
	m.Runner = virshState(r, "shut off", nil)
	if err := m.Restore(context.Background(), r, "initial"); err != nil {
		t.Fatal(err)
	}
	if err := m.Recover(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorListsUnreadablePending(t *testing.T) {
	m := testManager(t)
	writeUnreadablePending(t, m, "bad")
	found := false
	for _, c := range m.Doctor(context.Background()) {
		if c.Name != "pending-markers" {
			continue
		}
		found = true
		if c.OK || !strings.Contains(c.Detail, "bad.json") {
			t.Fatalf("doctor: %+v", c)
		}
	}
	if !found {
		t.Fatal("doctor omitted pending-markers")
	}
}

func TestCollectRemovesPendingForMissingStage(t *testing.T) {
	m := testManager(t)
	unused := catalogImage(t, m, "")
	p := managedPending("ghost", "", m)
	writeManagedLeftovers(t, p)
	writePendingMarker(t, m, p)
	if err := m.Collect(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Store.pendingJSON(p.ID)); !os.IsNotExist(err) {
		t.Fatal("orphan marker remains")
	}
	if _, err := os.Stat(p.Disk); !os.IsNotExist(err) {
		t.Fatal("orphan disk remains")
	}
	if _, err := m.Store.Image(unused.ID); !os.IsNotExist(err) {
		t.Fatal("unused image kept")
	}
}

func TestCollectUnreadableStageIsNotOrphan(t *testing.T) {
	m := testManager(t)
	readyStage(t, m, "demo")
	unused := catalogImage(t, m, "")
	p := managedPending("demo", "", m)
	writeManagedLeftovers(t, p)
	writePendingMarker(t, m, p)
	if err := os.WriteFile(filepath.Join(m.Store.Dir("demo"), "stage.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Collect(); err == nil || !strings.Contains(err.Error(), "stage demo") {
		t.Fatalf("unreadable stage: %v", err)
	}
	if _, err := os.Stat(m.Store.pendingJSON(p.ID)); err != nil {
		t.Fatal("marker removed")
	}
	if _, err := os.Stat(p.Disk); err != nil {
		t.Fatal("disk removed")
	}
	if _, err := os.Stat(p.NVRAM); err != nil {
		t.Fatal("nvram removed")
	}
	if _, err := os.Stat(p.Keys); err != nil {
		t.Fatal("keys removed")
	}
	if _, err := m.Store.Image(unused.ID); err != nil {
		t.Fatal("removed images after unreadable stage")
	}
}

func TestCollectCountsPendingChildJSON(t *testing.T) {
	m := testManager(t)
	readyStage(t, m, "demo")
	ancestor := catalogImage(t, m, "")
	child := catalogImage(t, m, ancestor.ID)
	p := pendingCapture{
		ID: child.ID, Stage: "demo", Parent: "",
		Disk: child.Disk, NVRAM: child.NVRAM,
		Keys: filepath.Join(m.Store.Root, "images", child.ID), Created: time.Now().UTC(),
	}
	writePendingMarker(t, m, p)
	if err := m.Collect(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.Image(ancestor.ID); err != nil {
		t.Fatal("pending child JSON did not keep ancestor")
	}
	if _, err := m.Store.Image(child.ID); err != nil {
		t.Fatal("pending child collected")
	}
}

func TestCollectProtectsPendingPaths(t *testing.T) {
	m := testManager(t)
	readyStage(t, m, "demo")
	victim := catalogImage(t, m, "")
	p := pendingCapture{
		ID: randomID(), Stage: "demo",
		Disk: victim.Disk, NVRAM: victim.NVRAM,
		Keys: filepath.Join(m.Store.Root, "images", victim.ID), Created: time.Now().UTC(),
	}
	writePendingMarker(t, m, p)
	if _, err := os.Stat(m.Store.imageJSON(p.ID)); !os.IsNotExist(err) {
		t.Fatal("child JSON should be absent for path protection")
	}
	if err := m.Collect(); err == nil || !strings.Contains(err.Error(), "outside managed storage") {
		t.Fatalf("mismatched pending paths: %v", err)
	}
	if _, err := m.Store.Image(victim.ID); err != nil {
		t.Fatal("pending paths did not protect catalog image")
	}
}

func TestRecoverAndDeleteLeaveOtherStagePending(t *testing.T) {
	m := testManager(t)
	x := readyStage(t, m, "alpha")
	_ = readyStage(t, m, "beta")
	p := managedPending("beta", "", m)
	if err := os.WriteFile(p.Disk, []byte("live"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.NVRAM, []byte("vars"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.Keys, 0o700); err != nil {
		t.Fatal(err)
	}
	writePendingMarker(t, m, p)
	if err := m.Recover(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Store.pendingJSON(p.ID)); err != nil {
		t.Fatal("Recover of alpha touched beta marker")
	}
	if _, err := os.Stat(p.Disk); err != nil {
		t.Fatal("Recover of alpha touched beta disk")
	}
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			return "", nil
		}
		switch args[2] {
		case "domuuid":
			return "", errors.New("missing")
		case "list":
			return "", nil
		default:
			return "", fmtUnexpected(bin, args)
		}
	})
	if err := m.Delete(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Store.pendingJSON(p.ID)); err != nil {
		t.Fatal("Delete of alpha touched beta marker")
	}
	if _, err := os.Stat(p.Disk); err != nil {
		t.Fatal("Delete of alpha touched beta disk")
	}
}

func TestSnapshotCommitHoldsCatalog(t *testing.T) {
	m, r := captureReady(t, "demo")
	commitStageRecord = func(s *Store, rec *Record) error {
		_, err := s.LockMany("image-catalog")
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("catalog not held at mapping commit: %v", err)
		}
		return s.Save(rec)
	}
	t.Cleanup(func() { commitStageRecord = (*Store).Save })
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
}

func TestReplaceSnapshotSuccessLeavesNoMarker(t *testing.T) {
	m, r := captureReady(t, "demo")
	if _, err := m.ReplaceSnapshot(context.Background(), r, "saved", testOrigin("/p", "s"), false); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(m.Store.pendingDir(), "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("vm-end left markers: %v", files)
	}
}

func TestPlanCaptureRequiresShutOffUnderCatalog(t *testing.T) {
	m, r := captureReady(t, "demo")
	var states, converts int
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			if len(args) > 0 && args[0] == "convert" {
				converts++
			}
			if len(args) > 0 && args[0] == "info" {
				return `[{"filename":"` + r.Disk + `"}]`, nil
			}
			return "", os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
		}
		switch args[2] {
		case "domuuid":
			return uuid(r.ID), nil
		case "domstate":
			states++
			if states == 1 {
				return "shut off", nil
			}
			return "running", nil
		default:
			return "", fmtUnexpected(bin, args)
		}
	})
	if err := m.Snapshot(context.Background(), r, "hand"); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("running under catalog: %v", err)
	}
	if converts != 0 {
		t.Fatalf("convert ran %d times", converts)
	}
}

func TestCaptureFailureInConvertDeletesNewFiles(t *testing.T) {
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	var dest string
	attachDeltaRunner(m, r, []string{parent.Disk}, func(args []string) error {
		dest = args[len(args)-1]
		if err := os.WriteFile(dest, []byte("image"), 0o600); err != nil {
			return err
		}
		return errors.New("convert failed")
	})
	if err := m.Snapshot(context.Background(), r, "hand"); err == nil || !strings.Contains(err.Error(), "convert failed") {
		t.Fatalf("convert: %v", err)
	}
	if dest == "" {
		t.Fatal("convert did not run")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("failed convert left disk")
	}
	pendings, err := m.listPending()
	if err != nil || len(pendings) != 0 {
		t.Fatalf("marker remains: %v %v", pendings, err)
	}
	files, err := filepath.Glob(filepath.Join(m.Store.Root, "images", "*-image.qcow2"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if filepath.Base(f) != filepath.Base(parent.Disk) {
			t.Fatalf("leftover child disk: %s", f)
		}
	}
}

func unmanagedPendingBait(t *testing.T, m *Manager, stage string) (pendingCapture, string, string) {
	t.Helper()
	outside := t.TempDir()
	baitFile := filepath.Join(outside, "secret.txt")
	baitDir := filepath.Join(outside, "secretdir")
	if err := os.WriteFile(baitFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(baitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baitDir, "inside"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := managedPending(stage, "", m)
	p.Disk = baitFile
	p.Keys = baitDir
	writePendingMarker(t, m, p)
	return p, baitFile, baitDir
}

func assertBaitStays(t *testing.T, baitFile, baitDir string) {
	t.Helper()
	if _, err := os.Stat(baitFile); err != nil {
		t.Fatal("deleted file outside the store")
	}
	if _, err := os.Stat(filepath.Join(baitDir, "inside")); err != nil {
		t.Fatal("deleted directory outside the store")
	}
}

func TestCollectRefusesUnmanagedPendingPaths(t *testing.T) {
	m := testManager(t)
	unused := catalogImage(t, m, "")
	p, baitFile, baitDir := unmanagedPendingBait(t, m, "ghost")
	if err := m.Collect(); err == nil || !strings.Contains(err.Error(), p.ID+".json") || !strings.Contains(err.Error(), "outside managed storage") {
		t.Fatalf("collect: %v", err)
	}
	assertBaitStays(t, baitFile, baitDir)
	if _, err := os.Stat(m.Store.pendingJSON(p.ID)); err != nil {
		t.Fatal("marker removed")
	}
	if _, err := m.Store.Image(unused.ID); err != nil {
		t.Fatal("removed images after unmanaged pending")
	}
}

func TestRecoverLeavesUnmanagedPendingPaths(t *testing.T) {
	m, r := captureReady(t, "demo")
	p, baitFile, baitDir := unmanagedPendingBait(t, m, r.Name)
	var warn bytes.Buffer
	m.Output = &warn
	if err := m.Recover(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn.String(), "unmanaged paths") {
		t.Fatalf("warning: %s", warn.String())
	}
	assertBaitStays(t, baitFile, baitDir)
	if _, err := os.Stat(m.Store.pendingJSON(p.ID)); err != nil {
		t.Fatal("marker removed")
	}
}

func TestDoctorListsUnmanagedPendingPaths(t *testing.T) {
	m := testManager(t)
	p, _, _ := unmanagedPendingBait(t, m, "demo")
	found := false
	for _, c := range m.Doctor(context.Background()) {
		if c.Name != "pending-markers" {
			continue
		}
		found = true
		if c.OK || !strings.Contains(c.Detail, p.ID+".json") {
			t.Fatalf("doctor: %+v", c)
		}
	}
	if !found {
		t.Fatal("doctor omitted pending-markers")
	}
}

func TestDeleteLeavesUnmanagedPendingPaths(t *testing.T) {
	m, r := captureReady(t, "demo")
	p, baitFile, baitDir := unmanagedPendingBait(t, m, r.Name)
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			return "", nil
		}
		switch args[2] {
		case "domuuid":
			return "", errors.New("missing")
		case "list":
			return "", nil
		default:
			return "", fmtUnexpected(bin, args)
		}
	})
	err := m.Delete(context.Background(), r)
	if err == nil || !strings.Contains(err.Error(), "outside managed storage") {
		t.Fatalf("delete: %v", err)
	}
	assertBaitStays(t, baitFile, baitDir)
	if _, err := os.Stat(m.Store.pendingJSON(p.ID)); err != nil {
		t.Fatal("marker removed")
	}
}

func TestFailPendingRefusesUnmanagedPendingPaths(t *testing.T) {
	m := testManager(t)
	p, baitFile, baitDir := unmanagedPendingBait(t, m, "demo")
	extra := filepath.Join(t.TempDir(), "extra.txt")
	if err := os.WriteFile(extra, []byte("extra"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := m.failPending(context.Background(), p, []string{extra})
	if err == nil || !strings.Contains(err.Error(), p.ID+".json") || !strings.Contains(err.Error(), "outside managed storage") {
		t.Fatalf("failPending: %v", err)
	}
	assertBaitStays(t, baitFile, baitDir)
	if _, err := os.Stat(extra); err != nil {
		t.Fatal("deleted extra path")
	}
	if _, err := os.Stat(m.Store.pendingJSON(p.ID)); err != nil {
		t.Fatal("marker removed")
	}
}

func TestSnapshotRemovePendingFailureIsWarning(t *testing.T) {
	m, r := captureReady(t, "demo")
	var warn bytes.Buffer
	m.Output = &warn
	prev := removePendingMarker
	removePendingMarker = func(*Manager, string) error { return errors.New("marker busy") }
	t.Cleanup(func() { removePendingMarker = prev })
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatalf("committed snapshot: %v", err)
	}
	if !strings.Contains(warn.String(), "pending marker left; the next stage operation removes it") {
		t.Fatalf("warning: %s", warn.String())
	}
	id := r.Snapshots["hand"]
	if id == "" {
		t.Fatal("mapping rolled back")
	}
	if _, err := m.Store.Image(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Store.pendingJSON(id)); err != nil {
		t.Fatal("marker should remain for Recover")
	}
	if err := m.Recover(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Store.pendingJSON(id)); !os.IsNotExist(err) {
		t.Fatal("Recover left marker")
	}
}
