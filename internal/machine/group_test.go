package machine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func groupOrigin(group, gen, take string) SnapshotOrigin {
	o := testOrigin("/p", "s")
	o.Group = group
	o.Generation = gen
	o.Take = take
	return o
}

func member(alias, stage string, r *Record) GroupMemberState {
	return GroupMemberState{Alias: alias, Stage: stage, Record: r}
}

func TestCheckGroupMembersIncludesFilmed(t *testing.T) {
	laptop := testRecord("house-laptop")
	laptop.Snapshots["linked"] = "img-l"
	laptop.SnapshotOrigins = map[string]SnapshotOrigin{"linked": groupOrigin("household", "gen-b", TakeRecording)}
	server := testRecord("house-server")
	server.Snapshots["linked"] = "img-s"
	server.SnapshotOrigins = map[string]SnapshotOrigin{"linked": groupOrigin("household", "gen-a", TakeRecording)}
	err := CheckGroupMembers([]GroupMemberState{
		member("laptop", "house-laptop", laptop),
		member("server", "house-server", server),
	}, "household", "linked", true)
	if err == nil {
		t.Fatal("filmed other generation was ignored")
	}
	var ge *GroupMemberError
	if !asGroupErr(err, &ge) || ge.Kind != GroupGenerationKind {
		t.Fatalf("typed: %v", err)
	}
}

func TestCheckGroupMembersMissingAndRehearsal(t *testing.T) {
	ok := func(stage, gen, take string) *Record {
		r := testRecord(stage)
		r.Snapshots["linked"] = "img"
		r.SnapshotOrigins = map[string]SnapshotOrigin{"linked": groupOrigin("household", gen, take)}
		return r
	}
	err := CheckGroupMembers([]GroupMemberState{
		member("laptop", "house-laptop", ok("house-laptop", "g1", TakeRecording)),
		member("server", "house-server", testRecord("house-server")),
	}, "household", "linked", true)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing: %v", err)
	}

	err = CheckGroupMembers([]GroupMemberState{
		member("laptop", "house-laptop", ok("house-laptop", "g1", TakeRecording)),
		member("server", "house-server", ok("house-server", "g1", TakeRehearsal)),
	}, "household", "linked", true)
	if err == nil || !strings.Contains(err.Error(), "rehearsal") {
		t.Fatalf("play from rehearsal: %v", err)
	}

	if err := CheckGroupMembers([]GroupMemberState{
		member("laptop", "house-laptop", ok("house-laptop", "g1", TakeRecording)),
		member("server", "house-server", ok("house-server", "g1", TakeRecording)),
	}, "household", "linked", false); err != nil {
		t.Fatalf("rehearse from recorded: %v", err)
	}
}

func TestRestoreGroupMemberRequiresGeneration(t *testing.T) {
	m, r, _ := seedCapturedState(t, "ready")
	r.SnapshotOrigins["ready"] = groupOrigin("household", "g1", TakeRecording)
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	creates := 0
	state := "shut off"
	attachStageRunner(m, r, &state, &creates)
	skipped, err := m.RestoreGroupMember(context.Background(), r, "ready", "g2")
	if err != nil {
		t.Fatal(err)
	}
	if skipped || creates == 0 {
		t.Fatal("matching fingerprint but wrong generation skipped restore")
	}

	m, r, _ = seedCapturedState(t, "ready")
	r.SnapshotOrigins["ready"] = groupOrigin("household", "g1", TakeRecording)
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	creates = 0
	state = "shut off"
	attachStageRunner(m, r, &state, &creates)
	skipped, err = m.RestoreGroupMember(context.Background(), r, "ready", "g1")
	if err != nil {
		t.Fatal(err)
	}
	if !skipped || creates != 0 {
		t.Fatal("matching generation still restored")
	}

	m, r, _ = seedCapturedState(t, "ready")
	creates = 0
	state = "shut off"
	attachStageRunner(m, r, &state, &creates)
	if !m.canSkipRestore(context.Background(), r, "ready", "") {
		t.Fatal("empty generation must keep A12 skip")
	}
}

func TestSnapshotOriginGroupPersists(t *testing.T) {
	m := testManager(t)
	r := testRecord("demo")
	o := groupOrigin("household", "abc", TakeRecording)
	o.Image = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	r.Snapshots["linked"] = o.Image
	r.SnapshotOrigins = map[string]SnapshotOrigin{"linked": o}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	got, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	have, ok := got.Origin("linked")
	if !ok || have.Group != "household" || have.Generation != "abc" {
		t.Fatalf("origin: %+v", have)
	}
	info := got.SnapshotInfo()["linked"]
	if info.Origin == nil || info.Origin.Group != "household" || info.Origin.Generation != "abc" {
		t.Fatalf("info: %+v", info)
	}
}

func TestCheckGroupMembersReportsEveryMember(t *testing.T) {
	ok := func(stage, gen, take string) *Record {
		r := testRecord(stage)
		r.Snapshots["linked"] = "img"
		r.SnapshotOrigins = map[string]SnapshotOrigin{"linked": groupOrigin("household", gen, take)}
		return r
	}
	err := CheckGroupMembers([]GroupMemberState{
		member("laptop", "house-laptop", testRecord("house-laptop")),
		member("server", "house-server", ok("house-server", "g1", TakeRehearsal)),
	}, "household", "linked", true)
	if err == nil || !strings.Contains(err.Error(), "missing") || !strings.Contains(err.Error(), "rehearsal") {
		t.Fatalf("both members: %v", err)
	}
	var many *GroupMembersError
	if !errors.As(err, &many) || len(many.Members) != 2 {
		t.Fatalf("typed members: %v", err)
	}
	if many.Members[0].Kind != GroupRehearsal || many.Members[1].Kind != GroupMissing {
		t.Fatalf("precedence: %+v", many.Members)
	}
}

func TestCheckGroupMembersWrongGroupName(t *testing.T) {
	laptop := testRecord("house-laptop")
	laptop.Snapshots["linked"] = "img-l"
	o := groupOrigin("office", "g1", TakeRecording)
	laptop.SnapshotOrigins = map[string]SnapshotOrigin{"linked": o}
	server := testRecord("house-server")
	server.Snapshots["linked"] = "img-s"
	server.SnapshotOrigins = map[string]SnapshotOrigin{"linked": groupOrigin("household", "g1", TakeRecording)}
	err := CheckGroupMembers([]GroupMemberState{
		member("laptop", "house-laptop", laptop),
		member("server", "house-server", server),
	}, "household", "linked", true)
	if err == nil {
		t.Fatal("wrong group name was accepted")
	}
	var ge *GroupMemberError
	if !asGroupErr(err, &ge) || ge.Kind != GroupGenerationKind || ge.Alias != "laptop" {
		t.Fatalf("wrong group: %v", err)
	}
}

func TestCheckGroupMembersEmptyGeneration(t *testing.T) {
	laptop := testRecord("house-laptop")
	laptop.Snapshots["linked"] = "img-l"
	o := groupOrigin("household", "", TakeRecording)
	laptop.SnapshotOrigins = map[string]SnapshotOrigin{"linked": o}
	server := testRecord("house-server")
	server.Snapshots["linked"] = "img-s"
	server.SnapshotOrigins = map[string]SnapshotOrigin{"linked": groupOrigin("household", "g1", TakeRecording)}
	err := CheckGroupMembers([]GroupMemberState{
		member("laptop", "house-laptop", laptop),
		member("server", "house-server", server),
	}, "household", "linked", true)
	if err == nil {
		t.Fatal("empty generation was accepted")
	}
	var ge *GroupMemberError
	if !asGroupErr(err, &ge) || ge.Kind != GroupGenerationKind || ge.Alias != "laptop" {
		t.Fatalf("empty generation: %v", err)
	}
}

func TestRestoreGroupMemberSkipLogsAndKeepsAtState(t *testing.T) {
	m, r, _ := seedCapturedState(t, "ready")
	r.SnapshotOrigins["ready"] = groupOrigin("household", "g1", TakeRecording)
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	creates := 0
	state := "shut off"
	attachStageRunner(m, r, &state, &creates)
	skipped, err := m.RestoreGroupMember(context.Background(), r, "ready", "g1")
	if err != nil {
		t.Fatal(err)
	}
	if !skipped || creates != 0 {
		t.Fatal("matching generation still restored")
	}
	if !strings.Contains(readProvisionLog(t, m, "demo"), "timing restore-skipped true") {
		t.Fatal("skip did not log restore-skipped on the member")
	}
	if loadedAtState(t, m) == nil {
		t.Fatal("skip cleared at-state")
	}
}

func TestRestoreGroupMemberRestoreClearsAtState(t *testing.T) {
	m, r, _ := seedCapturedState(t, "ready")
	r.SnapshotOrigins["ready"] = groupOrigin("household", "g1", TakeRecording)
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	creates := 0
	state := "shut off"
	attachStageRunner(m, r, &state, &creates)
	skipped, err := m.RestoreGroupMember(context.Background(), r, "ready", "g2")
	if err != nil {
		t.Fatal(err)
	}
	if skipped || creates == 0 {
		t.Fatal("wrong generation skipped restore")
	}
	log := readProvisionLog(t, m, "demo")
	if !strings.Contains(log, "timing restore-stop-seconds") || !strings.Contains(log, "timing restore-activate-seconds") {
		t.Fatalf("restore timings: %s", log)
	}
	if strings.Contains(log, "timing restore-skipped") {
		t.Fatalf("restore logged skip: %s", log)
	}
	if loadedAtState(t, m) != nil {
		t.Fatal("restore left at-state")
	}
}

func asGroupErr(err error, dest **GroupMemberError) bool {
	if err == nil {
		return false
	}
	var many *GroupMembersError
	if errors.As(err, &many) && len(many.Members) > 0 {
		*dest = &many.Members[0]
		return true
	}
	var ge *GroupMemberError
	if errors.As(err, &ge) {
		*dest = ge
		return true
	}
	return false
}
