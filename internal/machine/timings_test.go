package machine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/guest"
)

func stepClock(step time.Duration) func() time.Time {
	t := time.Unix(1_000, 0)
	return func() time.Time {
		now := t
		t = t.Add(step)
		return now
	}
}

func readProvisionLog(t *testing.T, m *Manager, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(m.Store.Dir(name), "provision.log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestSnapshotWritesTimingLines(t *testing.T) {
	m, r := captureReady(t, "demo")
	var progress bytes.Buffer
	m.Output = &progress
	m.Now = stepClock(time.Second)
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	log := readProvisionLog(t, m, "demo")
	for _, name := range []string{"timing shutdown-seconds", "timing capture-seconds", "timing capture-bytes", "timing capture-apparent-bytes"} {
		if !strings.Contains(log, name) {
			t.Fatalf("missing %s in %s", name, log)
		}
	}
	if !strings.Contains(progress.String(), ">> stage demo: capture (") || !strings.Contains(progress.String(), "GiB)") {
		t.Fatalf("progress: %s", progress.String())
	}
	id := r.Snapshots["hand"]
	if id == "" {
		t.Fatal("snapshot not saved")
	}
	img, err := m.Store.Image(id)
	if err != nil {
		t.Fatal(err)
	}
	var st syscall.Stat_t
	if err := syscall.Stat(img.Disk, &st); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "timing capture-bytes "+strconv.FormatInt(st.Blocks*512, 10)) {
		t.Fatalf("allocated bytes: %s want %d", log, st.Blocks*512)
	}
	if !strings.Contains(log, "timing capture-apparent-bytes "+strconv.FormatInt(st.Size, 10)) {
		t.Fatalf("apparent bytes: %s want %d", log, st.Size)
	}
}

func TestReplaceSnapshotCaptureFailureKeepsShutdown(t *testing.T) {
	m, r := captureReady(t, "demo")
	m.Now = stepClock(time.Second)
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			return "", errors.New("qemu-img convert failed")
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
	result, err := m.ReplaceSnapshot(context.Background(), r, "saved", SnapshotOrigin{Project: "/p", Scene: "s"}, false)
	if err == nil {
		t.Fatal("expected capture failure")
	}
	if result.ShutdownSeconds == nil || *result.ShutdownSeconds != 1 {
		t.Fatalf("shutdown: %+v", result)
	}
	if result.CaptureSeconds != nil || result.CaptureBytes != nil || result.Image != nil {
		t.Fatalf("capture leaked: %+v", result)
	}
	log := readProvisionLog(t, m, "demo")
	if !strings.Contains(log, "timing shutdown-seconds 1.000") {
		t.Fatalf("log: %s", log)
	}
	if strings.Contains(log, "timing capture-seconds") {
		t.Fatalf("failed capture wrote capture-seconds: %s", log)
	}
}

func TestRestoreWritesTimingLines(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	r.Video = "bochs"
	r.Firmware = "firmware"
	r.Disk = m.diskPath(r.ID, "-old.qcow2")
	r.NVRAM = m.diskPath(r.ID, "-old.fd")
	for _, p := range []string{r.Disk, r.NVRAM} {
		if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	imgID := randomID()
	r.Snapshots["initial"] = imgID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := m.Store.SaveCredentials(r.Name, Credentials{Password: "old", Key: "old-key"}); err != nil {
		t.Fatal(err)
	}
	writeCatalogImage(t, m, imgID)
	m.Now = stepClock(2 * time.Second)
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
			return "", nil
		default:
			t.Fatalf("unexpected %v", args)
			return "", nil
		}
	})
	if err := m.Restore(context.Background(), r, "initial"); err != nil {
		t.Fatal(err)
	}
	if m.StartTimes.RestoreStopSeconds == nil || *m.StartTimes.RestoreStopSeconds != 2 {
		t.Fatalf("restore-stop: %+v", m.StartTimes)
	}
	if m.StartTimes.RestoreActivateSeconds == nil || *m.StartTimes.RestoreActivateSeconds != 2 {
		t.Fatalf("restore-activate: %+v", m.StartTimes)
	}
	log := readProvisionLog(t, m, "demo")
	if !strings.Contains(log, "timing restore-stop-seconds 2.000") || !strings.Contains(log, "timing restore-activate-seconds 2.000") {
		t.Fatalf("log: %s", log)
	}
}

func TestBeginKeepsRestoreStopWhenActivateFails(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	r.Video = "bochs"
	r.Firmware = "firmware"
	r.Disk = m.diskPath(r.ID, "-old.qcow2")
	r.NVRAM = m.diskPath(r.ID, "-old.fd")
	for _, p := range []string{r.Disk, r.NVRAM} {
		if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	imgID := randomID()
	r.Snapshots["initial"] = imgID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	writeCatalogImage(t, m, imgID)
	m.Now = stepClock(time.Second)
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
			return "", errors.New("define failed")
		default:
			t.Fatalf("unexpected %v", args)
			return "", nil
		}
	})
	_, err := m.Begin(context.Background(), r, "clean", "initial", "", "/p", true)
	if err == nil {
		t.Fatal("expected activate failure")
	}
	if m.StartTimes.RestoreStopSeconds == nil || *m.StartTimes.RestoreStopSeconds != 1 {
		t.Fatalf("restore-stop lost: %+v", m.StartTimes)
	}
	if m.StartTimes.RestoreActivateSeconds != nil || m.StartTimes.BootSeconds != nil {
		t.Fatalf("later phases leaked: %+v", m.StartTimes)
	}
}

func TestTimingLogFailureDoesNotFailSnapshot(t *testing.T) {
	m, r := captureReady(t, "demo")
	var out bytes.Buffer
	m.Output = &out
	m.Log = func(string) error { return errors.New("log denied") }
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	if r.Snapshots["hand"] == "" {
		t.Fatal("snapshot rolled back")
	}
	if !strings.Contains(out.String(), "warning: timing log:") {
		t.Fatalf("stderr warning: %s", out.String())
	}
}

func stubBootGuest(t *testing.T) {
	t.Helper()
	prev := bootGuest
	bootGuest = func(*guest.Guest, time.Duration) error { return nil }
	t.Cleanup(func() { bootGuest = prev })
}

func virshState(r *Record, state string, qemu func(dest string) error) runnerFunc {
	return runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			if qemu != nil {
				return "", qemu(args[len(args)-1])
			}
			return "", os.WriteFile(args[len(args)-1], []byte("overlay"), 0o600)
		}
		switch args[2] {
		case "domuuid":
			return uuid(r.ID), nil
		case "domstate":
			if state == "" {
				return "", errors.New("domstate failed")
			}
			return state, nil
		case "define":
			return "", nil
		default:
			return "", fmt.Errorf("unexpected %s %v", bin, args)
		}
	})
}

func restoreReady(t *testing.T) (*Manager, *Record) {
	t.Helper()
	m := testManager(t)
	r := testRecord("demo")
	r.Video = "bochs"
	r.Firmware = "firmware"
	r.Disk = m.diskPath(r.ID, "-old.qcow2")
	r.NVRAM = m.diskPath(r.ID, "-old.fd")
	for _, p := range []string{r.Disk, r.NVRAM} {
		if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	imgID := randomID()
	r.Snapshots["initial"] = imgID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := m.Store.SaveCredentials(r.Name, Credentials{Password: "old", Key: "old-key"}); err != nil {
		t.Fatal(err)
	}
	writeCatalogImage(t, m, imgID)
	return m, r
}

func TestBeginReuseDoesNotKeepPriorRestore(t *testing.T) {
	stubBootGuest(t)
	m, r := restoreReady(t)
	m.Runner = virshState(r, "shut off", nil)
	if _, err := m.Begin(context.Background(), r, "clean", "initial", "", "/p", true); err != nil {
		t.Fatal(err)
	}
	if m.StartTimes.RestoreStopSeconds == nil || m.StartTimes.RestoreActivateSeconds == nil {
		t.Fatalf("first begin: %+v", m.StartTimes)
	}
	if _, err := m.Begin(context.Background(), r, "reuse", "", "", "/p", true); err != nil {
		t.Fatal(err)
	}
	if m.StartTimes.RestoreStopSeconds != nil || m.StartTimes.RestoreActivateSeconds != nil {
		t.Fatalf("restore leaked: %+v", m.StartTimes)
	}
}

func TestBeginBootSecondsOnlyWhenDomainWasStopped(t *testing.T) {
	stubBootGuest(t)
	for _, tc := range []struct {
		name, state string
		stateErr    bool
		wantBoot    bool
	}{
		{name: "running", state: "running"},
		{name: "stopped", state: "shut off", wantBoot: true},
		{name: "state-error", stateErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, r := restoreReady(t)
			state := tc.state
			if tc.stateErr {
				state = ""
			}
			m.Runner = virshState(r, state, nil)
			if _, err := m.Begin(context.Background(), r, "reuse", "", "", "/p", true); err != nil {
				t.Fatal(err)
			}
			if tc.wantBoot {
				if m.StartTimes.BootSeconds == nil {
					t.Fatal("missing boot-seconds")
				}
				if !strings.Contains(readProvisionLog(t, m, "demo"), "timing boot-seconds") {
					t.Fatal("missing timing boot-seconds")
				}
				return
			}
			if m.StartTimes.BootSeconds != nil {
				t.Fatalf("boot-seconds: %+v", m.StartTimes)
			}
			if _, err := os.Stat(filepath.Join(m.Store.Dir(r.Name), "provision.log")); err == nil {
				if strings.Contains(readProvisionLog(t, m, "demo"), "timing boot-seconds") {
					t.Fatal("logged boot-seconds")
				}
			}
		})
	}
}

func TestBeginBootExcludesRecover(t *testing.T) {
	stubBootGuest(t)
	m, r := restoreReady(t)
	imgID := r.Snapshots["initial"]
	i, err := m.Store.Image(imgID)
	if err != nil {
		t.Fatal(err)
	}
	i.Firmware = "firmware"
	i.Credentials = Credentials{Password: "restored", Key: "restored-key"}
	failDefine := true
	clock := time.Unix(1_000, 0)
	m.Now = func() time.Time { return clock }
	journal := filepath.Join(m.Store.Dir(r.Name), "activate.json")
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, name string, args ...string) (string, error) {
		if name == "qemu-img" {
			return "", os.WriteFile(args[len(args)-1], []byte("overlay"), 0o600)
		}
		if _, err := os.Stat(journal); err == nil {
			clock = clock.Add(10 * time.Second)
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
	if err := m.activate(context.Background(), r, i, false); err == nil {
		t.Fatal("expected interrupted activation")
	}
	if _, err := os.Stat(journal); err != nil {
		t.Fatal(err)
	}
	failDefine = false
	prevBoot := bootGuest
	bootGuest = func(*guest.Guest, time.Duration) error {
		clock = clock.Add(time.Second)
		return nil
	}
	t.Cleanup(func() { bootGuest = prevBoot })
	if _, err := m.Begin(context.Background(), r, "reuse", "", "", "/p", true); err != nil {
		t.Fatal(err)
	}
	if m.StartTimes.BootSeconds == nil || *m.StartTimes.BootSeconds != 1 {
		got := any(nil)
		if m.StartTimes.BootSeconds != nil {
			got = *m.StartTimes.BootSeconds
		}
		t.Fatalf("boot included recover: %v", got)
	}
}

func TestRestoreRoundsToMillisecond(t *testing.T) {
	m, r := restoreReady(t)
	m.Now = stepClock(1234560 * time.Microsecond)
	m.Runner = virshState(r, "shut off", nil)
	if err := m.Restore(context.Background(), r, "initial"); err != nil {
		t.Fatal(err)
	}
	if m.StartTimes.RestoreStopSeconds == nil || *m.StartTimes.RestoreStopSeconds != 1.235 {
		got := any(nil)
		if m.StartTimes.RestoreStopSeconds != nil {
			got = *m.StartTimes.RestoreStopSeconds
		}
		t.Fatalf("restore-stop: %v", got)
	}
}

func TestPrintCaptureUsesAllocatedGibibyte(t *testing.T) {
	var out bytes.Buffer
	m := &Manager{Output: &out}
	alloc := int64(1610612736)
	m.printCapture(&Record{Name: "demo"}, 12.3, &alloc)
	if out.String() != ">> stage demo: capture (12.3s, 1.5 GiB)\n" {
		t.Fatalf("progress: %q", out.String())
	}
}

func TestNoteCaptureUsesAllocatedBytes(t *testing.T) {
	m, r := captureReady(t, "demo")
	var out bytes.Buffer
	m.Output = &out
	m.Now = stepClock(time.Second)
	prev := imageDiskSizes
	imageDiskSizes = func(string) (int64, int64, error) { return 1610612736, 99, nil }
	t.Cleanup(func() { imageDiskSizes = prev })
	began := m.now()
	secs, alloc, appar := m.noteCapture(r, began, "unused")
	if secs == nil || alloc == nil || *alloc != 1610612736 || appar == nil || *appar != 99 {
		t.Fatalf("noteCapture %v %v %v", secs, alloc, appar)
	}
	if out.String() != ">> stage demo: capture (1.0s, 1.5 GiB)\n" {
		t.Fatalf("progress: %q", out.String())
	}
}

func TestNoteCaptureStatFailureOmitsSize(t *testing.T) {
	m, r := captureReady(t, "demo")
	var out bytes.Buffer
	m.Output = &out
	m.Now = stepClock(time.Second)
	prev := imageDiskSizes
	imageDiskSizes = func(string) (int64, int64, error) { return 0, 0, errors.New("stat denied") }
	t.Cleanup(func() { imageDiskSizes = prev })
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	if r.Snapshots["hand"] == "" {
		t.Fatal("snapshot rolled back")
	}
	got := out.String()
	if !strings.Contains(got, "warning: timing log: image size:") {
		t.Fatalf("warning: %s", got)
	}
	if !strings.Contains(got, ">> stage demo: capture (1.0s)\n") || strings.Contains(got, "GiB") {
		t.Fatalf("progress: %s", got)
	}
	log := readProvisionLog(t, m, "demo")
	if !strings.Contains(log, "timing capture-seconds") || strings.Contains(log, "timing capture-bytes") {
		t.Fatalf("log: %s", log)
	}
}
