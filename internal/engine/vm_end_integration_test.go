package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	if err := eng.Run(consumer, Options{Context: ctx, Record: true, Speed: 1, Version: "test"}); err != nil {
		t.Fatal(err)
	}
	got := readFacts(t, filepath.Join(dir, "recordings", "use-ready.mp4"))
	if got.StartImage != image {
		t.Fatalf("start-image = %q, want %q", got.StartImage, image)
	}
	if got.StartState == nil || got.StartState.Snapshot != "ready" {
		t.Fatalf("start-state: %+v", got.StartState)
	}
	if got.Result != facts.ResultOK {
		t.Fatalf("consumer facts: %+v", got)
	}
}
