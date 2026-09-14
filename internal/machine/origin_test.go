package machine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadRecordWithoutSnapshotOrigins(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	r.ID = "12345678901234567890123456789012"
	r.Snapshots["initial"] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dir := m.Store.Dir(r.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{
  "schema": 1,
  "id": "12345678901234567890123456789012",
  "name": "demo",
  "domain": "backstage-test-demo",
  "uri": "qemu:///system",
  "spec": {"omarchy":"latest","cpus":4,"memory":8589934592,"disk":42949672960,"width":1920,"height":1080,"keyboard":"us","locale":"en_US.UTF-8","timezone":"UTC"},
  "source": {},
  "status": "ready",
  "phase": "",
  "created": "2020-01-01T00:00:00Z",
  "snapshots": {"initial": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
}
`
	if err := os.WriteFile(filepath.Join(dir, "stage.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got.SnapshotOrigins != nil {
		t.Fatalf("origins: %#v", got.SnapshotOrigins)
	}
	if got.Snapshots["initial"] != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("snapshots: %#v", got.Snapshots)
	}
}

func TestSaveReloadPreservesSnapshotOrigins(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	origin := testOrigin("/proj", "alpha")
	origin.Image = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	origin.Made = time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)
	r.Snapshots["ready"] = origin.Image
	r.SnapshotOrigins = map[string]SnapshotOrigin{"ready": origin}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	got, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Snapshots["ready"] != origin.Image {
		t.Fatalf("image: %s", got.Snapshots["ready"])
	}
	have, ok := got.SnapshotOrigins["ready"]
	if !ok || have != origin {
		t.Fatalf("origin: %#v", got.SnapshotOrigins)
	}
}

func TestReplaceSnapshotOwnOwnerWritesMappingAndOriginOnce(t *testing.T) {
	m, r := captureReady(t, "demo")
	old := randomID()
	writeCatalogImage(t, m, old)
	r.Snapshots["ready"] = old
	r.SnapshotOrigins = map[string]SnapshotOrigin{"ready": testOrigin("/proj", "alpha")}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	saves := 0
	commitStageRecord = func(s *Store, rec *Record) error {
		saves++
		if rec.Snapshots["ready"] == old {
			t.Fatal("saved mapping without the new image")
		}
		o, ok := rec.SnapshotOrigins["ready"]
		if !ok || o.Image == "" || o.Image == old || rec.Snapshots["ready"] != o.Image {
			t.Fatalf("partial origin write: %#v", rec.SnapshotOrigins)
		}
		return s.Save(rec)
	}
	t.Cleanup(func() { commitStageRecord = (*Store).Save })
	result, err := m.ReplaceSnapshot(context.Background(), r, "ready", testOrigin("/proj", "alpha"), false)
	if err != nil {
		t.Fatal(err)
	}
	if saves != 1 {
		t.Fatalf("commits: %d", saves)
	}
	if result.Image == nil || result.Warning != "" {
		t.Fatalf("result: %+v", result)
	}
	got, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Snapshots["ready"] != result.Image.ID {
		t.Fatalf("mapping: %s", got.Snapshots["ready"])
	}
	if got.SnapshotOrigins["ready"].Image != result.Image.ID {
		t.Fatalf("origin image: %#v", got.SnapshotOrigins["ready"])
	}
	if _, err := m.Store.Image(old); !os.IsNotExist(err) {
		t.Fatalf("old image remains: %v", err)
	}
}

func TestReplaceSnapshotManualRequiresAdopt(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	r.Snapshots["ready"] = randomID()
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReplaceSnapshot(context.Background(), r, "ready", testOrigin("/proj", "alpha"), false); err == nil || !strings.Contains(err.Error(), "no origin") {
		t.Fatalf("manual: %v", err)
	}
	loaded, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SnapshotOrigins != nil {
		t.Fatal("wrote an origin without adopt")
	}
	m, r = captureReady(t, "demo")
	old := randomID()
	writeCatalogImage(t, m, old)
	r.Snapshots["ready"] = old
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	result, err := m.ReplaceSnapshot(context.Background(), r, "ready", testOrigin("/proj", "alpha"), true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Image == nil {
		t.Fatal("adopt did not capture")
	}
	got, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got.SnapshotOrigins["ready"].Project != "/proj" {
		t.Fatalf("adopted: %#v", got.SnapshotOrigins["ready"])
	}
}

func TestReplaceSnapshotRefusesOtherOwner(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	r.Snapshots["ready"] = randomID()
	r.SnapshotOrigins = map[string]SnapshotOrigin{"ready": testOrigin("/other", "beta")}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReplaceSnapshot(context.Background(), r, "ready", testOrigin("/proj", "alpha"), false); err == nil || !strings.Contains(err.Error(), "another scene") {
		t.Fatalf("other owner: %v", err)
	}
}

func TestReplaceSnapshotRefusesNotReady(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	r.Status = "building"
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	release, err := m.Store.LockMany("image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, err = m.ReplaceSnapshot(context.Background(), r, "ready", testOrigin("/proj", "alpha"), false)
	if err == nil || !strings.Contains(err.Error(), "ready") {
		t.Fatalf("not ready: %v", err)
	}
}

func TestReplaceSnapshotRefusesInitial(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	r.Snapshots["initial"] = randomID()
	if _, err := m.ReplaceSnapshot(context.Background(), r, "initial", testOrigin("/proj", "alpha"), true); err == nil || !strings.Contains(err.Error(), "initial") {
		t.Fatalf("initial: %v", err)
	}
}

func TestReplaceSnapshotCaptureFailureKeepsPrevious(t *testing.T) {
	m, r := captureReady(t, "demo")
	old := randomID()
	writeCatalogImage(t, m, old)
	origin := testOrigin("/proj", "alpha")
	origin.Image = old
	r.Snapshots["ready"] = old
	r.SnapshotOrigins = map[string]SnapshotOrigin{"ready": origin}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	captureStageImage = func(*Manager, context.Context, *Record) (*Image, error) {
		return nil, errors.New("capture failed")
	}
	t.Cleanup(func() {
		captureStageImage = func(m *Manager, ctx context.Context, rec *Record) (*Image, error) {
			return m.capture(ctx, rec)
		}
	})
	if _, err := m.ReplaceSnapshot(context.Background(), r, "ready", testOrigin("/proj", "alpha"), false); err == nil {
		t.Fatal("capture failure succeeded")
	}
	assertUnchanged(t, m, "ready", old, origin)
}

func TestAtomicWriteSyncFailureIsCommitted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stage.json")
	prev := syncParentDir
	syncParentDir = func(*os.File) error { return errors.New("dir sync failed") }
	t.Cleanup(func() { syncParentDir = prev })
	if err := atomicWrite(path, []byte("{}\n"), 0o600); !errors.Is(err, ErrCommitted) {
		t.Fatalf("committed: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{}\n" {
		t.Fatalf("bytes: %q", got)
	}
}

func TestReplaceSnapshotCommitSyncFailureKeepsNewImage(t *testing.T) {
	m, r := captureReady(t, "demo")
	old := randomID()
	writeCatalogImage(t, m, old)
	origin := testOrigin("/proj", "alpha")
	origin.Image = old
	r.Snapshots["ready"] = old
	r.SnapshotOrigins = map[string]SnapshotOrigin{"ready": origin}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	commitStageRecord = func(s *Store, rec *Record) error {
		prev := syncParentDir
		syncParentDir = func(*os.File) error { return errors.New("dir sync failed") }
		defer func() { syncParentDir = prev }()
		return s.Save(rec)
	}
	t.Cleanup(func() { commitStageRecord = (*Store).Save })
	result, err := m.ReplaceSnapshot(context.Background(), r, "ready", testOrigin("/proj", "alpha"), false)
	if err != nil {
		t.Fatalf("sync after rename is a warning: %v", err)
	}
	if result.Image == nil || result.Warning == "" || !strings.Contains(result.Warning, "dir sync failed") {
		t.Fatalf("result: %+v", result)
	}
	if r.Snapshots["ready"] != result.Image.ID {
		t.Fatalf("in-memory mapping: %s", r.Snapshots["ready"])
	}
	got, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Snapshots["ready"] != result.Image.ID {
		t.Fatalf("disk mapping: %s", got.Snapshots["ready"])
	}
	if _, err := m.Store.Image(result.Image.ID); err != nil {
		t.Fatalf("new image: %v", err)
	}
}

func TestReplaceSnapshotWriteFailureRemovesCapturedImage(t *testing.T) {
	m, r := captureReady(t, "demo")
	old := randomID()
	writeCatalogImage(t, m, old)
	origin := testOrigin("/proj", "alpha")
	origin.Image = old
	r.Snapshots["ready"] = old
	r.SnapshotOrigins = map[string]SnapshotOrigin{"ready": origin}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	var orphan string
	commitStageRecord = func(*Store, *Record) error {
		return errors.New("disk full")
	}
	t.Cleanup(func() { commitStageRecord = (*Store).Save })
	captureStageImage = func(mgr *Manager, ctx context.Context, rec *Record) (*Image, error) {
		img, err := mgr.capture(ctx, rec)
		if err == nil {
			orphan = img.ID
		}
		return img, err
	}
	t.Cleanup(func() {
		captureStageImage = func(mgr *Manager, ctx context.Context, rec *Record) (*Image, error) {
			return mgr.capture(ctx, rec)
		}
	})
	if _, err := m.ReplaceSnapshot(context.Background(), r, "ready", testOrigin("/proj", "alpha"), false); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("write: %v", err)
	}
	assertUnchanged(t, m, "ready", old, origin)
	if orphan == "" {
		t.Fatal("capture did not produce an image")
	}
	if _, err := m.Store.Image(orphan); !os.IsNotExist(err) {
		t.Fatalf("captured image remains: %v", err)
	}
}

func TestReplaceSnapshotCollectFailureKeepsNewAndLaterMutationCleansOrphan(t *testing.T) {
	m, r := captureReady(t, "demo")
	old := randomID()
	writeCatalogImage(t, m, old)
	origin := testOrigin("/proj", "alpha")
	origin.Image = old
	r.Snapshots["ready"] = old
	r.SnapshotOrigins = map[string]SnapshotOrigin{"ready": origin}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	collectUnusedImages = func(*Manager) error {
		return errors.New("collect busy")
	}
	t.Cleanup(func() {
		collectUnusedImages = func(mgr *Manager) error { return mgr.Collect() }
	})
	result, err := m.ReplaceSnapshot(context.Background(), r, "ready", testOrigin("/proj", "alpha"), false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Warning == "" || !strings.Contains(result.Warning, "pending cleanup") {
		t.Fatalf("warning: %q", result.Warning)
	}
	got, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Snapshots["ready"] != result.Image.ID {
		t.Fatal("lost the new snapshot after collect failure")
	}
	if _, err := m.Store.Image(old); err != nil {
		t.Fatal("old image was collected despite failure", err)
	}
	collectUnusedImages = func(mgr *Manager) error { return mgr.Collect() }
	spare := randomID()
	writeCatalogImage(t, m, spare)
	got.Snapshots["spare"] = spare
	if err := m.Store.Save(got); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DeleteSnapshot(got, "spare"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.Image(old); !os.IsNotExist(err) {
		t.Fatalf("later mutation left orphan: %v", err)
	}
	if _, err := m.Store.Image(result.Image.ID); err != nil {
		t.Fatal("lost the committed image", err)
	}
}

func TestDeleteSnapshotCommitSyncFailureKeepsRemoval(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	keep := randomID()
	gone := randomID()
	writeCatalogImage(t, m, keep)
	writeCatalogImage(t, m, gone)
	origin := testOrigin("/proj", "alpha")
	origin.Image = gone
	r.Snapshots["initial"] = keep
	r.Snapshots["ready"] = gone
	r.SnapshotOrigins = map[string]SnapshotOrigin{"ready": origin}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	commitStageRecord = func(s *Store, rec *Record) error {
		prev := syncParentDir
		syncParentDir = func(*os.File) error { return errors.New("dir sync failed") }
		defer func() { syncParentDir = prev }()
		return s.Save(rec)
	}
	t.Cleanup(func() { commitStageRecord = (*Store).Save })
	warn, err := m.DeleteSnapshot(r, "ready")
	if err != nil {
		t.Fatalf("sync after rename is a warning: %v", err)
	}
	if warn == "" || !strings.Contains(warn, "dir sync failed") {
		t.Fatalf("warning: %q", warn)
	}
	if _, ok := r.Snapshots["ready"]; ok {
		t.Fatal("in-memory mapping remains")
	}
	got, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Snapshots["ready"]; ok {
		t.Fatal("disk mapping remains")
	}
	if _, ok := got.SnapshotOrigins["ready"]; ok {
		t.Fatal("origin remains")
	}
	if _, err := m.Store.Image(gone); !os.IsNotExist(err) {
		t.Fatalf("collect did not run: %v", err)
	}
	if _, err := m.Store.Image(keep); err != nil {
		t.Fatal("deleted initial", err)
	}
}

func TestDeleteSnapshotRefusesInitialAndRemovesMappingWithOrigin(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	keep := randomID()
	gone := randomID()
	writeCatalogImage(t, m, keep)
	writeCatalogImage(t, m, gone)
	origin := testOrigin("/proj", "alpha")
	origin.Image = gone
	r.Snapshots["initial"] = keep
	r.Snapshots["ready"] = gone
	r.SnapshotOrigins = map[string]SnapshotOrigin{"ready": origin}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DeleteSnapshot(r, "initial"); err == nil || !strings.Contains(err.Error(), "initial") {
		t.Fatalf("initial: %v", err)
	}
	if _, err := m.DeleteSnapshot(r, "missing"); err == nil {
		t.Fatal("missing snapshot")
	}
	if _, err := m.DeleteSnapshot(r, "ready"); err != nil {
		t.Fatal(err)
	}
	got, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Snapshots["ready"]; ok {
		t.Fatal("mapping remains")
	}
	if _, ok := got.SnapshotOrigins["ready"]; ok {
		t.Fatal("origin remains")
	}
	if got.Snapshots["initial"] != keep {
		t.Fatal("deleted initial")
	}
	if _, err := m.Store.Image(gone); !os.IsNotExist(err) {
		t.Fatalf("collect did not run: %v", err)
	}
}

func TestRecordWithoutOriginsRefusesReplacement(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	r.Snapshots["ready"] = randomID()
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReplaceSnapshot(context.Background(), r, "ready", testOrigin("/proj", "alpha"), false); err == nil {
		t.Fatal("replaced a record with no origins")
	}
}

func testOrigin(project, scene string) SnapshotOrigin {
	return SnapshotOrigin{
		Project:      project,
		Scene:        scene,
		InputsSHA256: "digest",
		StartImage:   "start",
		Take:         TakeRecording,
		Backstage:    "test",
	}
}

func captureReady(t *testing.T, name string) (*Manager, *Record) {
	t.Helper()
	m := testManager(t)
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
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			return "", os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
		}
		switch args[2] {
		case "domuuid":
			return uuid(r.ID), nil
		case "domstate":
			return "shut off", nil
		default:
			t.Fatalf("unexpected %s %v", bin, args)
			return "", nil
		}
	})
	return m, r
}

func writeCatalogImage(t *testing.T, m *Manager, id string) {
	t.Helper()
	i := Image{Schema: Schema, ID: id, Disk: m.diskPath(id, "-image.qcow2"), NVRAM: m.diskPath(id, "-image.fd")}
	if err := os.WriteFile(i.Disk, []byte("disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(i.NVRAM, []byte("vars"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(m.Store.Root, "images", id+".json"), i); err != nil {
		t.Fatal(err)
	}
}

func assertUnchanged(t *testing.T, m *Manager, name, image string, origin SnapshotOrigin) {
	t.Helper()
	got, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Snapshots[name] != image {
		t.Fatalf("mapping changed: %s", got.Snapshots[name])
	}
	if got.SnapshotOrigins[name] != origin {
		t.Fatalf("origin changed: %#v", got.SnapshotOrigins[name])
	}
}

func TestSnapshotInfoJSONNullOrigin(t *testing.T) {
	r := testRecord("demo")
	r.Snapshots["initial"] = "i"
	r.Snapshots["ready"] = "n"
	origin := testOrigin("/p", "s")
	origin.Image = "n"
	r.SnapshotOrigins = map[string]SnapshotOrigin{"ready": origin}
	b, err := json.Marshal(r.SnapshotInfo())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"origin":null`) {
		t.Fatalf("manual origin: %s", b)
	}
	if !strings.Contains(string(b), `"scene":"s"`) {
		t.Fatalf("produced origin: %s", b)
	}
}
