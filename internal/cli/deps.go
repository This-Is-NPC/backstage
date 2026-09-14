package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"

	"github.com/This-Is-NPC/backstage/internal/engine"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/workspace"
)

type sceneRun func(path string, opts engine.Options) error

type depsExec struct {
	Store *machine.Store
	Out   io.Writer
	Run   sceneRun
	Lock  func(names ...string) (func(), error)
}

func runWithDeps(out io.Writer, scenePath string, opts engine.Options, store *machine.Store, run sceneRun) error {
	if store == nil {
		var err error
		store, err = machine.DefaultStore()
		if err != nil {
			return err
		}
	}
	if out == nil {
		out = os.Stdout
	}
	if run == nil {
		run = runScene
	}
	exec := depsExec{Store: store, Out: out, Run: run, Lock: store.LockMany}
	return exec.run(scenePath, opts)
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
	writeDepsPlan(d.Out, plan)
	for _, w := range plan.Warnings {
		fmt.Fprintf(d.Out, ">> warning: %s: %s\n", w.Path, w.Error)
	}

	if plan.AdoptSnapshot != "" && opts.ConfirmAdopt != nil {
		if err := opts.ConfirmAdopt(plan.AdoptSnapshot); err != nil {
			if takeInterrupted(err, opts.Context) {
				return interrupted(err)
			}
			return err
		}
		opts.ConfirmAdopt = func(string) error { return nil }
	}

	reserved := map[string]bool{}
	var names []string
	for _, st := range plan.Stages {
		if st == "" {
			continue
		}
		reserved[st] = true
		names = append(names, st)
	}
	if len(names) > 0 {
		lock := d.Lock
		if lock == nil {
			lock = d.Store.LockMany
		}
		release, err := lock(names...)
		if err != nil {
			return err
		}
		defer release()
	}
	opts.ReservedStages = reserved

	before := snapshotImages(d.Store, plan.Stages)
	var mu sync.Mutex
	var ran []string
	var failed string
	var failErr error
	started := map[string]bool{}
	var midStep string
	var interruptedID string
	var interruptedBefore string
	var reportOnce sync.Once
	printReport := func() {
		reportOnce.Do(func() {
			mu.Lock()
			defer mu.Unlock()
			for _, id := range ran {
				fmt.Fprintf(d.Out, ">> ran %s\n", id)
			}
			if interruptedID != "" {
				fmt.Fprintf(d.Out, ">> interrupted %s\n", interruptedID)
			}
			if interruptedBefore != "" {
				fmt.Fprintf(d.Out, ">> interrupted before %s\n", interruptedBefore)
			}
			if failed != "" {
				fmt.Fprintf(d.Out, ">> failed %s: %v\n", failed, failErr)
			}
			for _, step := range plan.Steps {
				if step.ID == failed || step.ID == interruptedID {
					continue
				}
				if started[step.ID] && step.ID != interruptedBefore {
					continue
				}
				fmt.Fprintf(d.Out, ">> not run %s\n", step.ID)
			}
			for _, s := range savedSince(d.Store, plan.Stages, before) {
				fmt.Fprintf(d.Out, ">> saved %s %s %s\n", s.stage, s.snapshot, s.image)
			}
		})
	}

	prevInterrupt := opts.OnInterrupt
	opts.OnInterrupt = func() {
		mu.Lock()
		if midStep != "" && interruptedID == "" {
			interruptedID = midStep
		}
		mu.Unlock()
		printReport()
		if prevInterrupt != nil {
			prevInterrupt()
		}
	}

	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}

	for _, step := range plan.Steps {
		if err := ctx.Err(); err != nil {
			mu.Lock()
			interruptedBefore = step.ID
			failErr = interrupted(err)
			mu.Unlock()
			break
		}
		stepOpts := opts
		if !step.Requested {
			stepOpts.Adopt = false
			stepOpts.ConfirmAdopt = nil
		}
		mu.Lock()
		started[step.ID] = true
		midStep = step.ID
		mu.Unlock()
		err := d.Run(step.Path, stepOpts)
		mu.Lock()
		midStep = ""
		if interruptedID == step.ID || takeInterrupted(err, ctx) {
			if interruptedID == "" {
				interruptedID = step.ID
			}
			if failErr == nil {
				failErr = interrupted(err)
			}
			mu.Unlock()
			break
		}
		if err != nil {
			failErr = err
			failed = step.ID
			mu.Unlock()
			break
		}
		ran = append(ran, step.ID)
		mu.Unlock()
	}

	printReport()
	if failErr != nil {
		return failErr
	}
	return nil
}

func writeDepsPlan(out io.Writer, plan *workspace.Plan) {
	fmt.Fprintln(out, ">> with-deps")
	for _, s := range plan.Steps {
		rel := s.ProjectRel
		if rel == "" {
			rel = "."
		}
		stage := s.Stage
		if stage == "" {
			stage = "-"
		}
		fmt.Fprintf(out, "  %s  %s  %s  %s\n", s.Scene, rel, stage, s.Reason)
	}
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
