package machine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	s := &Store{Root: filepath.Join(dir, "registry"), Cache: filepath.Join(dir, "cache"), Storage: filepath.Join(dir, "storage")}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.Storage, 0o700); err != nil {
		t.Fatal(err)
	}
	return &Manager{Store: s, Runner: ExecRunner{}, URI: "qemu:///system", Timeout: time.Second, Output: io.Discard}
}

func testRecord(name string) *Record {
	return &Record{Schema: Schema, ID: randomID(), Name: name, Domain: "backstage-test-" + name, URI: "qemu:///system", Spec: DefaultSpec(), Status: "ready", Snapshots: map[string]string{}}
}

func TestStoreIsolationAndLocks(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.Load("../demo"); err == nil {
		t.Fatal("accepted traversal")
	}
	release, err := m.Store.LockMany("demo", "other", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if unlock, err := m.Store.LockMany("demo"); err == nil {
		unlock()
		t.Fatal("allowed concurrent access")
	}
	release()
	unlock, err := m.Store.LockMany("demo")
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if err := m.Store.SaveCredentials("demo", Credentials{Password: "secret", Key: "private-key"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(m.Store.Dir("demo"), "stage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "private-key") {
		t.Fatal("registry exposes secrets")
	}
	info, err := os.Stat(filepath.Join(m.Store.Dir("demo"), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatal("credentials permissions")
	}
	r.Schema = 99
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.Load("demo"); err == nil {
		t.Fatal("accepted unknown schema")
	}
}

func TestVerifiedDownloadCacheAndCorruption(t *testing.T) {
	content := "an ISO fixture"
	sum := sha256.Sum256([]byte(content))
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; _, _ = io.WriteString(w, content) }))
	defer server.Close()
	source := Source{URL: server.URL, SHA256: hex.EncodeToString(sum[:])}
	path := filepath.Join(t.TempDir(), "omarchy.iso")
	if err := downloadVerified(context.Background(), source, path); err != nil {
		t.Fatal(err)
	}
	if err := downloadVerified(context.Background(), source, path); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("cache downloaded %d times", calls)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	content = "wrong download"
	if err := downloadVerified(context.Background(), source, path); err == nil {
		t.Fatal("accepted wrong checksum")
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".download-*"))
	if len(files) != 0 {
		t.Fatal("partial download leaked")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := downloadVerified(ctx, source, path); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestBaseIdentityAndInstallerDisk(t *testing.T) {
	s := DefaultSpec()
	source := Source{Version: "4.0.3", SHA256: "abc", Recipe: Recipe}
	key := baseKey(s, source)
	s.CPUs = 8
	s.Memory = 16 << 30
	if baseKey(s, source) != key {
		t.Fatal("hardware prevents base reuse")
	}
	s.Locale = "pt_BR.UTF-8"
	if baseKey(s, source) == key {
		t.Fatal("locale shares incompatible base")
	}
	b, err := json.Marshal(installerConfig(s, "demo"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"device":"/dev/vda"`, `"wipe":true`, `"fs_type":"btrfs"`, `"fs_type":"fat32"`, `"defer_provisioning":false`} {
		if !strings.Contains(string(b), fragment) {
			t.Errorf("missing %s", fragment)
		}
	}
	if strings.Contains(string(b), "disk_encryption") {
		t.Fatal("unattended stage asks for encryption")
	}
}

func TestContinuityRejectsDifferentEnvironment(t *testing.T) {
	last := &Continuity{Project: "/project", Scene: "one", Recording: true, Session: "boot\nhypr"}
	if err := CheckContinuity(last, "/project", "one", true, "boot\nhypr"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		project, after, session string
		recording               bool
	}{{"/other", "one", "boot\nhypr", true}, {"/project", "two", "boot\nhypr", true}, {"/project", "one", "newboot\nhypr", true}, {"/project", "one", "boot\nnewhypr", true}, {"/project", "one", "boot\nhypr", false}} {
		if CheckContinuity(last, tc.project, tc.after, tc.recording, tc.session) == nil {
			t.Fatalf("accepted mismatched continuation %+v", tc)
		}
	}
	if CheckContinuity(nil, "/project", "one", true, "boot\nhypr") == nil {
		t.Fatal("accepted absent predecessor")
	}
}

type runnerFunc func(context.Context, io.Reader, string, ...string) (string, error)

func (f runnerFunc) Run(c context.Context, r io.Reader, n string, a ...string) (string, error) {
	return f(c, r, n, a...)
}

func TestDestructiveOperationChecksOwnership(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	calls := []string{}
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, name string, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[2] == "domuuid" {
			return uuid(randomID()), nil
		}
		t.Fatalf("unexpected %s %v", name, args)
		return "", nil
	})
	if m.Stop(context.Background(), r, true) == nil {
		t.Fatal("stopped foreign domain")
	}
	if len(calls) != 1 {
		t.Fatalf("destructive calls: %v", calls)
	}
}

func TestVideoSelectionUsesAvailableSoftwareDevice(t *testing.T) {
	for _, tc := range []struct {
		values, want string
		bad          bool
	}{{"<value>virtio</value><value>bochs</value>", "virtio", false}, {"<value>bochs</value>", "bochs", false}, {"<value>cirrus</value>", "", true}} {
		m := testManager(t)
		m.Runner = runnerFunc(func(context.Context, io.Reader, string, ...string) (string, error) {
			return `<domainCapabilities><devices><video><enum name="modelType">` + tc.values + `</enum></video></devices></domainCapabilities>`, nil
		})
		got, err := m.videoModel(context.Background())
		if (err != nil) != tc.bad || got != tc.want {
			t.Fatalf("%s: %s %v", tc.values, got, err)
		}
	}
}

func TestSnapshotRefusesRunningDiskAndExistingName(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	r.Snapshots["initial"] = "existing"
	if err := m.Snapshot(context.Background(), r, "initial"); err == nil {
		t.Fatal("overwrote snapshot")
	}
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, _ string, args ...string) (string, error) {
		switch args[2] {
		case "domuuid":
			return uuid(r.ID), nil
		case "domstate":
			return "running", nil
		default:
			t.Fatalf("touched active disk: %v", args)
			return "", nil
		}
	})
	if _, err := m.capture(context.Background(), r); err == nil {
		t.Fatal("captured live disk")
	}
}

func TestInterruptedActivationPreservesOldDiskAndRecoversReplacement(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	r.Video = "bochs"
	r.Disk = m.diskPath(r.ID, "-old.qcow2")
	r.NVRAM = m.diskPath(r.ID, "-old.fd")
	for _, p := range []string{r.Disk, r.NVRAM} {
		if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := m.Store.SaveCredentials(r.Name, Credentials{Password: "old", Key: "old-key"}); err != nil {
		t.Fatal(err)
	}
	i := &Image{ID: randomID(), Firmware: "firmware", Credentials: Credentials{Password: "restored", Key: "restored-key"}}
	i.Disk = m.diskPath(i.ID, "-image.qcow2")
	i.NVRAM = m.diskPath(i.ID, "-image.fd")
	for _, p := range []string{i.Disk, i.NVRAM} {
		if err := os.WriteFile(p, []byte("snapshot"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	failDefine := true
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, name string, args ...string) (string, error) {
		if name == "qemu-img" {
			return "", os.WriteFile(args[len(args)-1], []byte("overlay"), 0o600)
		}
		switch args[2] {
		case "domuuid":
			return uuid(r.ID), nil
		case "domstate":
			return "shut off", nil
		case "define":
			if failDefine {
				return "", errors.New("interrupted define")
			}
			return "", nil
		default:
			t.Fatalf("unexpected %v", args)
			return "", nil
		}
	})
	oldDisk := r.Disk
	if err := m.activate(context.Background(), r, i, false); err == nil {
		t.Fatal("expected interrupted activation")
	}
	stored, err := m.Store.Load(r.Name)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Disk != oldDisk {
		t.Fatal("published an uncommitted replacement")
	}
	if _, err := os.Stat(oldDisk); err != nil {
		t.Fatal("removed recovery source", err)
	}
	failDefine = false
	if err := m.Recover(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	stored, err = m.Store.Load(r.Name)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Disk == oldDisk || stored.Source.Image != i.ID {
		t.Fatal("did not complete replacement")
	}
	c, err := m.Store.Credentials(r.Name)
	if err != nil || c.Password != "restored" {
		t.Fatalf("credentials did not follow disk: %+v %v", c, err)
	}
	if _, err := os.Stat(filepath.Join(m.Store.Dir(r.Name), "activate.json")); !os.IsNotExist(err) {
		t.Fatal("recovery journal remains")
	}
}

func TestContinueStoppedVMConsumesMarkerWithoutBooting(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	r.Continuity = &Continuity{Project: "/p", Scene: "one", Session: "s"}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, _ string, args ...string) (string, error) {
		switch args[2] {
		case "domuuid":
			return uuid(r.ID), nil
		case "domstate":
			return "shut off", nil
		default:
			t.Fatalf("must not boot: %v", args)
			return "", nil
		}
	})
	if _, err := m.Begin(context.Background(), r, "continue", "", "one", "/p", true); err == nil {
		t.Fatal("continued stopped stage")
	}
	loaded, err := m.Store.Load(r.Name)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Continuity != nil {
		t.Fatal("failed continuation retained its marker")
	}
}

func TestCollectRetainsCloneBackingAfterOriginDeletion(t *testing.T) {
	m := testManager(t)
	imageIDs := []string{randomID(), randomID(), randomID()}
	for _, id := range imageIDs {
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
	clone := testRecord("clone")
	clone.Source.Image = imageIDs[0]
	if err := m.Store.Save(clone); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(m.Store.Root, "bases", "base.json"), imageIDs[1]); err != nil {
		t.Fatal(err)
	}
	if err := m.Collect(); err != nil {
		t.Fatal(err)
	}
	for _, id := range imageIDs[:2] {
		if _, err := m.Store.Image(id); err != nil {
			t.Fatal("deleted referenced image", err)
		}
	}
	if _, err := m.Store.Image(imageIDs[2]); !os.IsNotExist(err) {
		t.Fatalf("unreferenced image remains: %v", err)
	}
}

func TestExecutorCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := (ExecRunner{}).Run(ctx, nil, "sh", "-c", "sleep 30 & wait")
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("command group not cancelled: %v", err)
	}
}

func TestSizeValidation(t *testing.T) {
	for s, want := range map[string]uint64{"8G": 8 << 30, "512MiB": 512 << 20, "40GiB": 40 << 30} {
		got, err := ParseSize(s)
		if err != nil || got != want {
			t.Fatalf("%s: %d, %v", s, got, err)
		}
	}
	for _, s := range []string{"0", "-1", "99999999999999999999G", "1G;rm"} {
		if _, err := ParseSize(s); err == nil {
			t.Fatalf("accepted %s", s)
		}
	}
	for _, name := range []string{"../demo", "demo/other", "--help", "demo;touch", "", "UPPER"} {
		if ValidateName(name) == nil {
			t.Fatalf("accepted %q", name)
		}
	}
}

func TestDeleteDoesNotTreatConnectionFailureAsMissingDomain(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	m.Runner = runnerFunc(func(context.Context, io.Reader, string, ...string) (string, error) {
		return "", errors.New("connection refused")
	})
	if m.Delete(context.Background(), r) == nil {
		t.Fatal("deleted stage on connection error")
	}
	if _, err := m.Store.Load(r.Name); err != nil {
		t.Fatal("lost registry", err)
	}
}
