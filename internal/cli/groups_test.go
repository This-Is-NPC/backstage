package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/This-Is-NPC/backstage/internal/engine"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/workspace"
)

func writeGroupCLIProject(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "scenes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "backstage.json"), []byte(`{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {
			"laptop": {"stage": "house-laptop"},
			"server": {"stage": "house-server"}
		},
		"state-groups": {"household": ["laptop", "server"]}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scenes", "use.json"), []byte(`{
		"name":"use","layout":"solo","vm":"laptop",
		"vm-start":{"mode":"clean","snapshot":"linked","group":"household"},
		"steps":[{"action":"wait"}]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReserveSceneStagesLocksEveryMember(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	store, err := machine.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	_, release, err := store.LockHold("house-server")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	dir := t.TempDir()
	writeGroupCLIProject(t, dir)
	p, err := scene.LoadProject(filepath.Join(dir, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := scene.LoadScene(filepath.Join(dir, "scenes", "use.json"))
	if err != nil {
		t.Fatal(err)
	}
	opts := engine.Options{}
	_, err = reserveSceneStages(p, s, &opts)
	if !errors.Is(err, machine.ErrBusy) {
		t.Fatalf("silent member must be in LockMany: %v", err)
	}
}

func TestReservedChildArgsPassMemberFDsAndGeneration(t *testing.T) {
	dir := t.TempDir()
	writeGroupCLIProject(t, dir)
	a, err := os.CreateTemp(dir, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := os.CreateTemp(dir, "server")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	spec := jobSpec{
		Path: filepath.Join(dir, "scenes", "use.json"),
		Step: workspace.PlanStep{
			Stage: "house-laptop",
			Path:  filepath.Join(dir, "scenes", "use.json"),
			Group: "household",
			Lanes: []string{"house-laptop", "house-server"},
		},
		Opts: engine.Options{StateGeneration: "shared-gen"},
		Locks: map[string]*os.File{
			"house-laptop": a,
			"house-server": b,
		},
	}
	args := reservedChildArgs(spec)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "house-laptop") || !strings.Contains(joined, "house-server") {
		t.Fatalf("member stages: %v", args)
	}
	if !strings.Contains(joined, "--internal-state-generation shared-gen") && !containsPair(args, "--internal-state-generation", "shared-gen") {
		t.Fatalf("generation: %v", args)
	}
	if lockExtraCount(spec) != 2 {
		t.Fatalf("fds %d", lockExtraCount(spec))
	}
}

func TestChildLocksIncludeGroupMembers(t *testing.T) {
	dir := t.TempDir()
	writeGroupCLIProject(t, dir)
	a, err := os.CreateTemp(dir, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := os.CreateTemp(dir, "server")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	got := childLocks(workspace.PlanStep{
		Stage: "house-laptop",
		Path:  filepath.Join(dir, "scenes", "use.json"),
		Group: "household",
		Lanes: []string{"house-laptop", "house-server"},
	}, map[string]*os.File{
		"house-laptop": a,
		"house-server": b,
	})
	if got["house-laptop"] == nil || got["house-server"] == nil {
		t.Fatalf("locks: %v", got)
	}
}

func TestApplyInternalChildSetsGeneration(t *testing.T) {
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
	defer release()
	opts := engine.Options{}
	gen := strings.Repeat("ab", 16)
	if err := applyInternalChild(&opts, []string{"demo"}, []int{int(held[0].File.Fd())}, -1, false, gen); err != nil {
		t.Fatal(err)
	}
	if opts.StateGeneration != gen {
		t.Fatalf("generation: %s", opts.StateGeneration)
	}
}

func TestApplyInternalChildRejectsGenerationAlone(t *testing.T) {
	opts := engine.Options{}
	err := applyInternalChild(&opts, nil, nil, -1, false, strings.Repeat("ab", 16))
	if err == nil || !strings.Contains(err.Error(), "internal-reserved") {
		t.Fatalf("flag alone: %v", err)
	}
	if opts.StateGeneration != "" {
		t.Fatalf("stamped without lock: %s", opts.StateGeneration)
	}
}

func TestApplyInternalChildRejectsMalformedGeneration(t *testing.T) {
	opts := engine.Options{}
	for _, id := range []string{"from-parent", strings.Repeat("AB", 16), strings.Repeat("ab", 15), strings.Repeat("ab", 16) + "0"} {
		if err := applyInternalChild(&opts, nil, nil, -1, false, id); err == nil || !strings.Contains(err.Error(), "32 lowercase hex") {
			t.Fatalf("malformed %q: %v", id, err)
		}
		if opts.StateGeneration != "" {
			t.Fatalf("stamped malformed %q", id)
		}
	}
}

func TestPlayStateGenerationFlag(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	store, err := machine.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeGroupCLIProject(t, dir)
	use := filepath.Join(dir, "scenes", "use.json")
	gen := strings.Repeat("cd", 16)

	cmd := playCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--internal-state-generation", gen, "missing.json"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "internal-reserved") {
		t.Fatalf("flag alone: %v", err)
	}

	cmd = playCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--internal-state-generation", "from-parent", "missing.json"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "32 lowercase hex") {
		t.Fatalf("malformed: %v", err)
	}

	server, release, err := store.LockHold("house-server")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cmd = playCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{
		"--internal-state-generation", gen,
		"--internal-reserved-stage", "house-server",
		"--internal-reserved-fd", strconv.Itoa(int(server[0].File.Fd())),
		use,
	})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "house-laptop") {
		t.Fatalf("other stage fd: %v", err)
	}

	laptop, lapRel, err := store.LockHold("house-laptop")
	if err != nil {
		t.Fatal(err)
	}
	defer lapRel()
	cmd = playCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{
		"--internal-state-generation", gen,
		"--internal-reserved-stage", "house-laptop",
		"--internal-reserved-fd", strconv.Itoa(int(laptop[0].File.Fd())),
		"missing.json",
	})
	err = cmd.Execute()
	if err == nil {
		t.Fatal("missing scene succeeded")
	}
	if strings.Contains(err.Error(), "internal-state-generation") || strings.Contains(err.Error(), "32 lowercase") {
		t.Fatalf("valid fd should pass generation check: %v", err)
	}
}

func TestSchedulerRefusesInternalChildFlags(t *testing.T) {
	gen := strings.Repeat("cd", 16)
	want := "cannot be used with --with-deps or --stale"
	cases := []struct {
		name string
		cmd  func() *cobra.Command
		args []string
	}{
		{"play-with-deps-generation", playCmd, []string{"--with-deps", "--internal-state-generation", gen, "missing.json"}},
		{"play-stale-generation", playCmd, []string{"--stale", "--internal-state-generation", gen}},
		{"play-with-deps-reserved-stage", playCmd, []string{"--with-deps", "--internal-reserved-stage", "house-server", "missing.json"}},
		{"play-stale-reserved-fd", playCmd, []string{"--stale", "--internal-reserved-fd", "3"}},
		{"rehearse-with-deps-generation", rehearseCmd, []string{"--with-deps", "--internal-state-generation", gen, "missing.json"}},
		{"rehearse-stale-reserved", rehearseCmd, []string{"--stale", "--internal-reserved-stage", "demo", "--internal-reserved-fd", "3"}},
		{"play-with-deps-adopt-confirmed", playCmd, []string{"--with-deps", "--internal-adopt-confirmed", "missing.json"}},
		{"play-stale-progress-fd", playCmd, []string{"--stale", "--internal-progress-fd", "3"}},
		{"rehearse-with-deps-progress-fd", rehearseCmd, []string{"--with-deps", "--internal-progress-fd", "4", "missing.json"}},
		{"rehearse-stale-adopt-confirmed", rehearseCmd, []string{"--stale", "--internal-adopt-confirmed=true"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.cmd()
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("child flags on scheduler: %v", err)
			}
			if strings.Contains(err.Error(), "internal-reserved") && strings.Contains(err.Error(), "requires") {
				t.Fatalf("applied child flags before refuse: %v", err)
			}
		})
	}
}

func TestPlayRefusesInternalAdoptWithoutReservedFD(t *testing.T) {
	cmd := playCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--internal-adopt-confirmed", "missing.json"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "internal-reserved-stage") || !strings.Contains(err.Error(), "internal-reserved-fd") {
		t.Fatalf("adopt-confirmed without fd: %v", err)
	}
	if strings.Contains(err.Error(), "cannot be used with --with-deps") {
		t.Fatalf("simple play used scheduler refuse: %v", err)
	}
}

func TestPlayStateGenerationRejectsOtherMemberStage(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	store, err := machine.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeGroupCLIProject(t, dir)
	makeLaptop := filepath.Join(dir, "scenes", "make-laptop.json")
	writeFile(t, makeLaptop, `{
		"name":"make-laptop","layout":"solo","vm":"laptop",
		"vm-start":{"mode":"clean"},"vm-end":{"snapshot":"linked","group":"household"},
		"steps":[{"action":"wait"}]
	}`)
	server, release, err := store.LockHold("house-server")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cmd := playCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"--internal-state-generation", strings.Repeat("cd", 16),
		"--internal-reserved-stage", "house-server",
		"--internal-reserved-fd", strconv.Itoa(int(server[0].File.Fd())),
		makeLaptop,
	})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "reserved lock on house-laptop") {
		t.Fatalf("other member stage fd: %v", err)
	}
}

func TestGroupConsumerDoesNotOverlapSilentStage(t *testing.T) {
	dir := t.TempDir()
	writeGroupCLIProject(t, dir)
	writeFile(t, filepath.Join(dir, "scenes", "make-laptop.json"), `{
		"name":"make-laptop","layout":"solo","vm":"laptop",
		"vm-start":{"mode":"clean"},"vm-end":{"snapshot":"linked","group":"household"},
		"steps":[{"action":"wait","delay-after":0.05}]
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "make-server.json"), `{
		"name":"make-server","layout":"solo","vm":"server",
		"vm-start":{"mode":"clean"},"vm-end":{"snapshot":"linked","group":"household"},
		"steps":[{"action":"wait","delay-after":0.05}]
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "other.json"), `{
		"name":"other","layout":"solo","vm":"server",
		"vm-start":{"mode":"clean"},"vm-end":{"snapshot":"unrelated"},
		"steps":[{"action":"wait","delay-after":0.05}]
	}`)
	store := cliStore(t)
	saveStageSpec(t, store, "house-laptop", 2, 2<<30)
	saveStageSpec(t, store, "house-server", 2, 2<<30)
	spans := map[string][2]time.Time{}
	var mu sync.Mutex
	active := map[string]string{}
	var shared []string
	d := testSched(t, store)
	d.Launch = func(ctx context.Context, spec jobSpec) (jobProc, error) {
		mu.Lock()
		for name := range spec.Locks {
			if other := active[name]; other != "" && other != spec.Step.Scene {
				shared = append(shared, name+":"+other+"+"+spec.Step.Scene)
			}
			active[name] = spec.Step.Scene
		}
		mu.Unlock()
		p, err := sleepLaunch(80*time.Millisecond, spans, &mu)(ctx, spec)
		if err != nil {
			return nil, err
		}
		return &unlockProc{jobProc: p, unlock: func() {
			mu.Lock()
			for name := range spec.Locks {
				if active[name] == spec.Step.Scene {
					delete(active, name)
				}
			}
			mu.Unlock()
		}}, nil
	}
	usePath := filepath.Join(dir, "scenes", "use.json")
	plan := &workspace.Plan{
		Steps: []workspace.PlanStep{
			{ID: "./make-laptop", Scene: "make-laptop", Path: filepath.Join(dir, "scenes", "make-laptop.json"), Stage: "house-laptop", Reason: workspace.ReasonRequested, Requested: true},
			{ID: "./make-server", Scene: "make-server", Path: filepath.Join(dir, "scenes", "make-server.json"), Stage: "house-server", Reason: workspace.ReasonGroupSibling},
			{ID: "./use", Scene: "use", Path: usePath, Stage: "house-laptop", Reason: workspace.ReasonRequested, Requested: true, Group: "household", Lanes: []string{"house-laptop", "house-server"}, Needs: []string{"./make-laptop", "./make-server"}},
			{ID: "./other", Scene: "other", Path: filepath.Join(dir, "scenes", "other.json"), Stage: "house-server", Reason: workspace.ReasonRequested, Requested: true},
		},
		Stages: []string{"house-laptop", "house-server"},
	}
	if err := d.schedule(plan, engine.Options{Record: true, Speed: 1}, "stale"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	use, other := spans["use"], spans["other"]
	mu.Unlock()
	if use[0].IsZero() || other[0].IsZero() {
		t.Fatalf("spans %v", spans)
	}
	if use[0].Before(other[1]) && other[0].Before(use[1]) {
		t.Fatalf("group consumer overlapped silent stage: use=%v other=%v", use, other)
	}
	if len(shared) > 0 {
		t.Fatalf("stage fd shared by two active jobs: %v", shared)
	}
}

func TestGroupConsumerWaitsForRunningSilentStage(t *testing.T) {
	dir := t.TempDir()
	writeGroupCLIProject(t, dir)
	writeFile(t, filepath.Join(dir, "scenes", "other.json"), `{
		"name":"other","layout":"solo","vm":"server",
		"vm-start":{"mode":"clean"},"vm-end":{"snapshot":"unrelated"},
		"steps":[{"action":"wait","delay-after":0.05}]
	}`)
	store := cliStore(t)
	saveStageSpec(t, store, "house-laptop", 2, 2<<30)
	saveStageSpec(t, store, "house-server", 2, 2<<30)
	spans := map[string][2]time.Time{}
	var mu sync.Mutex
	d := testSched(t, store)
	d.Launch = sleepLaunch(80*time.Millisecond, spans, &mu)
	usePath := filepath.Join(dir, "scenes", "use.json")
	plan := &workspace.Plan{
		Steps: []workspace.PlanStep{
			{ID: "./other", Scene: "other", Path: filepath.Join(dir, "scenes", "other.json"), Stage: "house-server", Reason: workspace.ReasonRequested, Requested: true},
			{ID: "./use", Scene: "use", Path: usePath, Stage: "house-laptop", Reason: workspace.ReasonRequested, Requested: true, Group: "household", Lanes: []string{"house-laptop", "house-server"}},
		},
		Stages: []string{"house-laptop", "house-server"},
	}
	if err := d.schedule(plan, engine.Options{Record: true, Speed: 1}, "stale"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	use, other := spans["use"], spans["other"]
	mu.Unlock()
	if use[0].IsZero() || other[0].IsZero() {
		t.Fatalf("spans %v", spans)
	}
	if !other[0].Before(use[0]) {
		t.Fatalf("silent-stage job must start first: other=%v use=%v", other, use)
	}
	if !use[0].After(other[1]) && !use[0].Equal(other[1]) {
		t.Fatalf("consumer admitted while silent stage was running: other=%v use=%v", other, use)
	}
}

func TestGroupSiblingRunSharesOneGeneration(t *testing.T) {
	dir := t.TempDir()
	writeGroupCLIProject(t, dir)
	writeFile(t, filepath.Join(dir, "scenes", "make-laptop.json"), `{
		"name":"make-laptop","layout":"solo","vm":"laptop",
		"vm-start":{"mode":"clean"},"vm-end":{"snapshot":"linked","group":"household"},
		"steps":[{"action":"wait","delay-after":0.05}]
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "make-server.json"), `{
		"name":"make-server","layout":"solo","vm":"server",
		"vm-start":{"mode":"clean"},"vm-end":{"snapshot":"linked","group":"household"},
		"steps":[{"action":"wait","delay-after":0.05}]
	}`)
	store := cliStore(t)
	saveStageSpec(t, store, "house-laptop", 2, 2<<30)
	saveStageSpec(t, store, "house-server", 2, 2<<30)
	var gens []string
	var mu sync.Mutex
	spans := map[string][2]time.Time{}
	d := testSched(t, store)
	d.Launch = func(ctx context.Context, spec jobSpec) (jobProc, error) {
		mu.Lock()
		gens = append(gens, spec.Step.Scene+"="+spec.Opts.StateGeneration)
		mu.Unlock()
		return sleepLaunch(20*time.Millisecond, spans, &mu)(ctx, spec)
	}
	usePath := filepath.Join(dir, "scenes", "use.json")
	plan := &workspace.Plan{
		Steps: []workspace.PlanStep{
			{ID: "./make-laptop", Scene: "make-laptop", Path: filepath.Join(dir, "scenes", "make-laptop.json"), Stage: "house-laptop", Reason: workspace.StaleInputs, Requested: true},
			{ID: "./make-server", Scene: "make-server", Path: filepath.Join(dir, "scenes", "make-server.json"), Stage: "house-server", Reason: workspace.ReasonGroupSibling},
			{ID: "./use", Scene: "use", Path: usePath, Stage: "house-laptop", Reason: workspace.ReasonRequested, Requested: true, Group: "household", Lanes: []string{"house-laptop", "house-server"}, Needs: []string{"./make-laptop", "./make-server"}},
		},
		Stages: []string{"house-laptop", "house-server"},
	}
	if err := d.schedule(plan, engine.Options{Record: true, Speed: 1}, "with-deps"); err != nil {
		t.Fatal(err)
	}
	if len(gens) != 3 {
		t.Fatalf("jobs: %v", gens)
	}
	id := strings.Split(gens[0], "=")[1]
	if !engine.ValidStateGeneration(id) {
		t.Fatalf("id: %s", id)
	}
	for _, g := range gens[1:] {
		if !strings.HasSuffix(g, "="+id) {
			t.Fatalf("mixed generations: %v", gens)
		}
	}
}

func TestPlanLanesSurviveCorruptScene(t *testing.T) {
	dir := t.TempDir()
	writeGroupCLIProject(t, dir)
	writeFile(t, filepath.Join(dir, "scenes", "make-laptop.json"), `{
		"name":"make-laptop","layout":"solo","vm":"laptop",
		"vm-start":{"mode":"clean"},"vm-end":{"snapshot":"linked","group":"household"},
		"steps":[{"action":"wait","delay-after":0.05}]
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "make-server.json"), `{
		"name":"make-server","layout":"solo","vm":"server",
		"vm-start":{"mode":"clean"},"vm-end":{"snapshot":"linked","group":"household"},
		"steps":[{"action":"wait","delay-after":0.05}]
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "other.json"), `{
		"name":"other","layout":"solo","vm":"server",
		"vm-start":{"mode":"clean"},"vm-end":{"snapshot":"unrelated"},
		"steps":[{"action":"wait","delay-after":0.05}]
	}`)
	store := cliStore(t)
	saveStageSpec(t, store, "house-laptop", 2, 2<<30)
	saveStageSpec(t, store, "house-server", 2, 2<<30)
	usePath := filepath.Join(dir, "scenes", "use.json")
	plan, err := workspace.PlanDeps(workspace.DepsOptions{
		Options:   workspace.Options{Dir: dir, Store: store},
		ScenePath: usePath,
		Kind:      workspace.KindPlay,
	})
	if err != nil {
		t.Fatal(err)
	}
	var use workspace.PlanStep
	for _, s := range plan.Steps {
		if s.Scene == "use" {
			use = s
		}
	}
	if use.Group != "household" || len(use.Lanes) < 2 {
		t.Fatalf("planned lanes: %+v", use)
	}
	if err := os.WriteFile(usePath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := jobLanes(use)
	if !containsStr(got, "house-laptop") || !containsStr(got, "house-server") {
		t.Fatalf("corrupt scene reduced lanes: %v", got)
	}
	if err := os.Remove(usePath); err != nil {
		t.Fatal(err)
	}
	if lanes := jobLanes(use); !containsStr(lanes, "house-server") {
		t.Fatalf("deleted scene reduced lanes: %v", lanes)
	}
	other := workspace.PlanStep{
		ID: "./other", Scene: "other", Path: filepath.Join(dir, "scenes", "other.json"),
		Stage: "house-server", Reason: workspace.ReasonRequested, Requested: true,
	}
	plan.Steps = append(plan.Steps, other)
	if !containsStr(plan.Stages, "house-server") {
		plan.Stages = append(plan.Stages, "house-server")
	}
	spans := map[string][2]time.Time{}
	var mu sync.Mutex
	d := testSched(t, store)
	d.Launch = sleepLaunch(80*time.Millisecond, spans, &mu)
	if err := d.schedule(plan, engine.Options{Record: true, Speed: 1}, "with-deps"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	u, o := spans["use"], spans["other"]
	mu.Unlock()
	if u[0].IsZero() || o[0].IsZero() {
		t.Fatalf("spans %v", spans)
	}
	if u[0].Before(o[1]) && o[0].Before(u[1]) {
		t.Fatalf("corrupt scene opened a lane: use=%v other=%v", u, o)
	}
}

func TestGroupStepMissingLanesRefusedBeforeLock(t *testing.T) {
	store := cliStore(t)
	saveStageSpec(t, store, "house-laptop", 2, 2<<30)
	locked := false
	d := testSched(t, store)
	d.Lock = func(names ...string) (func(), error) {
		locked = true
		return func() {}, nil
	}
	d.Launch = func(context.Context, jobSpec) (jobProc, error) {
		t.Fatal("launched")
		return nil, nil
	}
	err := d.schedule(&workspace.Plan{
		Steps: []workspace.PlanStep{{
			ID: "./use", Scene: "use", Stage: "house-laptop",
			Group: "household", Reason: workspace.ReasonRequested, Requested: true,
		}},
		Stages: []string{"house-laptop"},
	}, engine.Options{Record: true, Speed: 1}, "with-deps")
	if err == nil || !strings.Contains(err.Error(), "missing member lanes") {
		t.Fatalf("missing lanes: %v", err)
	}
	if locked {
		t.Fatal("locked before lanes check")
	}
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestSchedulerMintsOneGenerationForEveryChild(t *testing.T) {
	dir, store := cliTwoVM(t)
	var gens []string
	var mu sync.Mutex
	spans := map[string][2]time.Time{}
	d := testSched(t, store)
	d.Launch = func(ctx context.Context, spec jobSpec) (jobProc, error) {
		mu.Lock()
		gens = append(gens, spec.Opts.StateGeneration)
		mu.Unlock()
		return sleepLaunch(20*time.Millisecond, spans, &mu)(ctx, spec)
	}
	alpha := filepath.Join(dir, "scenes", "alpha.json")
	beta := filepath.Join(dir, "scenes", "beta.json")
	plan := &workspace.Plan{
		Steps: []workspace.PlanStep{
			{ID: "./alpha", Scene: "alpha", Path: alpha, Stage: "demo", Reason: workspace.ReasonRequested, Requested: true},
			{ID: "./beta", Scene: "beta", Path: beta, Stage: "lab", Reason: workspace.ReasonRequested, Requested: true},
		},
		Stages: []string{"demo", "lab"},
	}
	if err := d.schedule(plan, engine.Options{Record: true, Speed: 1}, "stale"); err != nil {
		t.Fatal(err)
	}
	if len(gens) != 2 || gens[0] == "" || gens[0] != gens[1] {
		t.Fatalf("generations: %v", gens)
	}
	if !engine.ValidStateGeneration(gens[0]) {
		t.Fatalf("id: %s", gens[0])
	}
}

type unlockProc struct {
	jobProc
	unlock func()
}

func (p *unlockProc) Wait() error {
	err := p.jobProc.Wait()
	if p.unlock != nil {
		p.unlock()
	}
	return err
}

func containsPair(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
