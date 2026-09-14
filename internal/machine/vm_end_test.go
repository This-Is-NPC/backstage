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

func TestBeginRefusesRecordingFromRehearsal(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	r.Snapshots["ready"] = randomID()
	r.SnapshotOrigins = map[string]SnapshotOrigin{"ready": {
		Project: "/proj", Scene: "alpha", Take: TakeRehearsal,
	}}
	r.Continuity = &Continuity{Project: "/proj", Scene: "prev", Recording: true, Session: "boot\nhypr"}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	_, err := m.Begin(context.Background(), r, "clean", "ready", "", "/proj", true)
	if err == nil || !strings.Contains(err.Error(), "rehearsal") {
		t.Fatalf("recording from rehearsal: %v", err)
	}
	if r.Continuity == nil || r.Continuity.Scene != "prev" {
		t.Fatal("consumed continuity before refusing")
	}
}

func TestLockManyBusyIsErrBusy(t *testing.T) {
	m := testManager(t)
	hold, err := m.Store.LockMany("image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	_, err = m.Store.LockMany("image-catalog")
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
}

func TestLockWaitAcquiresAfterReleaseAndRespectsCancel(t *testing.T) {
	m := testManager(t)
	hold, err := m.Store.LockMany("image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := m.Store.LockWait(ctx, "image-catalog")
		errCh <- err
	}()
	select {
	case err := <-errCh:
		hold()
		t.Fatalf("LockWait returned while held: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		hold()
		t.Fatal("LockWait ignored cancel")
	}
	hold()

	hold, err = m.Store.LockMany("image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(120 * time.Millisecond)
		hold()
	}()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer waitCancel()
	release, err := m.Store.LockWait(waitCtx, "image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestCaptureErrorRemovesPartialImage(t *testing.T) {
	m, r := captureReady(t, "demo")
	var dest string
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			dest = args[len(args)-1]
			if err := os.WriteFile(dest, []byte("partial"), 0o600); err != nil {
				return "", err
			}
			return "", errors.New("qemu-img killed")
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
	if _, err := m.capture(context.Background(), r); err == nil {
		t.Fatal("expected capture error")
	}
	if dest == "" {
		t.Fatal("qemu-img was not called")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("partial remains: %v", err)
	}
}

func TestCaptureCancelRemovesCreatedFiles(t *testing.T) {
	m, r := captureReady(t, "demo")
	ctx, cancel := context.WithCancel(context.Background())
	var dest string
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			dest = args[len(args)-1]
			if err := os.WriteFile(dest, []byte("image"), 0o600); err != nil {
				return "", err
			}
			cancel()
			return "", nil
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
	if _, err := m.capture(ctx, r); err == nil {
		t.Fatal("expected cancel")
	}
	if dest == "" {
		t.Fatal("qemu-img was not called")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("partial remains: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(m.Store.Root, "images", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("leftover images: %v", matches)
	}
}

func TestReplaceSnapshotCancelAfterCaptureRemovesImage(t *testing.T) {
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
	ctx, cancel := context.WithCancel(context.Background())
	var captured string
	stubCapture(func(mgr *Manager, ctx context.Context, rec *Record) (*Image, error) {
		img, err := mgr.capture(ctx, rec)
		if err == nil {
			captured = img.ID
		}
		cancel()
		return img, err
	})
	t.Cleanup(restoreDefaultCapture)
	result, err := m.ReplaceSnapshot(ctx, r, "ready", testOrigin("/proj", "alpha"), false)
	if err == nil {
		t.Fatal("expected cancel")
	}
	if result.CaptureSeconds == nil || result.CaptureBytes == nil || result.CaptureApparentBytes == nil {
		t.Fatalf("capture timings lost: %+v", result)
	}
	assertUnchanged(t, m, "ready", old, origin)
	if captured == "" {
		t.Fatal("capture did not produce an image")
	}
	if _, err := m.Store.Image(captured); !os.IsNotExist(err) {
		t.Fatalf("cancelled capture left an image: %v", err)
	}
}

func TestReplaceSnapshotLockWaitRespectsCancel(t *testing.T) {
	m, r := captureReady(t, "demo")
	hold, err := m.Store.LockMany("image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := m.ReplaceSnapshot(ctx, r, "ready", testOrigin("/proj", "alpha"), false)
		errCh <- err
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting replace: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReplaceSnapshot ignored cancel")
	}
}
