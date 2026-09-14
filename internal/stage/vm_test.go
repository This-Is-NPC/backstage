package stage

import (
	"errors"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/guest"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestVMSetupRecordsCompletedPhases(t *testing.T) {
	v := &VM{Guest: &guest.Guest{Domain: "box"}, Patience: time.Second}
	t0 := time.Unix(0, 0)
	v.Now = func() time.Time {
		now := t0
		t0 = t0.Add(time.Second)
		return now
	}
	if _, err := v.setup(scene.Layout{}, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	want := []string{"up", "omarchy", "tools", "desktop", "terminal"}
	if len(v.Phases) != len(want) {
		t.Fatalf("phases %+v", v.Phases)
	}
	for _, name := range want {
		if v.Phases[name] != 1 {
			t.Fatalf("%s = %v", name, v.Phases[name])
		}
	}
	if v.SessionSeconds == nil || *v.SessionSeconds != 4 {
		t.Fatalf("session %v", v.SessionSeconds)
	}
	if _, ok := v.Phases["up"]; !ok {
		t.Fatal("up missing")
	}
}

func TestVMSetupFailedPhaseDropsLater(t *testing.T) {
	v := &VM{Guest: &guest.Guest{Domain: "box"}, Patience: time.Second}
	t0 := time.Unix(0, 0)
	v.Now = func() time.Time {
		now := t0
		t0 = t0.Add(500 * time.Millisecond)
		return now
	}
	_, err := v.setup(scene.Layout{}, func(name string) error {
		if name == "tools" {
			return errors.New("provision failed")
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	if _, ok := v.Phases["tools"]; ok {
		t.Fatalf("failed phase recorded: %+v", v.Phases)
	}
	if _, ok := v.Phases["desktop"]; ok {
		t.Fatalf("desktop after failure: %+v", v.Phases)
	}
	if _, ok := v.Phases["terminal"]; ok {
		t.Fatalf("terminal after failure: %+v", v.Phases)
	}
	if v.Phases["up"] != 0.5 || v.Phases["omarchy"] != 0.5 {
		t.Fatalf("completed: %+v", v.Phases)
	}
	if v.SessionSeconds == nil || *v.SessionSeconds != 0.5 {
		t.Fatalf("session %v", v.SessionSeconds)
	}
}

func TestVMSetupRoundsToMillisecond(t *testing.T) {
	v := &VM{Guest: &guest.Guest{Domain: "box"}}
	t0 := time.Unix(0, 0)
	v.Now = func() time.Time {
		now := t0
		t0 = t0.Add(1234560 * time.Microsecond)
		return now
	}
	if _, err := v.setup(scene.Layout{}, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if v.Phases["up"] != 1.235 {
		t.Fatalf("up %v", v.Phases["up"])
	}
}

func TestVMContinueOnlyOmarchy(t *testing.T) {
	v := &VM{Guest: &guest.Guest{Domain: "box"}, Continue: true}
	t0 := time.Unix(0, 0)
	v.Now = func() time.Time {
		now := t0
		t0 = t0.Add(time.Second)
		return now
	}
	if _, err := v.setup(scene.Layout{}, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(v.Phases) != 1 || v.Phases["omarchy"] != 1 {
		t.Fatalf("continue phases %+v", v.Phases)
	}
	if v.SessionSeconds == nil || *v.SessionSeconds != 1 {
		t.Fatalf("session %v", v.SessionSeconds)
	}
}
