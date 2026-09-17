package workspace

import (
	"testing"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/machine"
)

func TestStatusOKIgnoresTimings(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	writeBaseProject(t, dir)
	writeScene(t, dir, "make", cleanScene("make", "", "ready"))
	saveStage(t, store, "demo", map[string]string{"initial": imgInitial, "ready": imgReady}, map[string]machine.SnapshotOrigin{
		"ready": {Project: dir, Scene: "make", Take: machine.TakeRecording, Image: imgReady},
	})
	p, s := load(t, dir, "make")
	rec, _ := store.Load("demo")
	first := publish(t, p, s, rec, facts.Facts{})
	without, err := facts.Read(first.StableFacts)
	if err != nil {
		t.Fatal(err)
	}
	shutdown := 6.7
	second := publish(t, p, s, rec, facts.Facts{Timings: &facts.Timings{ShutdownSeconds: &shutdown, StagePhases: map[string]float64{"up": 0.2}}})
	with, err := facts.Read(second.StableFacts)
	if err != nil {
		t.Fatal(err)
	}
	if with.Timings == nil || with.Timings.ShutdownSeconds == nil {
		t.Fatalf("timings dropped: %+v", with.Timings)
	}
	if without.InputsSHA256 == "" || without.InputsSHA256 != with.InputsSHA256 {
		t.Fatalf("digest changed: %s %s", without.InputsSHA256, with.InputsSHA256)
	}
	got := statusOf(t, report(t, dir, store), "make")
	if got.Status != OK {
		t.Fatalf("status %s reasons %v", got.Status, got.Reasons)
	}
}
