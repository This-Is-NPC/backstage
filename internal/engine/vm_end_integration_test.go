package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

// Opt-in only. Registers cleanup before Create so a partial accept-* is
// deleted even when Create fails. Installation plus two takes needs a
// context longer than the default 10m test timeout — run with
//
//	BACKSTAGE_VM_INTEGRATION=1 go test ./internal/engine -run TestRealVMEndProducerConsumer -v -timeout 180m -count=1
func TestRealVMEndProducerConsumer(t *testing.T) {
	if os.Getenv("BACKSTAGE_VM_INTEGRATION") != "1" {
		t.Skip("set BACKSTAGE_VM_INTEGRATION=1 to provision real VMs")
	}
	if _, err := exec.LookPath("guestfish"); err != nil {
		t.Fatal("install libguestfs before the full VM acceptance test")
	}
	m, err := machine.New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Minute)
	defer cancel()
	var id [4]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	name := "accept-" + hex.EncodeToString(id[:])
	t.Cleanup(func() {
		logProvisionTimingLines(t, filepath.Join(m.Store.Dir(name), "provision.log"))
		t.Logf("cleaning stage %s", name)
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		unlock, lockErr := m.Store.LockWait(clean, name, "image-catalog")
		if lockErr != nil {
			t.Errorf("cleanup lock %s: %v", name, lockErr)
			return
		}
		defer unlock()
		rec, loadErr := m.Store.Load(name)
		if loadErr != nil {
			if errors.Is(loadErr, os.ErrNotExist) {
				return
			}
			t.Logf("cleanup load %s: %v", name, loadErr)
			return
		}
		if delErr := m.Delete(clean, rec); delErr != nil {
			t.Errorf("cleanup delete %s: %v", name, delErr)
		}
	})
	release, err := m.Store.LockMany(name, "image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	spec := machine.DefaultSpec()
	if v := os.Getenv("BACKSTAGE_TEST_OMARCHY"); v != "" {
		spec.Omarchy = v
	}
	if _, err := m.Create(ctx, name, spec); err != nil {
		t.Logf("create %s failed; cleanup will delete a partial record: %v", name, err)
		release()
		t.Fatalf("create %s (retained for diagnosis until cleanup): %v", name, err)
	}
	release()

	dir := t.TempDir()
	p := &scene.Project{
		Dir:    dir,
		Record: scene.RecordCfg{Out: "recordings", FPS: 30},
		Layouts: map[string]scene.Layout{
			"solo": {Panes: []scene.Pane{{Name: "t"}}},
		},
		VMs: map[string]scene.VMCfg{
			"box": {Stage: name},
		},
	}
	producer := &scene.Scene{
		Name:   "make-ready",
		Layout: "solo",
		VM:     "box",
		VMStart: &scene.VMStart{
			Mode:     "clean",
			Snapshot: "initial",
		},
		VMEnd: &scene.VMEnd{Snapshot: "ready"},
		Steps: []scene.Step{{Action: "wait"}},
	}
	if err := producer.Validate(p); err != nil {
		t.Fatal(err)
	}
	eng, err := NewForScene(p, producer)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Run(producer, Options{Context: ctx, Record: true, Speed: 1, Version: "test"}); err != nil {
		t.Fatal(err)
	}
	producerFacts := readFacts(t, filepath.Join(dir, "recordings", "make-ready.mp4"))
	logClipTimings(t, "producer", producerFacts.Timings)
	if producerFacts.Timings == nil || producerFacts.Timings.ShutdownSeconds == nil || producerFacts.Timings.CaptureSeconds == nil || producerFacts.Timings.CaptureBytes == nil {
		t.Fatalf("producer timings: %+v", producerFacts.Timings)
	}
	r, err := m.Store.Load(name)
	if err != nil {
		t.Fatal(err)
	}
	image, ok := r.Snapshots["ready"]
	if !ok || image == "" {
		t.Fatalf("producer mapping: %#v", r.Snapshots)
	}
	origin, ok := r.Origin("ready")
	if !ok || origin.Scene != "make-ready" || origin.Image != image || origin.Take != machine.TakeRecording {
		t.Fatalf("producer origin: %+v", origin)
	}

	consumer := &scene.Scene{
		Name:   "use-ready",
		Layout: "solo",
		VM:     "box",
		VMStart: &scene.VMStart{
			Mode:     "clean",
			Snapshot: "ready",
		},
		Steps: []scene.Step{{Action: "wait"}},
	}
	if err := consumer.Validate(p); err != nil {
		t.Fatal(err)
	}
	eng, err = NewForScene(p, consumer)
	if err != nil {
		t.Fatal(err)
	}
	r, err = m.Store.Load(name)
	if err != nil {
		t.Fatal(err)
	}
	preBegin := logConsumerAtState(t, r)
	if err := eng.Run(consumer, Options{Context: ctx, Record: true, Speed: 1, Version: "test"}); err != nil {
		t.Fatal(err)
	}
	got := readFacts(t, filepath.Join(dir, "recordings", "use-ready.mp4"))
	logClipTimings(t, "consumer", got.Timings)
	if got.StartImage != image {
		t.Fatalf("start-image = %q, want %q", got.StartImage, image)
	}
	if got.StartState == nil || got.StartState.Snapshot != "ready" {
		t.Fatalf("start-state: %+v", got.StartState)
	}
	if got.Result != facts.ResultOK {
		t.Fatalf("consumer facts: %+v", got)
	}
	if !consumerSkippedRestore(got.Timings) {
		t.Fatalf("consumer did not skip restore\ntimings: %+v\npre-begin at-state:\n%s", got.Timings, preBegin)
	}
	requireProvisionTimings(t, filepath.Join(m.Store.Dir(name), "provision.log"),
		"shutdown-seconds", "capture-seconds", "capture-bytes",
		"restore-stop-seconds", "restore-activate-seconds", "boot-seconds", "session-seconds",
		"capture-mode", "image-depth", "restore-skipped")
}

func consumerSkippedRestore(tm *facts.Timings) bool {
	return tm != nil &&
		tm.RestoreSkipped != nil && *tm.RestoreSkipped &&
		tm.RestoreStopSeconds == nil &&
		tm.RestoreActivateSeconds == nil &&
		tm.BootSeconds != nil &&
		tm.SessionSeconds != nil
}

type atStateProbe struct {
	Snapshot string            `json:"snapshot,omitempty"`
	Image    string            `json:"image,omitempty"`
	Recorded *machine.AtState  `json:"recorded"`
	DiskNow  machine.FilePrint `json:"disk-now"`
	DiskErr  string            `json:"disk-err,omitempty"`
	NVRAMNow machine.FilePrint `json:"nvram-now"`
	NVRAMErr string            `json:"nvram-err,omitempty"`
}

func (p atStateProbe) String() string {
	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(body)
}

func logConsumerAtState(t *testing.T, r *machine.Record) atStateProbe {
	t.Helper()
	p := probeAtState(r)
	t.Logf("consumer pre-begin at-state:\n%s", p)
	return p
}

func probeAtState(r *machine.Record) atStateProbe {
	p := atStateProbe{}
	if r != nil && r.AtState != nil {
		cp := *r.AtState
		p.Recorded = &cp
		p.Snapshot = cp.Snapshot
		p.Image = cp.Image
	}
	if r == nil {
		return p
	}
	if disk, err := statPrint(r.Disk); err != nil {
		p.DiskErr = err.Error()
	} else {
		p.DiskNow = disk
	}
	if nvram, err := statPrint(r.NVRAM); err != nil {
		p.NVRAMErr = err.Error()
	} else {
		p.NVRAMNow = nvram
	}
	return p
}

func logClipTimings(t *testing.T, label string, tm *facts.Timings) {
	t.Helper()
	body, err := json.MarshalIndent(tm, "", "  ")
	if err != nil {
		t.Fatalf("%s timings json: %v", label, err)
	}
	t.Logf("%s timings:\n%s", label, body)
}

func logProvisionTimingLines(t *testing.T, path string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Logf("provision.log: %v", err)
		return
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, "timing ") {
			t.Logf("%s", line)
		}
	}
}

func requireProvisionTimings(t *testing.T, path string, names ...string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("provision.log: %v", err)
	}
	for _, name := range names {
		if !strings.Contains(string(body), "timing "+name) {
			t.Fatalf("missing timing %s in %s", name, path)
		}
	}
}
