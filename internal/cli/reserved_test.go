package cli

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/machine"
)

func TestVerifyReservedStage(t *testing.T) {
	store := cliStore(t)
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	held, release, err := store.LockHold("demo")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fd := int(held[0].File.Fd())
	if err := verifyReservedStage(store, "demo", fd); err != nil {
		t.Fatal(err)
	}
	if err := verifyReservedStage(store, "demo", -1); err == nil || !strings.Contains(err.Error(), "missing lock fd") {
		t.Fatalf("missing fd: %v", err)
	}
	if err := verifyReservedStage(store, "", fd); err == nil || !strings.Contains(err.Error(), "missing name") {
		t.Fatalf("missing name: %v", err)
	}
	other, err := os.CreateTemp(t.TempDir(), "not-lock")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := verifyReservedStage(store, "demo", int(other.Fd())); err == nil || !strings.Contains(err.Error(), "fd is not") {
		t.Fatalf("other file: %v", err)
	}
	lab, labRel, err := store.LockHold("lab")
	if err != nil {
		t.Fatal(err)
	}
	defer labRel()
	if err := verifyReservedStage(store, "demo", int(lab[0].File.Fd())); err == nil || !strings.Contains(err.Error(), "fd is not") {
		t.Fatalf("other stage: %v", err)
	}
}

func TestPlayReservedFlagFailsBeforeAction(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	store, err := machine.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	_, createdRel, err := store.LockHold("demo")
	if err != nil {
		t.Fatal(err)
	}
	createdRel()

	cmd := playCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--internal-reserved-stage", "demo", "missing.json"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "missing lock fd") {
		t.Fatalf("flag without fd: %v", err)
	}

	other, err := os.CreateTemp(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	cmd = playCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--internal-reserved-stage", "demo", "--internal-reserved-fd", strconv.Itoa(int(other.Fd())), "missing.json"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "fd is not") {
		t.Fatalf("other file fd: %v", err)
	}

	held, release, err := store.LockHold("lab")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cmd = playCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--internal-reserved-stage", "demo", "--internal-reserved-fd", strconv.Itoa(int(held[0].File.Fd())), "missing.json"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "fd is not") {
		t.Fatalf("other stage fd: %v", err)
	}

	demo, demoRel, err := store.LockHold("demo")
	if err != nil {
		t.Fatal(err)
	}
	defer demoRel()
	if err := verifyReservedStage(store, "demo", int(demo[0].File.Fd())); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyReservedStageRefusesUnlockedOFD(t *testing.T) {
	store := cliStore(t)
	held, release, err := store.LockHold("demo")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	other, err := os.Open(store.LockPath("demo"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if int(other.Fd()) == int(held[0].File.Fd()) {
		t.Fatal("expected a second open file description")
	}
	err = verifyReservedStage(store, "demo", int(other.Fd()))
	if err == nil || !strings.Contains(err.Error(), "lock not held") {
		t.Fatalf("new OFD while another holder has the lock: %v", err)
	}
}

func TestFlockRelockOnSameFD(t *testing.T) {
	store := cliStore(t)
	held, release, err := store.LockHold("demo")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fd := int(held[0].File.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("relock: %v", err)
	}
}
