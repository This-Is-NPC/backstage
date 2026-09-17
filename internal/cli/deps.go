package cli

import (
	"io"
	"os"
	"sort"
	"time"

	"github.com/This-Is-NPC/backstage/internal/engine"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/workspace"
)

type sceneRun func(path string, opts engine.Options) error

type depsExec struct {
	Store        *machine.Store
	Out          io.Writer
	Run          sceneRun
	Lock         func(names ...string) (func(), error)
	Launch       jobLauncher
	Jobs         int
	JSON         bool
	MemAvailable func() (uint64, error)
	NumCPU       func() int
	JobsDir      string
	now          func() time.Time
	runSuffix    func() (string, error)
}

func newDepsExec(out io.Writer, store *machine.Store, run sceneRun) (*depsExec, error) {
	if store == nil {
		var err error
		store, err = machine.DefaultStore()
		if err != nil {
			return nil, err
		}
	}
	if out == nil {
		out = os.Stdout
	}
	return &depsExec{Store: store, Out: out, Run: run}, nil
}

func runWithDeps(out io.Writer, scenePath string, opts engine.Options, store *machine.Store, run sceneRun) error {
	d, err := newDepsExec(out, store, run)
	if err != nil {
		return err
	}
	return d.run(scenePath, opts)
}

func (d *depsExec) run(scenePath string, opts engine.Options) error {
	kind := workspace.KindPlay
	if !opts.Record {
		kind = workspace.KindRehearse
	}
	plan, err := workspace.PlanDeps(workspace.DepsOptions{
		Options:      workspace.Options{Dir: scenePath, Store: d.Store},
		ScenePath:    scenePath,
		Kind:         kind,
		Adopt:        opts.Adopt,
		ReplaceState: opts.ReplaceState,
	})
	if err != nil {
		return err
	}
	return d.schedule(plan, opts, "with-deps")
}

func (d *depsExec) runStale(dir string, opts engine.Options) error {
	if dir == "" {
		dir = "."
	}
	kind := workspace.KindPlay
	if !opts.Record {
		kind = workspace.KindRehearse
	}
	plan, err := workspace.PlanStale(workspace.DepsOptions{
		Options:      workspace.Options{Dir: dir, Store: d.Store},
		Kind:         kind,
		Adopt:        opts.Adopt,
		ReplaceState: opts.ReplaceState,
	})
	if err != nil {
		return err
	}
	return d.schedule(plan, opts, "stale")
}

type savedSnap struct {
	stage, snapshot, image string
}

func snapshotImages(store *machine.Store, stages []string) map[string]string {
	out := map[string]string{}
	if store == nil {
		return out
	}
	for _, stage := range stages {
		if stage == "" {
			continue
		}
		rec, err := store.Load(stage)
		if err != nil || rec == nil {
			continue
		}
		for snap, img := range rec.Snapshots {
			out[stage+"\x00"+snap] = img
		}
	}
	return out
}

func savedSince(store *machine.Store, stages []string, before map[string]string) []savedSnap {
	var out []savedSnap
	after := snapshotImages(store, stages)
	var keys []string
	for k, img := range after {
		if before[k] == img {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		stage, snap, ok := splitStageSnap(k)
		if !ok {
			continue
		}
		out = append(out, savedSnap{stage: stage, snapshot: snap, image: after[k]})
	}
	return out
}

func splitStageSnap(k string) (stage, snap string, ok bool) {
	for i := 0; i < len(k); i++ {
		if k[i] == 0 {
			return k[:i], k[i+1:], true
		}
	}
	return "", "", false
}
