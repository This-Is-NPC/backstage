package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/This-Is-NPC/backstage/internal/engine"
	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/guest"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/workspace"
)

func TestMain(m *testing.M) {
	switch os.Getenv("BACKSTAGE_TEST_HELPER") {
	case "double-int":
		os.Exit(runDoubleIntHelper())
	case "hold-lock":
		os.Exit(runHoldLockHelper())
	case "spawn-sleep":
		os.Exit(runSpawnSleepHelper())
	case "sleep-pid":
		os.Exit(runSleepPidHelper())
	case "exit-130":
		os.Exit(130)
	case "wait-int":
		os.Exit(runWaitIntHelper())
	}
	os.Exit(m.Run())
}

func runDoubleIntHelper() int {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt)
	n := 0
	for range ch {
		n++
		if n == 1 {
			time.Sleep(200 * time.Millisecond)
			return 130
		}
	}
	return 130
}

func runHoldLockHelper() int {
	time.Sleep(400 * time.Millisecond)
	return 0
}

func runSpawnSleepHelper() int {
	opts := engine.Options{}
	if err := applyInternalChild(&opts, "demo", 3, -1, false); err != nil {
		return 1
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMain")
	cmd.Env = append(os.Environ(), "BACKSTAGE_TEST_HELPER=sleep-pid")
	if err := cmd.Start(); err != nil {
		return 1
	}
	return 0
}

func runSleepPidHelper() int {
	if p := os.Getenv("BACKSTAGE_TEST_PIDFILE"); p != "" {
		_ = os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}
	time.Sleep(10 * time.Second)
	return 0
}

func runWaitIntHelper() int {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	<-ch
	return 130
}

func TestTwoStagesOverlapAndOwnLogs(t *testing.T) {
	dir, store := cliTwoVM(t)
	spans := map[string][2]time.Time{}
	var mu sync.Mutex
	d := testSched(t, store)
	d.Launch = sleepLaunch(80*time.Millisecond, spans, &mu)
	err := d.runStale(dir, engine.Options{Record: true, Speed: 1})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	a, b := spans["alpha"], spans["beta"]
	mu.Unlock()
	if a[0].IsZero() || b[0].IsZero() {
		t.Fatalf("spans %v", spans)
	}
	if !a[0].Before(b[1]) || !b[0].Before(a[1]) {
		t.Fatalf("independent stages must overlap: alpha=%v beta=%v", a, b)
	}
	logs, _ := filepath.Glob(filepath.Join(d.JobsDir, "*", "job-*.log"))
	if len(logs) < 2 {
		t.Fatalf("logs %v", logs)
	}
}

func TestContinueAcrossStagesWaits(t *testing.T) {
	dir := t.TempDir()
	store := cliStore(t)
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {"laptop": {"stage": "demo"}, "lab": {"stage": "lab"}}
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "live.json"), `{"name":"live","layout":"solo","vm":"laptop","vm-start":{"mode":"clean"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeFile(t, filepath.Join(dir, "scenes", "next.json"), `{"name":"next","layout":"solo","vm":"lab","vm-start":{"mode":"continue","after":"live"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	saveStageSpec(t, store, "demo", 2, 2<<30)
	saveStageSpec(t, store, "lab", 2, 2<<30)
	spans := map[string][2]time.Time{}
	var mu sync.Mutex
	d := testSched(t, store)
	d.Launch = sleepLaunch(50*time.Millisecond, spans, &mu)
	if err := d.run(filepath.Join(dir, "scenes", "next.json"), engine.Options{Record: true, Speed: 1}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	live, next := spans["live"], spans["next"]
	mu.Unlock()
	if live[1].IsZero() || next[0].IsZero() || !live[1].Before(next[0]) && !live[1].Equal(next[0]) {
		if next[0].Before(live[1]) {
			t.Fatalf("continue crossed into a live take: live=%v next=%v", live, next)
		}
	}
}

func TestHostNeverOverlapsVM(t *testing.T) {
	dir, store := cliHostAndVM(t)
	spans := map[string][2]time.Time{}
	var mu sync.Mutex
	d := testSched(t, store)
	d.Launch = sleepLaunch(80*time.Millisecond, spans, &mu)
	if err := d.runStale(dir, engine.Options{Record: true, Speed: 1}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	h, v := spans["host"], spans["guest"]
	mu.Unlock()
	if h[0].IsZero() || v[0].IsZero() {
		t.Fatalf("spans %v", spans)
	}
	if h[0].Before(v[1]) && v[0].Before(h[1]) {
		t.Fatalf("host overlapped VM: host=%v vm=%v", h, v)
	}
}

func TestVMNotAdmittedWhileHostRunning(t *testing.T) {
	dir, store := cliHostAndVM(t)
	spans := map[string][2]time.Time{}
	var mu sync.Mutex
	d := testSched(t, store)
	d.Launch = sleepLaunch(80*time.Millisecond, spans, &mu)
	host := workspace.PlanStep{ID: "./host", Scene: "host", Path: filepath.Join(dir, "scenes", "host.json"), Reason: workspace.ReasonRequested, Requested: true}
	guest := workspace.PlanStep{ID: "./guest", Scene: "guest", Path: filepath.Join(dir, "scenes", "guest.json"), Stage: "demo", Reason: workspace.ReasonDownstream}
	if err := d.schedule(&workspace.Plan{Steps: []workspace.PlanStep{host, guest}, Stages: []string{"demo"}}, engine.Options{Record: true, Speed: 1}, "stale"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	h, v := spans["host"], spans["guest"]
	mu.Unlock()
	if h[0].IsZero() || v[0].IsZero() {
		t.Fatalf("spans %v", spans)
	}
	if !h[0].Before(v[0]) {
		t.Fatalf("host must start first: host=%v vm=%v", h, v)
	}
	if h[0].Before(v[1]) && v[0].Before(h[1]) {
		t.Fatalf("VM admitted while host was running: host=%v vm=%v", h, v)
	}
}

func TestVMNotAdmittedWhileHostWaiting(t *testing.T) {
	dir, store := cliHostAndVM(t)
	spans := map[string][2]time.Time{}
	var mu sync.Mutex
	d := testSched(t, store)
	d.Launch = func(ctx context.Context, spec jobSpec) (jobProc, error) {
		if spec.Step.Scene == "host" && spec.Progress != nil {
			writeProgress(spec.Progress, jobWaiting, phaseCatalog)
		}
		return sleepLaunch(80*time.Millisecond, spans, &mu)(ctx, spec)
	}
	host := workspace.PlanStep{ID: "./host", Scene: "host", Path: filepath.Join(dir, "scenes", "host.json"), Reason: workspace.ReasonRequested, Requested: true}
	guest := workspace.PlanStep{ID: "./guest", Scene: "guest", Path: filepath.Join(dir, "scenes", "guest.json"), Stage: "demo", Reason: workspace.ReasonDownstream}
	if err := d.schedule(&workspace.Plan{Steps: []workspace.PlanStep{host, guest}, Stages: []string{"demo"}}, engine.Options{Record: true, Speed: 1}, "stale"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	h, v := spans["host"], spans["guest"]
	mu.Unlock()
	if h[0].IsZero() || v[0].IsZero() {
		t.Fatalf("spans %v", spans)
	}
	if h[0].Before(v[1]) && v[0].Before(h[1]) {
		t.Fatalf("VM admitted while host was waiting: host=%v vm=%v", h, v)
	}
	if !strings.Contains(d.Out.(*bytes.Buffer).String(), "waiting image-catalog") {
		t.Fatalf("host never reported waiting:\n%s", d.Out.(*bytes.Buffer).String())
	}
}

func TestSameStageNeverOverlaps(t *testing.T) {
	dir := t.TempDir()
	store := cliStore(t)
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {"laptop": {"stage": "demo"}}
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "alpha.json"), `{"name":"alpha","layout":"solo","vm":"laptop","vm-start":{"mode":"clean"},"vm-end":{"snapshot":"ready"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeFile(t, filepath.Join(dir, "scenes", "gamma.json"), `{"name":"gamma","layout":"solo","vm":"laptop","vm-start":{"mode":"clean"},"vm-end":{"snapshot":"other"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	saveStageSpec(t, store, "demo", 2, 2<<30)
	spans := map[string][2]time.Time{}
	var mu sync.Mutex
	d := testSched(t, store)
	d.Launch = sleepLaunch(80*time.Millisecond, spans, &mu)
	if err := d.runStale(dir, engine.Options{Record: true, Speed: 1}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	a, g := spans["alpha"], spans["gamma"]
	mu.Unlock()
	if a[0].IsZero() || g[0].IsZero() {
		t.Fatalf("spans %v", spans)
	}
	if a[0].Before(g[1]) && g[0].Before(a[1]) {
		t.Fatalf("same stage overlapped: alpha=%v gamma=%v", a, g)
	}
}

func TestSchedulerSameStageSkipRestore(t *testing.T) {
	machine.SetBootGuest(func(*guest.Guest, time.Duration) error { return nil })
	t.Cleanup(func() { machine.SetBootGuest(nil) })

	m, r := cliLiveStage(t)
	store := m.Store
	var (
		mu      sync.Mutex
		spans   = map[string][2]time.Time{}
		skipped bool
	)
	d := testSched(t, store)
	d.Launch = func(_ context.Context, spec jobSpec) (jobProc, error) {
		start := time.Now()
		switch spec.Step.Scene {
		case "make":
			if _, err := m.ReplaceSnapshot(context.Background(), r, "ready", machine.SnapshotOrigin{Project: "/p", Scene: "make"}, false); err != nil {
				return doneProc{err: err}, nil
			}
			loaded, err := store.Load("demo")
			if err != nil {
				return doneProc{err: err}, nil
			}
			r = loaded
		case "use":
			if _, err := m.Begin(context.Background(), r, "clean", "ready", "", "/p", true); err != nil {
				return doneProc{err: err}, nil
			}
			mu.Lock()
			skipped = m.StartTimes.RestoreSkipped != nil && *m.StartTimes.RestoreSkipped
			mu.Unlock()
		}
		end := time.Now()
		mu.Lock()
		spans[spec.Step.Scene] = [2]time.Time{start, end}
		mu.Unlock()
		return doneProc{}, nil
	}
	plan := &workspace.Plan{
		Steps: []workspace.PlanStep{
			{ID: "./make", Scene: "make", Path: filepath.Join(t.TempDir(), "make.json"), Stage: "demo", Reason: workspace.ReasonRequested, Requested: true},
			{ID: "./use", Scene: "use", Path: filepath.Join(t.TempDir(), "use.json"), Stage: "demo", Reason: workspace.ReasonDownstream},
		},
		Stages: []string{"demo"},
	}
	if err := d.schedule(plan, engine.Options{Record: true, Speed: 1}, "stale"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	makeSpan, useSpan := spans["make"], spans["use"]
	got := skipped
	mu.Unlock()
	if makeSpan[0].IsZero() || useSpan[0].IsZero() {
		t.Fatalf("spans %v", spans)
	}
	if useSpan[0].Before(makeSpan[1]) {
		t.Fatalf("consumer started before producer finished: make=%v use=%v", makeSpan, useSpan)
	}
	if !got {
		t.Fatal("consumer did not skip restore")
	}
}

func TestCatalogWaitDoesNotFailJob(t *testing.T) {
	store := cliStore(t)
	saveStageSpec(t, store, "demo", 2, 2<<30)
	hold, err := store.LockMany("image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	d := testSched(t, store)
	d.Launch = func(ctx context.Context, spec jobSpec) (jobProc, error) {
		p := &signalProc{done: make(chan error, 1)}
		go func() {
			writeProgress(spec.Progress, jobWaiting, phaseCatalog)
			close(started)
			rel, err := store.LockWait(ctx, "image-catalog")
			if err != nil {
				p.done <- err
				return
			}
			rel()
			p.done <- nil
		}()
		return p, nil
	}
	plan := oneVMPlan(t, "demo")
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.schedule(plan, engine.Options{Record: true, Speed: 1}, "with-deps")
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		hold()
		t.Fatal("job never waited")
	}
	select {
	case err := <-errCh:
		hold()
		t.Fatalf("job finished while catalog held: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	hold()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("job stuck")
	}
	out := d.Out.(*bytes.Buffer).String()
	if !strings.Contains(out, "image-catalog") {
		t.Fatalf("catalog wait missing from output:\n%s", out)
	}
}

func TestMemoryBudgetReadOnce(t *testing.T) {
	dir, store := cliTwoVM(t)
	rec, _ := store.Load("demo")
	rec.Spec.Memory = 6 << 30
	rec.Spec.CPUs = 2
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}
	lab, _ := store.Load("lab")
	lab.Spec.Memory = 6 << 30
	lab.Spec.CPUs = 2
	if err := store.Save(lab); err != nil {
		t.Fatal(err)
	}
	reads := 0
	spans := map[string][2]time.Time{}
	var mu sync.Mutex
	d := testSched(t, store)
	d.MemAvailable = func() (uint64, error) {
		reads++
		return 10 << 30, nil
	}
	d.NumCPU = func() int { return 16 }
	d.Launch = sleepLaunch(80*time.Millisecond, spans, &mu)
	if err := d.runStale(dir, engine.Options{Record: true, Speed: 1}); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("MemAvailable reads %d, want 1", reads)
	}
	mu.Lock()
	a, b := spans["alpha"], spans["beta"]
	mu.Unlock()
	if a[0].Before(b[1]) && b[0].Before(a[1]) {
		t.Fatalf("6+6 GiB must not overlap on 8 GiB budget: %v %v", a, b)
	}
}

func TestOversizedJobRunsAlone(t *testing.T) {
	dir, store := cliTwoVM(t)
	rec, _ := store.Load("demo")
	rec.Spec.Memory = 2 << 30
	rec.Spec.CPUs = 2
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}
	lab, _ := store.Load("lab")
	lab.Spec.Memory = 16 << 30
	lab.Spec.CPUs = 2
	if err := store.Save(lab); err != nil {
		t.Fatal(err)
	}
	spans := map[string][2]time.Time{}
	var mu sync.Mutex
	d := testSched(t, store)
	d.MemAvailable = func() (uint64, error) { return 10 << 30, nil }
	d.NumCPU = func() int { return 16 }
	d.Launch = sleepLaunch(80*time.Millisecond, spans, &mu)
	if err := d.runStale(dir, engine.Options{Record: true, Speed: 1}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	a, b := spans["alpha"], spans["beta"]
	mu.Unlock()
	if a[0].IsZero() || b[0].IsZero() {
		t.Fatalf("oversized must still run, not be refused: %v", spans)
	}
	if a[0].Before(b[1]) && b[0].Before(a[1]) {
		t.Fatalf("oversized overlapped another VM: alpha=%v beta=%v", a, b)
	}
}

func TestCatalogWaitHoldsMemoryBudget(t *testing.T) {
	dir, store := cliTwoVM(t)
	rec, _ := store.Load("demo")
	rec.Spec.Memory = 6 << 30
	rec.Spec.CPUs = 2
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}
	lab, _ := store.Load("lab")
	lab.Spec.Memory = 6 << 30
	lab.Spec.CPUs = 2
	if err := store.Save(lab); err != nil {
		t.Fatal(err)
	}
	spans := map[string][2]time.Time{}
	mem := map[string]uint64{"alpha": 6 << 30, "beta": 6 << 30}
	var mu sync.Mutex
	d := testSched(t, store)
	d.MemAvailable = func() (uint64, error) { return 10 << 30, nil }
	d.NumCPU = func() int { return 16 }
	d.Launch = func(ctx context.Context, spec jobSpec) (jobProc, error) {
		if spec.Step.Scene == "alpha" && spec.Progress != nil {
			writeProgress(spec.Progress, jobWaiting, phaseCatalog)
		}
		return sleepLaunch(80*time.Millisecond, spans, &mu)(ctx, spec)
	}
	if err := d.runStale(dir, engine.Options{Record: true, Speed: 1}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	a, b := spans["alpha"], spans["beta"]
	mu.Unlock()
	if a[0].IsZero() || b[0].IsZero() {
		t.Fatalf("spans %v", spans)
	}
	if a[0].Before(b[1]) && b[0].Before(a[1]) {
		t.Fatalf("catalog wait must keep the reservation: alpha=%v beta=%v", a, b)
	}
	if peak := peakConcurrentMem(spans, mem); peak > 8<<30 {
		t.Fatalf("active memory %d exceeds 8 GiB budget", peak)
	}
	if !strings.Contains(d.Out.(*bytes.Buffer).String(), "waiting image-catalog") {
		t.Fatalf("alpha never reported waiting:\n%s", d.Out.(*bytes.Buffer).String())
	}
}

func TestJobsOneMatchesA7Order(t *testing.T) {
	dir, store := cliTwoVM(t)
	var order []string
	var mu sync.Mutex
	d := testSched(t, store)
	d.Jobs = 1
	d.Launch = func(ctx context.Context, spec jobSpec) (jobProc, error) {
		p := &signalProc{done: make(chan error, 1)}
		go func() {
			mu.Lock()
			order = append(order, spec.Step.Scene)
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			p.done <- nil
		}()
		return p, nil
	}
	if err := d.runStale(dir, engine.Options{Record: true, Speed: 1}); err != nil {
		t.Fatal(err)
	}
	plan, err := workspace.PlanStale(workspace.DepsOptions{Options: workspace.Options{Dir: dir, Store: store}, Kind: workspace.KindPlay})
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, s := range plan.Steps {
		want = append(want, s.Scene)
	}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order %v, plan %v", order, want)
	}
}

func TestFirstFailureDoesNotAdmitIndependent(t *testing.T) {
	dir, store := cliTwoVM(t)
	var ran []string
	d := testSched(t, store)
	d.Jobs = 1
	d.Launch = func(ctx context.Context, spec jobSpec) (jobProc, error) {
		ran = append(ran, spec.Step.Scene)
		return doneProc{err: errors.New("boom")}, nil
	}
	err := d.runStale(dir, engine.Options{Record: true, Speed: 1})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err %v", err)
	}
	if len(ran) != 1 {
		t.Fatalf("admitted after failure: %v", ran)
	}
}

func TestFirstFailureDoesNotAdmitNew(t *testing.T) {
	dir, store := cliChainABC(t)
	var ran []string
	d := testSched(t, store)
	d.Jobs = 1
	d.Launch = func(ctx context.Context, spec jobSpec) (jobProc, error) {
		name := strings.TrimSuffix(filepath.Base(spec.Path), ".json")
		ran = append(ran, name)
		if name == "b" {
			return doneProc{err: errors.New("boom")}, nil
		}
		return doneProc{}, nil
	}
	err := d.run(filepath.Join(dir, "scenes", "c.json"), engine.Options{Record: true, Speed: 1})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err %v", err)
	}
	if ExitStatus(err) != 1 {
		t.Fatalf("exit %d", ExitStatus(err))
	}
	if strings.Join(ran, ",") != "a,b" {
		t.Fatalf("ran %v", ran)
	}
	text := d.Out.(*bytes.Buffer).String()
	if !strings.Contains(text, ">> not run ./c") || !strings.Contains(text, ">> failed ./b") {
		t.Fatalf("report: %s", text)
	}
}

func TestDoubleSIGINTStillExits130(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestMain")
	cmd.Env = append(os.Environ(), "BACKSTAGE_TEST_HELPER=double-int")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 130 {
		t.Fatalf("exit %v", err)
	}
}

func TestCtrlCBeforeFirstJobExits130(t *testing.T) {
	store := cliStore(t)
	saveStageSpec(t, store, "demo", 2, 2<<30)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := testSched(t, store)
	called := false
	d.Launch = func(context.Context, jobSpec) (jobProc, error) {
		called = true
		return doneProc{}, nil
	}
	err := d.schedule(oneVMPlan(t, "demo"), engine.Options{Context: ctx, Record: true, Speed: 1}, "with-deps")
	if ExitStatus(err) != 130 {
		t.Fatalf("exit %d %v", ExitStatus(err), err)
	}
	if called {
		t.Fatal("cancelled run started a job")
	}
	text := d.Out.(*bytes.Buffer).String()
	if strings.Count(text, ">> interrupted") != 1 || !strings.Contains(text, ">> not run") {
		t.Fatalf("report: %s", text)
	}
}

func TestChild130IsRunFailure(t *testing.T) {
	dir, store := cliChainABC(t)
	d := testSched(t, store)
	d.Jobs = 1
	var ran []string
	d.Launch = func(ctx context.Context, spec jobSpec) (jobProc, error) {
		ran = append(ran, spec.Step.Scene)
		cmd := exec.Command(os.Args[0], "-test.run=TestMain")
		cmd.Env = append(os.Environ(), "BACKSTAGE_TEST_HELPER=exit-130")
		null, err := os.Open(os.DevNull)
		if err != nil {
			return nil, err
		}
		cmd.Stdin = null
		if err := cmd.Start(); err != nil {
			_ = null.Close()
			return nil, err
		}
		return &execProc{cmd: cmd, null: null}, nil
	}
	err := d.run(filepath.Join(dir, "scenes", "c.json"), engine.Options{Record: true, Speed: 1})
	if ExitStatus(err) != 1 {
		t.Fatalf("exit %d %v", ExitStatus(err), err)
	}
	if strings.Join(ran, ",") != "a" {
		t.Fatalf("ran %v", ran)
	}
	text := d.Out.(*bytes.Buffer).String()
	if !strings.Contains(text, ">> interrupted ./a") || !strings.Contains(text, ">> not run ./b") {
		t.Fatalf("report: %s", text)
	}
}

func TestChild130ThenParentCancelExits130(t *testing.T) {
	store := cliStore(t)
	saveStageSpec(t, store, "demo", 2, 2<<30)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	d := testSched(t, store)
	d.Launch = func(context.Context, jobSpec) (jobProc, error) {
		cmd := exec.Command(os.Args[0], "-test.run=TestMain")
		cmd.Env = append(os.Environ(), "BACKSTAGE_TEST_HELPER=exit-130")
		null, err := os.Open(os.DevNull)
		if err != nil {
			return nil, err
		}
		cmd.Stdin = null
		if err := cmd.Start(); err != nil {
			_ = null.Close()
			return nil, err
		}
		close(started)
		return &execProc{cmd: cmd, null: null}, nil
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.schedule(oneVMPlan(t, "demo"), engine.Options{Context: ctx, Record: true, Speed: 1}, "with-deps")
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no start")
	}
	time.Sleep(100 * time.Millisecond)
	cancel()
	err := <-errCh
	if ExitStatus(err) != 130 {
		t.Fatalf("parent Ctrl-C during grace must be 130, got %d %v", ExitStatus(err), err)
	}
}

func TestChild130WithoutParentCancelIsFailure(t *testing.T) {
	store := cliStore(t)
	saveStageSpec(t, store, "demo", 2, 2<<30)
	d := testSched(t, store)
	d.Launch = func(context.Context, jobSpec) (jobProc, error) {
		cmd := exec.Command(os.Args[0], "-test.run=TestMain")
		cmd.Env = append(os.Environ(), "BACKSTAGE_TEST_HELPER=exit-130")
		null, err := os.Open(os.DevNull)
		if err != nil {
			return nil, err
		}
		cmd.Stdin = null
		if err := cmd.Start(); err != nil {
			_ = null.Close()
			return nil, err
		}
		return &execProc{cmd: cmd, null: null}, nil
	}
	start := time.Now()
	err := d.schedule(oneVMPlan(t, "demo"), engine.Options{Record: true, Speed: 1}, "with-deps")
	elapsed := time.Since(start)
	if ExitStatus(err) != 1 {
		t.Fatalf("solo child 130 must be run failure, got %d %v", ExitStatus(err), err)
	}
	if elapsed < 400*time.Millisecond {
		t.Fatalf("grace wait too short: %s", elapsed)
	}
	if elapsed > 800*time.Millisecond {
		t.Fatalf("grace wait too long: %s", elapsed)
	}
}

func TestParentCtrlCExits130(t *testing.T) {
	store := cliStore(t)
	saveStageSpec(t, store, "demo", 2, 2<<30)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	d := testSched(t, store)
	d.Launch = func(context.Context, jobSpec) (jobProc, error) {
		p := &signalProc{done: make(chan error, 1)}
		go func() {
			close(started)
			<-ctx.Done()
			p.done <- context.Canceled
		}()
		return p, nil
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.schedule(oneVMPlan(t, "demo"), engine.Options{Context: ctx, Record: true, Speed: 1}, "with-deps")
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no start")
	}
	cancel()
	err := <-errCh
	if ExitStatus(err) != 130 {
		t.Fatalf("parent Ctrl-C must be 130, got %d %v", ExitStatus(err), err)
	}
}

func TestCtrlCSignalsChildrenReportOnce(t *testing.T) {
	store := cliStore(t)
	saveStageSpec(t, store, "demo", 2, 2<<30)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	d := testSched(t, store)
	d.Launch = func(context.Context, jobSpec) (jobProc, error) {
		p := &signalProc{done: make(chan error, 1)}
		go func() {
			close(started)
			<-time.After(2 * time.Second)
			p.done <- context.Canceled
		}()
		return p, nil
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.schedule(oneVMPlan(t, "demo"), engine.Options{Context: ctx, Record: true, Speed: 1}, "with-deps")
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no start")
	}
	cancel()
	err := <-errCh
	if ExitStatus(err) != 130 {
		t.Fatalf("exit %d %v", ExitStatus(err), err)
	}
	text := d.Out.(*bytes.Buffer).String()
	if got := strings.Count(text, ">> interrupted"); got != 1 {
		t.Fatalf("report once, got %d: %s", got, text)
	}
}

func TestParentForwardsSIGINTToOtherPgid(t *testing.T) {
	store := cliStore(t)
	saveStageSpec(t, store, "demo", 2, 2<<30)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var child *exec.Cmd
	d := testSched(t, store)
	d.Launch = func(context.Context, jobSpec) (jobProc, error) {
		cmd := exec.Command(os.Args[0], "-test.run=TestMain")
		cmd.Env = append(os.Environ(), "BACKSTAGE_TEST_HELPER=wait-int")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		null, err := os.Open(os.DevNull)
		if err != nil {
			return nil, err
		}
		cmd.Stdin = null
		if err := cmd.Start(); err != nil {
			_ = null.Close()
			return nil, err
		}
		child = cmd
		close(started)
		return &execProc{cmd: cmd, null: null}, nil
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.schedule(oneVMPlan(t, "demo"), engine.Options{Context: ctx, Record: true, Speed: 1}, "with-deps")
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no start")
	}
	cancel()
	select {
	case err := <-errCh:
		if ExitStatus(err) != 130 {
			t.Fatalf("exit %d %v", ExitStatus(err), err)
		}
	case <-time.After(2 * time.Second):
		if child != nil && child.Process != nil {
			_ = child.Process.Kill()
		}
		t.Fatal("parent did not forward SIGINT")
	}
}

func TestInheritedLockBlocksOutsider(t *testing.T) {
	store := cliStore(t)
	saveStageSpec(t, store, "demo", 2, 2<<30)
	started := make(chan struct{})
	release := make(chan struct{})
	d := testSched(t, store)
	d.Lock = nil
	d.Launch = func(context.Context, jobSpec) (jobProc, error) {
		p := &signalProc{done: make(chan error, 1)}
		go func() {
			close(started)
			<-release
			p.done <- nil
		}()
		return p, nil
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.schedule(oneVMPlan(t, "demo"), engine.Options{Record: true, Speed: 1}, "with-deps")
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no start")
	}
	if _, err := store.LockMany("demo"); !errors.Is(err, machine.ErrBusy) {
		close(release)
		t.Fatalf("third process got the lock: %v", err)
	}
	close(release)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestInheritedFDKeepsLockAfterParentCloses(t *testing.T) {
	store := cliStore(t)
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	held, release, err := store.LockHold("demo")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMain")
	cmd.Env = append(os.Environ(), "BACKSTAGE_TEST_HELPER=hold-lock")
	cmd.ExtraFiles = childExtraFiles(jobSpec{Lock: held[0].File, Step: workspace.PlanStep{Stage: "demo"}})
	if err := cmd.Start(); err != nil {
		release()
		t.Fatal(err)
	}
	release()
	if _, err := store.LockMany("demo"); !errors.Is(err, machine.ErrBusy) {
		_ = cmd.Process.Kill()
		t.Fatalf("third process got the lock: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestGrandchildDoesNotKeepStageLock(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	store, err := machine.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	held, release, err := store.LockHold("demo")
	if err != nil {
		t.Fatal(err)
	}
	pidfile := filepath.Join(t.TempDir(), "pid")
	cmd := exec.Command(os.Args[0], "-test.run=TestMain")
	cmd.Env = append(os.Environ(), "BACKSTAGE_TEST_HELPER=spawn-sleep", "BACKSTAGE_TEST_PIDFILE="+pidfile)
	cmd.ExtraFiles = []*os.File{held[0].File}
	if err := cmd.Start(); err != nil {
		release()
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		release()
		t.Fatal(err)
	}
	release()
	var pid int
	for i := 0; i < 50; i++ {
		b, err := os.ReadFile(pidfile)
		if err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			if pid > 0 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("grandchild did not start")
	}
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()
	if _, err := store.LockMany("demo"); err != nil {
		t.Fatalf("third process did not get the lock: %v", err)
	}
}

func TestJSONProgressAndReport(t *testing.T) {
	dir, store := cliChainABC(t)
	d := testSched(t, store)
	d.JSON = true
	d.Jobs = 1
	d.Launch = func(context.Context, jobSpec) (jobProc, error) {
		return doneProc{}, nil
	}
	if err := d.run(filepath.Join(dir, "scenes", "c.json"), engine.Options{Record: true, Speed: 1}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(d.Out.(*bytes.Buffer).String()), "\n")
	if len(lines) < 4 {
		t.Fatalf("lines %v", lines)
	}
	var last jobReport
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("final %s: %v", lines[len(lines)-1], err)
	}
	if last.RunID == "" || len(last.Jobs) == 0 {
		t.Fatalf("report %+v", last)
	}
	var sawProgress bool
	for _, line := range lines[:len(lines)-1] {
		var p jobProgressLine
		if json.Unmarshal([]byte(line), &p) != nil {
			t.Fatalf("progress %s", line)
		}
		if p.Method != "job.progress" || p.JSONRPC != "2.0" {
			t.Fatalf("shape %s", line)
		}
		sawProgress = true
	}
	if !sawProgress {
		t.Fatal("no progress lines")
	}
}

func TestJSONIncludesPlanWarnings(t *testing.T) {
	dir, store := cliTwoVM(t)
	writeFile(t, filepath.Join(dir, "scenes", "orphan.json"), `{"name":"orphan","layout":"solo","vm":"laptop","vm-start":{"mode":"clean","snapshot":"ghost"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	d := testSched(t, store)
	d.JSON = true
	d.Launch = func(context.Context, jobSpec) (jobProc, error) {
		return doneProc{}, nil
	}
	if err := d.runStale(dir, engine.Options{Record: true, Speed: 1}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(d.Out.(*bytes.Buffer).String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("lines %v", lines)
	}
	var warnings []workspace.Warning
	for _, line := range lines[:len(lines)-1] {
		var n struct {
			JSONRPC string `json:"jsonrpc"`
			Method  string `json:"method"`
			Params  struct {
				Path  string `json:"path"`
				Error string `json:"error"`
			} `json:"params"`
		}
		if json.Unmarshal([]byte(line), &n) != nil {
			continue
		}
		if n.Method == "plan.warning" {
			if n.JSONRPC != "2.0" || n.Params.Error == "" {
				t.Fatalf("warning shape %s", line)
			}
			warnings = append(warnings, workspace.Warning{Path: n.Params.Path, Error: n.Params.Error})
		}
	}
	if len(warnings) == 0 {
		t.Fatalf("no plan.warning lines:\n%s", d.Out.(*bytes.Buffer).String())
	}
	var last jobReport
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("final %s: %v", lines[len(lines)-1], err)
	}
	if len(last.Warnings) == 0 {
		t.Fatalf("final report dropped warnings: %+v", last)
	}
	found := false
	for _, w := range last.Warnings {
		if strings.Contains(w.Error, "ghost") || strings.Contains(w.Path, "orphan") {
			found = true
		}
	}
	if !found {
		t.Fatalf("orphan warning missing from report: %+v", last.Warnings)
	}
}

func TestJobLogRetentionKeepsCurrent(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 25; i++ {
		name := time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC).Format("20060102T150405Z")
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	current := "20260102T000000Z"
	if err := os.MkdirAll(filepath.Join(root, current), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := pruneJobRuns(root, current, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, current)); err != nil {
		t.Fatal("current run was deleted")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	if n != 20 {
		t.Fatalf("kept %d, want 20", n)
	}
}

func TestJobLogRetentionKeepsLockedRun(t *testing.T) {
	root := t.TempDir()
	var names []string
	for i := 0; i < 25; i++ {
		name := time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC).Format("20060102T150405Z") + "-aaaaaa"
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	held, err := lockRunDir(filepath.Join(root, names[0]))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	current := names[24]
	if err := pruneJobRuns(root, current, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, names[0])); err != nil {
		t.Fatal("oldest live run.lock must be kept")
	}
	if _, err := os.Stat(filepath.Join(root, names[1])); err == nil {
		t.Fatal("unlocked old run should have been pruned")
	}
	if _, err := os.Stat(filepath.Join(root, current)); err != nil {
		t.Fatal("current run was deleted")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	if n != 21 {
		t.Fatalf("kept %d, want 21 (20 newest + oldest locked)", n)
	}
}

func TestJobLogRetentionKeepsExactly20(t *testing.T) {
	for _, total := range []int{20, 21} {
		root := t.TempDir()
		var names []string
		for i := 0; i < total; i++ {
			name := time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC).Format("20060102T150405Z") + "-aaaaaa"
			if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
				t.Fatal(err)
			}
			names = append(names, name)
		}
		current := names[len(names)-1]
		if err := pruneJobRuns(root, current, 20); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range entries {
			if e.IsDir() {
				n++
			}
		}
		if n != 20 {
			t.Fatalf("total %d: kept %d, want exactly 20", total, n)
		}
		if _, err := os.Stat(filepath.Join(root, current)); err != nil {
			t.Fatalf("total %d: current deleted", total)
		}
		if total == 21 {
			if _, err := os.Stat(filepath.Join(root, names[0])); err == nil {
				t.Fatal("21 runs must drop the oldest unlocked")
			}
		}
	}
}

func TestTwoRunsSameClockGetDifferentIDs(t *testing.T) {
	store := cliStore(t)
	saveStageSpec(t, store, "demo", 2, 2<<30)
	fixed := time.Date(2026, 9, 14, 15, 4, 5, 0, time.UTC)
	jobsDir := filepath.Join(t.TempDir(), "jobs")
	runOnce := func() {
		d := testSched(t, store)
		d.JobsDir = jobsDir
		d.now = func() time.Time { return fixed }
		d.Launch = func(context.Context, jobSpec) (jobProc, error) {
			return doneProc{}, nil
		}
		if err := d.schedule(oneVMPlan(t, "demo"), engine.Options{Record: true, Speed: 1}, "with-deps"); err != nil {
			t.Fatal(err)
		}
	}
	runOnce()
	runOnce()
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		t.Fatal(err)
	}
	var runs []string
	for _, e := range entries {
		if e.IsDir() {
			runs = append(runs, e.Name())
		}
	}
	if len(runs) != 2 {
		t.Fatalf("runs %v", runs)
	}
	if runs[0] == runs[1] {
		t.Fatal("same run-id")
	}
	for _, name := range runs {
		if !strings.HasPrefix(name, "20260914T150405Z-") || len(name) != len("20260914T150405Z-")+6 {
			t.Fatalf("run-id %s", name)
		}
	}
}

func TestCreateRunDirRetriesExistingSuffix(t *testing.T) {
	d := testSched(t, cliStore(t))
	d.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	n := 0
	d.runSuffix = func() (string, error) {
		n++
		if n == 1 {
			return "aaaaaa", nil
		}
		return "bbbbbb", nil
	}
	if err := os.MkdirAll(filepath.Join(d.JobsDir, "20260101T000000Z-aaaaaa"), 0o700); err != nil {
		t.Fatal(err)
	}
	id, dir, lock, err := d.createRunDir()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if id != "20260101T000000Z-bbbbbb" {
		t.Fatalf("id %s", id)
	}
	if _, err := os.Stat(filepath.Join(dir, "run.lock")); err != nil {
		t.Fatal(err)
	}
}

func TestStaleRecordsDespiteUnrelatedBlock(t *testing.T) {
	dir, store := cliTwoVM(t)
	writeFile(t, filepath.Join(dir, "scenes", "orphan.json"), `{"name":"orphan","layout":"solo","vm":"laptop","vm-start":{"mode":"clean","snapshot":"ghost"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	var ran []string
	d := testSched(t, store)
	d.Launch = func(ctx context.Context, spec jobSpec) (jobProc, error) {
		ran = append(ran, spec.Step.Scene)
		return doneProc{}, nil
	}
	if err := d.runStale(dir, engine.Options{Record: true, Speed: 1}); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(ran, ",")
	if got != "alpha,beta" && got != "beta,alpha" {
		t.Fatalf("ran %v", ran)
	}
	if !strings.Contains(d.Out.(*bytes.Buffer).String(), "warning:") {
		t.Fatalf("missing warning:\n%s", d.Out.(*bytes.Buffer).String())
	}
}

func TestStaleUsesA7Refusals(t *testing.T) {
	dir, store := cliChainABC(t)
	saveCLIChain(t, store, dir, true, false)
	rec, _ := store.Load("demo")
	delete(rec.SnapshotOrigins, "ready")
	if err := store.Save(rec); err != nil {
		t.Fatal(err)
	}
	d := testSched(t, store)
	called := false
	d.Launch = func(context.Context, jobSpec) (jobProc, error) {
		called = true
		return doneProc{}, nil
	}
	err := d.runStale(dir, engine.Options{Record: true, Speed: 1})
	if err == nil || !strings.Contains(err.Error(), "no origin") && !strings.Contains(err.Error(), "--adopt") {
		t.Fatalf("stale must refuse: %v", err)
	}
	if called {
		t.Fatal("refused plan started a job")
	}
}

func TestRefusalAndSuccessExitCodes(t *testing.T) {
	if ExitStatus(nil) != 0 {
		t.Fatal("ok")
	}
	if ExitStatus(errors.New("boom")) != 1 {
		t.Fatal("failed")
	}
	if ExitStatus(interrupted(context.Canceled)) != 130 {
		t.Fatal("interrupt")
	}
}

func TestPlayAndRehearseStaleJobsJSONFlags(t *testing.T) {
	if playCmd().Flags().Lookup("stale") == nil || playCmd().Flags().Lookup("jobs") == nil || playCmd().Flags().Lookup("json") == nil {
		t.Fatal("play flags")
	}
	if rehearseCmd().Flags().Lookup("stale") == nil {
		t.Fatal("rehearse --stale")
	}
	if !playCmd().Flags().Lookup("internal-reserved-stage").Hidden {
		t.Fatal("reserved flag must be hidden")
	}
}

func TestJobsJSONRequireScheduler(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	scene := filepath.Join(t.TempDir(), "scene.json")
	writeFile(t, scene, `{"name":"solo","layout":"solo","steps":[{"action":"wait","delay-after":0.05}]}`)
	want := "--jobs and --json require --with-deps or --stale"
	cases := []struct {
		name string
		cmd  func() *cobra.Command
		args []string
	}{
		{"play-json", playCmd, []string{"--json", scene}},
		{"play-jobs", playCmd, []string{"--jobs", "2", scene}},
		{"rehearse-json", rehearseCmd, []string{"--json", scene}},
		{"rehearse-jobs", rehearseCmd, []string{"--jobs", "2", scene}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.cmd()
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil || err.Error() != want {
				t.Fatalf("got %v", err)
			}
			jobs := filepath.Join(os.Getenv("XDG_STATE_HOME"), "backstage", "jobs")
			if _, err := os.Stat(jobs); err == nil {
				t.Fatal("created a job run before refusing")
			}
		})
	}
}

func TestStaleAdoptStaysOnSeeded(t *testing.T) {
	dir, store := cliChainABC(t)
	saveCLIChain(t, store, dir, true, true)
	publishCLI(t, dir, store, "a", facts.Facts{InputsSHA256: "not-the-digest"})
	publishCLI(t, dir, store, "b", facts.Facts{})
	publishCLI(t, dir, store, "c", facts.Facts{})
	adopt := map[string]bool{}
	d := testSched(t, store)
	d.Launch = func(_ context.Context, spec jobSpec) (jobProc, error) {
		adopt[spec.Step.Scene] = spec.Opts.Adopt
		return doneProc{}, nil
	}
	if err := d.runStale(dir, engine.Options{
		Record: true, Speed: 1, Adopt: true,
		ConfirmAdopt: func(string) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if len(adopt) != 3 || !adopt["a"] || adopt["b"] || adopt["c"] {
		t.Fatalf("adopt must stay on the seed: %v", adopt)
	}
}

func cliLiveStage(t *testing.T) (*machine.Manager, *machine.Record) {
	t.Helper()
	store := &machine.Store{
		Root:    filepath.Join(t.TempDir(), "reg"),
		Cache:   filepath.Join(t.TempDir(), "cache"),
		Storage: filepath.Join(t.TempDir(), "storage"),
	}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.Storage, 0o700); err != nil {
		t.Fatal(err)
	}
	m := &machine.Manager{Store: store, URI: "qemu:///system", Timeout: time.Second, Output: io.Discard}
	r := &machine.Record{
		Schema:    machine.Schema,
		ID:        strings.Repeat("ab", 16),
		Name:      "demo",
		Domain:    "backstage-test-demo",
		URI:       m.URI,
		Spec:      machine.DefaultSpec(),
		Status:    "ready",
		Video:     "bochs",
		Firmware:  "firmware",
		Snapshots: map[string]string{},
	}
	r.Disk = filepath.Join(store.Storage, r.ID+".qcow2")
	r.NVRAM = filepath.Join(store.Storage, r.ID+".fd")
	for _, p := range []string{r.Disk, r.NVRAM} {
		if err := os.WriteFile(p, []byte("disk"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	key := filepath.Join(store.Dir(r.Name), "id_ed25519")
	if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key+".pub", []byte("pub"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCredentials(r.Name, machine.Credentials{Password: "pw", Key: key}); err != nil {
		t.Fatal(err)
	}
	m.Runner = cliRunner(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			if len(args) > 0 && args[0] == "info" {
				return `[{"filename":"` + r.Disk + `"}]`, nil
			}
			return "", os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
		}
		if len(args) > 2 {
			switch args[2] {
			case "domuuid":
				return r.ID[:8] + "-" + r.ID[8:12] + "-" + r.ID[12:16] + "-" + r.ID[16:20] + "-" + r.ID[20:], nil
			case "domstate":
				return "shut off", nil
			}
		}
		return "", nil
	})
	return m, r
}

func testSched(t *testing.T, store *machine.Store) *depsExec {
	t.Helper()
	return &depsExec{
		Store:   store,
		Out:     &bytes.Buffer{},
		JobsDir: filepath.Join(t.TempDir(), "jobs"),
		NumCPU:  func() int { return 16 },
		MemAvailable: func() (uint64, error) {
			return 64 << 30, nil
		},
	}
}

func peakConcurrentMem(spans map[string][2]time.Time, mem map[string]uint64) uint64 {
	type ev struct {
		t   time.Time
		d   int64
		mem uint64
	}
	var evs []ev
	for name, span := range spans {
		if span[0].IsZero() || span[1].IsZero() {
			continue
		}
		evs = append(evs, ev{t: span[0], d: 1, mem: mem[name]})
		evs = append(evs, ev{t: span[1], d: -1, mem: mem[name]})
	}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].t.Equal(evs[j].t) {
			return evs[i].d < evs[j].d
		}
		return evs[i].t.Before(evs[j].t)
	})
	var cur, peak uint64
	for _, e := range evs {
		if e.d > 0 {
			cur += e.mem
			if cur > peak {
				peak = cur
			}
		} else if cur >= e.mem {
			cur -= e.mem
		}
	}
	return peak
}

func sleepLaunch(d time.Duration, spans map[string][2]time.Time, mu *sync.Mutex) jobLauncher {
	return func(ctx context.Context, spec jobSpec) (jobProc, error) {
		p := &signalProc{done: make(chan error, 1)}
		go func() {
			start := time.Now()
			select {
			case <-time.After(d):
			case <-ctx.Done():
			}
			end := time.Now()
			mu.Lock()
			spans[spec.Step.Scene] = [2]time.Time{start, end}
			mu.Unlock()
			p.done <- nil
		}()
		return p, nil
	}
}

func oneVMPlan(t *testing.T, stage string) *workspace.Plan {
	t.Helper()
	return &workspace.Plan{
		Steps: []workspace.PlanStep{{
			ID: "./make", Scene: "make", Path: filepath.Join(t.TempDir(), "make.json"),
			Stage: stage, Reason: workspace.ReasonRequested, Requested: true,
		}},
		Stages: []string{stage},
	}
}

func cliTwoVM(t *testing.T) (string, *machine.Store) {
	t.Helper()
	dir := t.TempDir()
	store := cliStore(t)
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {"laptop": {"stage": "demo"}, "lab": {"stage": "lab"}}
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "alpha.json"), `{"name":"alpha","layout":"solo","vm":"laptop","vm-start":{"mode":"clean"},"vm-end":{"snapshot":"ready"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeFile(t, filepath.Join(dir, "scenes", "beta.json"), `{"name":"beta","layout":"solo","vm":"lab","vm-start":{"mode":"clean"},"vm-end":{"snapshot":"ready"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	saveStageSpec(t, store, "demo", 2, 2<<30)
	saveStageSpec(t, store, "lab", 2, 2<<30)
	return dir, store
}

func cliHostAndVM(t *testing.T) (string, *machine.Store) {
	t.Helper()
	dir := t.TempDir()
	store := cliStore(t)
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {"laptop": {"stage": "demo"}}
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "host.json"), `{"name":"host","layout":"solo","steps":[{"action":"wait","delay-after":0.05}]}`)
	writeFile(t, filepath.Join(dir, "scenes", "guest.json"), `{"name":"guest","layout":"solo","vm":"laptop","vm-start":{"mode":"clean"},"vm-end":{"snapshot":"ready"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	saveStageSpec(t, store, "demo", 2, 2<<30)
	return dir, store
}

func saveStageSpec(t *testing.T, store *machine.Store, name string, cpus int, mem uint64) {
	t.Helper()
	spec := machine.DefaultSpec()
	spec.CPUs = cpus
	spec.Memory = mem
	r := &machine.Record{
		Schema: machine.Schema, ID: strings.Repeat("ab", 16), Name: name,
		Domain: "backstage-test-" + name, URI: "qemu:///system",
		Spec: spec, Status: "ready",
		Snapshots: map[string]string{"initial": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}
	if name != "demo" {
		r.ID = strings.Repeat("cd", 16)
	}
	if err := store.Save(r); err != nil {
		t.Fatal(err)
	}
}
