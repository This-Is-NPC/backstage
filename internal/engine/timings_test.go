package engine

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/guest"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

func fptr(v float64) *float64 { return &v }
func iptr(v int64) *int64     { return &v }

func timedManaged(t *testing.T, dir string, phases map[string]float64, session *float64) *Engine {
	t.Helper()
	e, _ := managedEngine(t, dir)
	ord := []string{}
	g := guest.New("dom", "user", "", "", "")
	g.StageName = "demo"
	e.Stager = &fakeVMStager{
		fakeStager: fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		g:          g,
		omarchy:    "1.0",
		phases:     phases,
		session:    session,
	}
	return e
}

func TestRunCleanVMEndWritesAllTimings(t *testing.T) {
	dir := t.TempDir()
	sess := 12.8
	e := timedManaged(t, dir, map[string]float64{"up": 0.2, "omarchy": 1.1, "tools": 8, "desktop": 3.2, "terminal": 0.5}, &sess)
	e.Managed.StartTimes = machine.StartTimes{
		RestoreStopSeconds:     fptr(12.3),
		RestoreActivateSeconds: fptr(4.1),
		BootSeconds:            fptr(45.2),
	}
	prev := replaceTakeState
	replaceTakeState = func(*machine.Manager, context.Context, *machine.Record, string, machine.SnapshotOrigin, bool) (machine.ReplaceResult, error) {
		mode, depth := "delta", 1
		return machine.ReplaceResult{
			Image:                &machine.Image{ID: "captured"},
			ShutdownSeconds:      fptr(6.7),
			CaptureSeconds:       fptr(22.4),
			CaptureBytes:         iptr(4402343936),
			CaptureApparentBytes: iptr(42949672960),
			CaptureMode:          &mode,
			ImageDepth:           &depth,
		}, nil
	}
	t.Cleanup(func() { replaceTakeState = prev })
	clip := filepath.Join(dir, "demo.mp4")
	if err := e.Run(vmEndScene("clean", "initial", ""), Options{Record: true, OutPath: clip, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	got := readFacts(t, clip)
	if got.Timings == nil {
		t.Fatal("missing timings")
	}
	assertFloat(t, got.Timings.RestoreStopSeconds, 12.3)
	assertFloat(t, got.Timings.RestoreActivateSeconds, 4.1)
	assertFloat(t, got.Timings.BootSeconds, 45.2)
	assertFloat(t, got.Timings.SessionSeconds, 12.8)
	assertFloat(t, got.Timings.ShutdownSeconds, 6.7)
	assertFloat(t, got.Timings.CaptureSeconds, 22.4)
	if got.Timings.CaptureBytes == nil || *got.Timings.CaptureBytes != 4402343936 {
		t.Fatalf("bytes %+v", got.Timings.CaptureBytes)
	}
	if got.Timings.CaptureMode == nil || *got.Timings.CaptureMode != "delta" {
		t.Fatalf("mode %+v", got.Timings.CaptureMode)
	}
	if got.Timings.ImageDepth == nil || *got.Timings.ImageDepth != 1 {
		t.Fatalf("depth %+v", got.Timings.ImageDepth)
	}
	if got.Timings.StagePhases["up"] != 0.2 || got.Timings.StagePhases["omarchy"] != 1.1 {
		t.Fatalf("phases %+v", got.Timings.StagePhases)
	}
}

func TestRunReuseAndContinueOmitRestore(t *testing.T) {
	dir := t.TempDir()
	reuseSess := 4.0
	e := timedManaged(t, dir, map[string]float64{"up": 0.1, "omarchy": 1, "tools": 1, "desktop": 1, "terminal": 1}, &reuseSess)
	e.Managed.StartTimes = machine.StartTimes{BootSeconds: fptr(9)}
	clip := filepath.Join(dir, "reuse.mp4")
	if err := e.Run(&scene.Scene{Name: "demo", Layout: "solo", VMStart: &scene.VMStart{Mode: "reuse"}, Steps: []scene.Step{{Action: "wait"}}}, Options{Record: true, OutPath: clip, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	got := readFacts(t, clip)
	if got.Timings == nil || got.Timings.RestoreStopSeconds != nil || got.Timings.RestoreActivateSeconds != nil {
		t.Fatalf("reuse restore: %+v", got.Timings)
	}
	assertFloat(t, got.Timings.BootSeconds, 9)
	if got.Timings.ShutdownSeconds != nil || got.Timings.CaptureSeconds != nil {
		t.Fatalf("reuse capture: %+v", got.Timings)
	}

	contSess := 1.1
	e = timedManaged(t, dir, map[string]float64{"omarchy": 1.1}, &contSess)
	e.Managed.StartTimes = machine.StartTimes{}
	clip = filepath.Join(dir, "cont.mp4")
	if err := e.Run(&scene.Scene{Name: "demo", Layout: "solo", VMStart: &scene.VMStart{Mode: "continue", After: "demo"}, Steps: []scene.Step{{Action: "wait"}}}, Options{Record: true, OutPath: clip, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	got = readFacts(t, clip)
	if got.Timings == nil || got.Timings.RestoreStopSeconds != nil || got.Timings.BootSeconds != nil {
		t.Fatalf("continue restore/boot: %+v", got.Timings)
	}
	if len(got.Timings.StagePhases) != 1 || got.Timings.StagePhases["omarchy"] != 1.1 {
		t.Fatalf("continue phases %+v", got.Timings.StagePhases)
	}
}

func TestRunWithoutVMEndOmitsCapture(t *testing.T) {
	dir := t.TempDir()
	sess := 1.0
	e := timedManaged(t, dir, map[string]float64{"up": 0.1, "omarchy": 1}, &sess)
	e.Managed.StartTimes = machine.StartTimes{BootSeconds: fptr(3)}
	clip := filepath.Join(dir, "nocap.mp4")
	if err := e.Run(&scene.Scene{Name: "demo", Layout: "solo", VMStart: &scene.VMStart{Mode: "reuse"}, Steps: []scene.Step{{Action: "wait"}}}, Options{Record: true, OutPath: clip, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	got := readFacts(t, clip)
	if got.Timings == nil || got.Timings.ShutdownSeconds != nil || got.Timings.CaptureSeconds != nil || got.Timings.CaptureBytes != nil {
		t.Fatalf("capture fields: %+v", got.Timings)
	}
}

func TestSaveEndStateCaptureFailureKeepsShutdown(t *testing.T) {
	dir := t.TempDir()
	e := playEngine(t, dir, nil)
	e.Managed = &machine.Manager{}
	e.managedRec = &machine.Record{}
	e.timings = facts.Timings{BootSeconds: fptr(3)}
	clip := filepath.Join(dir, "work.mp4")
	prev := replaceTakeState
	replaceTakeState = func(*machine.Manager, context.Context, *machine.Record, string, machine.SnapshotOrigin, bool) (machine.ReplaceResult, error) {
		return machine.ReplaceResult{ShutdownSeconds: fptr(6.7)}, errors.New("qemu-img convert failed")
	}
	t.Cleanup(func() { replaceTakeState = prev })
	err := e.saveEndState(context.Background(), &scene.Scene{Name: "demo", VMEnd: &scene.VMEnd{Snapshot: "ready"}}, clip, Options{Record: true, Version: "v"})
	if !errors.Is(err, ErrCaptureFailed) {
		t.Fatalf("err %v", err)
	}
	got := readFacts(t, clip)
	if got.Result != facts.ResultCaptureFailed {
		t.Fatalf("result %s", got.Result)
	}
	if got.Timings == nil || got.Timings.ShutdownSeconds == nil || *got.Timings.ShutdownSeconds != 6.7 {
		t.Fatalf("shutdown missing: %+v", got.Timings)
	}
	if got.Timings.CaptureSeconds != nil {
		t.Fatalf("capture-seconds on failure: %+v", got.Timings)
	}
	assertFloat(t, got.Timings.BootSeconds, 3)
}

func TestTimingLogFailureDoesNotFailTake(t *testing.T) {
	dir := t.TempDir()
	sess := 1.0
	e := timedManaged(t, dir, map[string]float64{"omarchy": 1}, &sess)
	var out bytes.Buffer
	e.Managed.Output = &out
	e.Managed.Log = func(string) error { return errors.New("log denied") }
	clip := filepath.Join(dir, "ok.mp4")
	if err := e.Run(&scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}, Options{Record: true, OutPath: clip, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	if readFacts(t, clip).Result != facts.ResultOK {
		t.Fatal("take failed")
	}
	if !strings.Contains(out.String(), "warning: timing log:") {
		t.Fatalf("warning: %s", out.String())
	}
}

func TestRunDoesNotCarryCaptureToNextTake(t *testing.T) {
	dir := t.TempDir()
	sess := 1.0
	e := timedManaged(t, dir, map[string]float64{"omarchy": 1}, &sess)
	prev := replaceTakeState
	replaceTakeState = func(*machine.Manager, context.Context, *machine.Record, string, machine.SnapshotOrigin, bool) (machine.ReplaceResult, error) {
		return machine.ReplaceResult{
			Image:           &machine.Image{ID: "captured"},
			ShutdownSeconds: fptr(6.7),
			CaptureSeconds:  fptr(2.2),
			CaptureBytes:    iptr(8),
		}, nil
	}
	t.Cleanup(func() { replaceTakeState = prev })
	first := filepath.Join(dir, "one.mp4")
	if err := e.Run(vmEndScene("reuse", "", ""), Options{Record: true, OutPath: first, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	if readFacts(t, first).Timings == nil || readFacts(t, first).Timings.ShutdownSeconds == nil {
		t.Fatal("first take missing capture")
	}
	second := filepath.Join(dir, "two.mp4")
	if err := e.Run(&scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}, Options{Record: true, OutPath: second, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	got := readFacts(t, second)
	if got.Timings != nil && (got.Timings.ShutdownSeconds != nil || got.Timings.CaptureSeconds != nil || got.Timings.CaptureBytes != nil) {
		t.Fatalf("capture leaked: %+v", got.Timings)
	}
}

func TestExternalVMWritesSessionTimings(t *testing.T) {
	dir := t.TempDir()
	sess := 2.5
	var ord []string
	g := guest.New("box", "user", "", "", "")
	g.StageName = "ext"
	e := playEngine(t, dir, nil)
	e.Stager = &fakeVMStager{
		fakeStager: fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		g:          g,
		omarchy:    "1.0",
		phases:     map[string]float64{"up": 0.2, "omarchy": 2.5},
		session:    &sess,
	}
	clip := filepath.Join(dir, "ext.mp4")
	if err := e.Run(&scene.Scene{Name: "ext", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}, Options{Record: true, OutPath: clip, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	got := readFacts(t, clip)
	if got.Timings == nil {
		t.Fatal("missing timings")
	}
	if got.Timings.RestoreStopSeconds != nil || got.Timings.BootSeconds != nil || got.Timings.ShutdownSeconds != nil {
		t.Fatalf("machine fields: %+v", got.Timings)
	}
	if got.Timings.StagePhases["up"] != 0.2 || got.Timings.StagePhases["omarchy"] != 2.5 {
		t.Fatalf("phases %+v", got.Timings.StagePhases)
	}
	assertFloat(t, got.Timings.SessionSeconds, 2.5)
}

func TestTimingsJSONFieldNames(t *testing.T) {
	dir := t.TempDir()
	sess := 1.0
	e := timedManaged(t, dir, map[string]float64{"up": 0.2, "omarchy": 1}, &sess)
	e.Managed.StartTimes = machine.StartTimes{BootSeconds: fptr(2)}
	clip := filepath.Join(dir, "names.mp4")
	if err := e.Run(&scene.Scene{Name: "demo", Layout: "solo", Steps: []scene.Step{{Action: "wait"}}}, Options{Record: true, OutPath: clip, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	body := readFacts(t, clip)
	if body.Timings == nil || body.Timings.StagePhases["up"] != 0.2 {
		t.Fatalf("up: %+v", body.Timings)
	}
}

func assertFloat(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("got %v want %v", got, want)
	}
}
