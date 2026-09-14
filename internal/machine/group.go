package machine

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

var restoreAfterCheck = func(m *Manager, ctx context.Context, r *Record, snapshot string) error {
	release, err := m.Store.LockWait(ctx, "image-catalog")
	if err != nil {
		return err
	}
	err = m.Restore(ctx, r, snapshot)
	release()
	return err
}

const (
	GroupMissing        = "missing"
	GroupGenerationKind = "generation"
	GroupRehearsal      = "rehearsal"
)

// GroupMemberState is one member's record for a group start check.
type GroupMemberState struct {
	Alias  string
	Stage  string
	Record *Record
}

// GroupMemberError is one member that cannot join a group start.
type GroupMemberError struct {
	Group    string
	Snapshot string
	Alias    string
	Stage    string
	Kind     string
}

func (e *GroupMemberError) Error() string {
	if e == nil {
		return "state group is incomplete"
	}
	switch e.Kind {
	case GroupRehearsal:
		return fmt.Sprintf("state-group %s: %s on %s came from a rehearsal", e.Group, e.Alias, e.Stage)
	case GroupMissing:
		return fmt.Sprintf("state-group %s incomplete: %s on %s missing %s", e.Group, e.Alias, e.Stage, e.Snapshot)
	default:
		return fmt.Sprintf("state-group %s incomplete: %s on %s (%s)", e.Group, e.Alias, e.Stage, e.Kind)
	}
}

// GroupMembersError lists every member that cannot join a group start.
type GroupMembersError struct {
	Group    string
	Snapshot string
	Members  []GroupMemberError
}

func (e *GroupMembersError) Error() string {
	if e == nil || len(e.Members) == 0 {
		return "state group is incomplete"
	}
	if len(e.Members) == 1 {
		return e.Members[0].Error()
	}
	parts := make([]string, 0, len(e.Members))
	for i := range e.Members {
		parts = append(parts, e.Members[i].Error())
	}
	return strings.Join(parts, "; ")
}

func groupKindRank(kind string) int {
	switch kind {
	case GroupRehearsal:
		return 0
	case GroupMissing:
		return 1
	default:
		return 2
	}
}

func sortGroupMemberErrors(problems []GroupMemberError) {
	sort.SliceStable(problems, func(i, j int) bool {
		return groupKindRank(problems[i].Kind) < groupKindRank(problems[j].Kind)
	})
}

func classifyGroupMember(m GroupMemberState, group, snapshot string, recording bool) (GroupMemberError, bool) {
	base := GroupMemberError{Group: group, Snapshot: snapshot, Alias: m.Alias, Stage: m.Stage}
	if m.Record == nil || m.Record.Snapshots[snapshot] == "" {
		base.Kind = GroupMissing
		return base, true
	}
	o, ok := originOf(m.Record, snapshot)
	if !ok || o.Group != group || o.Generation == "" {
		base.Kind = GroupGenerationKind
		return base, true
	}
	if recording && o.Take == TakeRehearsal {
		base.Kind = GroupRehearsal
		return base, true
	}
	return GroupMemberError{}, false
}

// CheckGroupMembers verifies every member, including the filmed stage,
// before any Restore. recording refuses a rehearsal origin on any member.
// Problems from every member are collected; rehearsal is listed first.
func CheckGroupMembers(members []GroupMemberState, group, snapshot string, recording bool) error {
	if group == "" || snapshot == "" || len(members) == 0 {
		return fmt.Errorf("state-group %q: no members", group)
	}
	var problems []GroupMemberError
	var gen string
	for _, m := range members {
		if p, bad := classifyGroupMember(m, group, snapshot, recording); bad {
			problems = append(problems, p)
			continue
		}
		o, _ := originOf(m.Record, snapshot)
		if gen == "" {
			gen = o.Generation
		} else if o.Generation != gen {
			problems = append(problems, GroupMemberError{
				Group: group, Snapshot: snapshot, Alias: m.Alias, Stage: m.Stage, Kind: GroupGenerationKind,
			})
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sortGroupMemberErrors(problems)
	return &GroupMembersError{Group: group, Snapshot: snapshot, Members: problems}
}

// GroupGeneration is the shared generation after CheckGroupMembers succeeds.
func GroupGeneration(members []GroupMemberState, snapshot string) string {
	if len(members) == 0 {
		return ""
	}
	o, ok := originOf(members[0].Record, snapshot)
	if !ok {
		return ""
	}
	return o.Generation
}

// RestoreGroupMember skips or restores one already-checked member. It does not boot.
func (m *Manager) RestoreGroupMember(ctx context.Context, r *Record, snapshot, generation string) error {
	if m.canSkipRestore(ctx, r, snapshot, generation) {
		return m.clearAtState(r)
	}
	if err := m.clearAtState(r); err != nil {
		return err
	}
	return restoreAfterCheck(m, ctx, r, snapshot)
}
