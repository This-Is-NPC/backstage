package engine

import (
	"context"
	"fmt"
	"sort"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

var restoreGroupMember = func(m *machine.Manager, ctx context.Context, r *machine.Record, snapshot, generation string) (bool, error) {
	return m.RestoreGroupMember(ctx, r, snapshot, generation)
}

var recoverGroupMember = func(m *machine.Manager, ctx context.Context, r *machine.Record) error {
	return m.Recover(ctx, r)
}

var checkGroupMembers = machine.CheckGroupMembers

func lockUnreserved(store *machine.Store, reserved map[string]bool, names []string) (func(), error) {
	var missing []string
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" || seen[name] || reserved[name] {
			continue
		}
		seen[name] = true
		missing = append(missing, name)
	}
	if len(missing) == 0 {
		return func() {}, nil
	}
	sort.Strings(missing)
	return store.LockMany(missing...)
}

func groupMemberStates(p *scene.Project, store *machine.Store, s *scene.Scene) ([]machine.GroupMemberState, string, error) {
	group := s.StartGroup()
	if group == "" {
		return nil, "", nil
	}
	snapshot := s.VMStart.Snapshot
	members, err := p.GroupMembers(group)
	if err != nil {
		return nil, "", err
	}
	out := make([]machine.GroupMemberState, 0, len(members))
	for _, m := range members {
		r, err := store.Load(m.Stage)
		if err != nil {
			out = append(out, machine.GroupMemberState{Alias: m.Alias, Stage: m.Stage})
			continue
		}
		out = append(out, machine.GroupMemberState{Alias: m.Alias, Stage: m.Stage, Record: r})
	}
	return out, snapshot, nil
}

func memberReady(r *machine.Record) error {
	if r == nil {
		return nil
	}
	if r.Status != "ready" {
		return fmt.Errorf("stage %s is not ready (%s)", r.Name, r.Status)
	}
	return nil
}

func (e *Engine) prepareGroup(ctx context.Context, s *scene.Scene, opts Options) error {
	e.groupMembers = nil
	members, snapshot, err := groupMemberStates(e.Project, e.Managed.Store, s)
	if err != nil {
		return err
	}
	if len(members) == 0 {
		return nil
	}
	for _, m := range members {
		if m.Record == nil {
			continue
		}
		if err := recoverGroupMember(e.Managed, ctx, m.Record); err != nil {
			return err
		}
		if err := memberReady(m.Record); err != nil {
			return err
		}
	}
	if err := checkGroupMembers(members, s.StartGroup(), snapshot, opts.Record); err != nil {
		return err
	}
	gen := machine.GroupGeneration(members, snapshot)
	skipped := map[string]bool{}
	for _, m := range members {
		if m.Alias == s.VM {
			continue
		}
		skip, err := restoreGroupMember(e.Managed, ctx, m.Record, snapshot, gen)
		if err != nil {
			return err
		}
		if skip {
			skipped[m.Stage] = true
		}
	}
	for _, m := range members {
		e.groupMembers = append(e.groupMembers, facts.GroupMember{
			Stage:          m.Stage,
			Snapshot:       snapshot,
			Image:          m.Record.Snapshots[snapshot],
			Generation:     gen,
			RestoreSkipped: skipped[m.Stage],
		})
	}
	return nil
}
