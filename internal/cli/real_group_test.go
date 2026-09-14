package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/engine"
	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/machine"
)

// Opt-in only. Same binary-and-subprocess setup as TestRealParallelStages:
// the scheduler execs os.Executable(), so the parent must be cmd/backstage.
//
//	BACKSTAGE_VM_INTEGRATION=1 go test ./internal/cli -run TestRealStateGroup -v -count=1 -timeout 150m
func TestRealStateGroup(t *testing.T) {
	if os.Getenv("BACKSTAGE_VM_INTEGRATION") != "1" {
		t.Skip("set BACKSTAGE_VM_INTEGRATION=1 to provision real VMs")
	}
	if _, err := exec.LookPath("guestfish"); err != nil {
		t.Fatal("install libguestfs before the full VM acceptance test")
	}
	m, err := machine.New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 140*time.Minute)
	defer cancel()

	var id [4]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	prefix := "accept-" + hex.EncodeToString(id[:])
	nameA, nameB := prefix+"-a", prefix+"-b"
	var pgid atomic.Int32

	t.Cleanup(func() {
		if id := int(pgid.Load()); id > 0 {
			_ = syscall.Kill(-id, syscall.SIGKILL)
		}
		logStageTimings(t, m, nameA)
		logStageTimings(t, m, nameB)
		t.Logf("cleaning stages %s %s", nameA, nameB)
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		unlock, lockErr := m.Store.LockWait(clean, nameA, nameB, "image-catalog")
		if lockErr != nil {
			t.Errorf("cleanup lock: %v", lockErr)
			return
		}
		defer unlock()
		deleteStage(t, clean, m, nameA)
		deleteStage(t, clean, m, nameB)
	})

	bin := filepath.Join(t.TempDir(), "backstage")
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", bin, "../../cmd/backstage").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	createAcceptStage(t, ctx, m, nameA)
	createAcceptStage(t, ctx, m, nameB)

	ws := t.TempDir()
	writeGroupWorkspace(t, ws, nameA, nameB)
	usePath := filepath.Join(ws, "scenes", "use.json")
	makeBPath := filepath.Join(ws, "scenes", "make-b.json")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	grouped := runBackstage(t, ctx, &pgid, bin, "play", usePath, "--with-deps", "--json")
	if grouped.err != nil {
		t.Fatalf("play use --with-deps: %v\nstdout:\n%s\nstderr:\n%s", grouped.err, grouped.stdout, grouped.stderr)
	}
	logRunDocument(t, "part1", grouped)
	byScene := requireOKScenes(t, grouped.stdout, "make-a", "make-b", "use")
	requireProducersBeforeUse(t, grouped.evs, "make-a", "make-b")
	t.Logf("grouped jobs: %+v", byScene)

	a1 := loadLinked(t, m, nameA)
	b1 := loadLinked(t, m, nameB)
	logLinkedGenerations(t, "part1", a1, b1)
	if a1.origin.Generation != b1.origin.Generation {
		t.Fatalf("grouped generations A=%s B=%s", a1.origin.Generation, b1.origin.Generation)
	}
	gen1 := a1.origin.Generation
	requireUseGroupFacts(t, ws, gen1, a1, b1)
	useStart := firstSceneStatus(grouped.evs, "use", jobRunning)
	if useStart.IsZero() {
		t.Fatalf("use never reached running: %s", formatStamped(grouped.evs))
	}
	skipped := requireSilentMemberRestored(t, ctx, m, nameB, useStart)
	requireSilentMemberFacts(t, ws, nameB, skipped)
	if sceneOverlapsStage(runningSpans(grouped.evs), "use", nameB) {
		t.Fatalf("use overlapped a job on %s:%s", nameB, formatSpans(runningSpans(grouped.evs)))
	}
	requireRunFinished(t, m, nameA, nameB, &pgid)

	isolated := runBackstage(t, ctx, &pgid, bin, "play", makeBPath)
	logRunDocument(t, "part2-make-b", isolated)
	if isolated.err != nil {
		t.Fatalf("play make-b: %v\nstdout:\n%s\nstderr:\n%s", isolated.err, isolated.stdout, isolated.stderr)
	}
	if left := pidsInGroup(int(pgid.Load())); len(left) > 0 {
		t.Fatalf("isolated make-b children still alive: %v", left)
	}
	pgid.Store(0)
	aAfter := loadLinked(t, m, nameA)
	bAfter := loadLinked(t, m, nameB)
	logLinkedGenerations(t, "part2", aAfter, bAfter)
	if aAfter.origin.Generation != gen1 {
		t.Fatalf("isolated make-b changed A's generation %s → %s", gen1, aAfter.origin.Generation)
	}
	if bAfter.origin.Generation == gen1 || !engine.ValidStateGeneration(bAfter.origin.Generation) {
		t.Fatalf("isolated make-b generation %s, want a new id (was %s)", bAfter.origin.Generation, gen1)
	}
	t.Logf("isolated make-b generation %s (A still %s)", bAfter.origin.Generation, aAfter.origin.Generation)

	beforeA := mustDiskPrints(t, aAfter.rec)
	beforeB := mustDiskPrints(t, bAfter.rec)
	refused := runBackstage(t, ctx, &pgid, bin, "play", usePath)
	logRunDocument(t, "part2-use-refused", refused)
	if refused.err == nil {
		t.Fatalf("play use after isolated remake: want non-zero exit\nstdout:\n%s\nstderr:\n%s", refused.stdout, refused.stderr)
	}
	requireIncompleteGroupMessage(t, refused.stdout, refused.stderr, "b")
	if left := pidsInGroup(int(pgid.Load())); len(left) > 0 {
		t.Fatalf("refused use children still alive: %v", left)
	}
	pgid.Store(0)
	aRefuse := loadLinked(t, m, nameA)
	bRefuse := loadLinked(t, m, nameB)
	afterA := mustDiskPrints(t, aRefuse.rec)
	afterB := mustDiskPrints(t, bRefuse.rec)
	if afterA != beforeA {
		t.Fatalf("play use started a restore on A\nbefore=%+v\nafter=%+v", beforeA, afterA)
	}
	if afterB != beforeB {
		t.Fatalf("play use started a restore on B\nbefore=%+v\nafter=%+v", beforeB, afterB)
	}

	recompose := runBackstage(t, ctx, &pgid, bin, "play", usePath, "--with-deps", "--json")
	logRunDocument(t, "part3", recompose)
	if recompose.err != nil {
		t.Fatalf("recompose play use --with-deps: %v\nstdout:\n%s\nstderr:\n%s", recompose.err, recompose.stdout, recompose.stderr)
	}
	requireOKScenes(t, recompose.stdout, "make-a", "make-b", "use")
	a2 := loadLinked(t, m, nameA)
	b2 := loadLinked(t, m, nameB)
	logLinkedGenerations(t, "part3", a2, b2)
	requireUseGroupFacts(t, ws, a2.origin.Generation, a2, b2)
	if a2.origin.Generation != b2.origin.Generation {
		t.Fatalf("recomposed generations A=%s B=%s", a2.origin.Generation, b2.origin.Generation)
	}
	if !engine.ValidStateGeneration(a2.origin.Generation) {
		t.Fatalf("recomposed generation %q", a2.origin.Generation)
	}
	t.Logf("recomposed generation %s", a2.origin.Generation)
	if left := pidsInGroup(int(pgid.Load())); len(left) > 0 {
		t.Fatalf("recompose children still alive: %v", left)
	}
	pgid.Store(0)
}

func writeGroupWorkspace(t *testing.T, ws, nameA, nameB string) {
	t.Helper()
	writeFile(t, filepath.Join(ws, "backstage.json"), fmt.Sprintf(`{
		"record": {"out": "recordings", "fps": 30},
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"vms": {
			"a": {"stage": %q},
			"b": {"stage": %q}
		},
		"state-groups": {"pair": ["a", "b"]}
	}`, nameA, nameB))
	writeFile(t, filepath.Join(ws, "scenes", "make-a.json"), `{
		"name":"make-a","layout":"solo","vm":"a",
		"vm-start":{"mode":"clean","snapshot":"initial"},
		"vm-end":{"snapshot":"linked","group":"pair"},
		"steps":[{"action":"wait"}]
	}`)
	writeFile(t, filepath.Join(ws, "scenes", "make-b.json"), `{
		"name":"make-b","layout":"solo","vm":"b",
		"vm-start":{"mode":"clean","snapshot":"initial"},
		"vm-end":{"snapshot":"linked","group":"pair"},
		"steps":[{"action":"wait"}]
	}`)
	writeFile(t, filepath.Join(ws, "scenes", "use.json"), `{
		"name":"use","layout":"solo","vm":"a",
		"vm-start":{"mode":"clean","snapshot":"linked","group":"pair"},
		"steps":[{"action":"wait"}]
	}`)
}

type backstageRun struct {
	stdout string
	stderr string
	evs    []stampedProgress
	err    error
}

func runBackstage(t *testing.T, ctx context.Context, pgid *atomic.Int32, bin string, args ...string) backstageRun {
	t.Helper()
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = null
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 5 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	var (
		mu        sync.Mutex
		stdoutBuf strings.Builder
		stderrBuf strings.Builder
		evs       []stampedProgress
	)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stdoutR)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			mu.Lock()
			stdoutBuf.WriteString(line)
			stdoutBuf.WriteByte('\n')
			var p jobProgressLine
			if json.Unmarshal([]byte(line), &p) == nil && p.Method == "job.progress" {
				evs = append(evs, stampedProgress{at: time.Now(), line: p})
			}
			mu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderrR)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			t.Logf("stderr: %s", line)
			mu.Lock()
			stderrBuf.WriteString(line)
			stderrBuf.WriteByte('\n')
			mu.Unlock()
		}
	}()

	if err := cmd.Start(); err != nil {
		_ = stdoutW.Close()
		_ = stderrW.Close()
		t.Fatal(err)
	}
	pgid.Store(int32(cmd.Process.Pid))
	_ = stdoutW.Close()
	_ = stderrW.Close()
	waitErr := cmd.Wait()
	wg.Wait()
	_ = stdoutR.Close()
	_ = stderrR.Close()

	mu.Lock()
	run := backstageRun{stdout: stdoutBuf.String(), stderr: stderrBuf.String(), evs: append([]stampedProgress(nil), evs...), err: waitErr}
	mu.Unlock()
	t.Logf("stdout:\n%s", run.stdout)
	if run.stderr != "" {
		t.Logf("stderr (full):\n%s", run.stderr)
	}
	if run.err != nil {
		logFailedJobOutputs(t, run)
	}
	return run
}

func logFailedJobOutputs(t *testing.T, run backstageRun) {
	t.Helper()
	rep, err := parseFinalJobReport(run.stdout)
	if err != nil {
		return
	}
	for _, j := range rep.Jobs {
		if j.Status != jobFailed || j.Log == "" {
			continue
		}
		body, err := os.ReadFile(j.Log)
		if err != nil {
			t.Logf("failed job %s (%s) log %s: %v", j.ID, j.Scene, j.Log, err)
			continue
		}
		t.Logf("failed job %s (%s) log:\n%s", j.ID, j.Scene, body)
	}
}

func requireOKScenes(t *testing.T, stdout string, scenes ...string) map[string]jobReportItem {
	t.Helper()
	rep, err := parseFinalJobReport(stdout)
	if err != nil {
		t.Fatalf("final document: %v\n%s", err, stdout)
	}
	byScene := map[string]jobReportItem{}
	for _, j := range rep.Jobs {
		if j.Status != jobOK {
			t.Fatalf("job %s scene %s status %s, want ok", j.ID, j.Scene, j.Status)
		}
		byScene[j.Scene] = j
	}
	if len(byScene) != len(scenes) {
		t.Fatalf("jobs %d, want %d %v: %+v", len(byScene), len(scenes), scenes, rep.Jobs)
	}
	for _, name := range scenes {
		if _, ok := byScene[name]; !ok {
			t.Fatalf("missing job %s in %+v", name, rep.Jobs)
		}
	}
	return byScene
}

func requireProducersBeforeUse(t *testing.T, evs []stampedProgress, producers ...string) {
	t.Helper()
	useRun := firstSceneStatus(evs, "use", jobRunning)
	if useRun.IsZero() {
		t.Fatalf("use never running: %s", formatStamped(evs))
	}
	for _, name := range producers {
		okAt := firstSceneStatus(evs, name, jobOK)
		if okAt.IsZero() {
			t.Fatalf("%s never ok: %s", name, formatStamped(evs))
		}
		if useRun.Before(okAt) {
			t.Fatalf("use running before %s ok: %s-ok=%v use-run=%v evs=%s", name, name, okAt, useRun, formatStamped(evs))
		}
	}
}

type linkedSnap struct {
	rec    *machine.Record
	origin machine.SnapshotOrigin
}

func loadLinked(t *testing.T, m *machine.Manager, stage string) linkedSnap {
	t.Helper()
	rec, err := m.Store.Load(stage)
	if err != nil {
		t.Fatal(err)
	}
	o, ok := rec.Origin("linked")
	if !ok || o.Group != "pair" || !engine.ValidStateGeneration(o.Generation) {
		t.Fatalf("linked origin on %s: ok=%v %+v", stage, ok, o)
	}
	if rec.Snapshots["linked"] == "" {
		t.Fatalf("%s missing linked image", stage)
	}
	return linkedSnap{rec: rec, origin: o}
}

func logLinkedGenerations(t *testing.T, part string, a, b linkedSnap) {
	t.Helper()
	t.Logf("%s generations A=%s B=%s", part, a.origin.Generation, b.origin.Generation)
}

func logRunDocument(t *testing.T, label string, run backstageRun) {
	t.Helper()
	if rep, err := parseFinalJobReport(run.stdout); err == nil {
		body, err := json.Marshal(rep)
		if err != nil {
			t.Fatalf("%s marshal: %v", label, err)
		}
		t.Logf("%s final document: %s", label, body)
		return
	}
	t.Logf("%s stdout:\n%s", label, run.stdout)
	if run.stderr != "" {
		t.Logf("%s stderr:\n%s", label, run.stderr)
	}
}

func requireUseGroupFacts(t *testing.T, ws, gen string, a, b linkedSnap) {
	t.Helper()
	got := readSceneFacts(t, ws, "use")
	body, err := json.Marshal(got.GroupMembers)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("use group-members: %s", body)
	if len(got.GroupMembers) != 2 {
		t.Fatalf("group-members: %+v", got.GroupMembers)
	}
	byStage := map[string]facts.GroupMember{}
	for _, mem := range got.GroupMembers {
		byStage[mem.Stage] = mem
		if mem.Snapshot != "linked" || mem.Generation != gen || mem.Image == "" {
			t.Fatalf("group-member %+v, want generation %s snapshot linked", mem, gen)
		}
	}
	if mem, ok := byStage[a.rec.Name]; !ok || mem.Image != a.rec.Snapshots["linked"] {
		t.Fatalf("facts member a: %+v want image %q", got.GroupMembers, a.rec.Snapshots["linked"])
	}
	if mem, ok := byStage[b.rec.Name]; !ok || mem.Image != b.rec.Snapshots["linked"] {
		t.Fatalf("facts member b: %+v want image %q", got.GroupMembers, b.rec.Snapshots["linked"])
	}
}

type timedLine struct {
	at   time.Time
	text string
}

func readProvisionTimings(t *testing.T, m *machine.Manager, name string) []timedLine {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(m.Store.Dir(name), "provision.log"))
	if err != nil {
		t.Fatalf("provision.log %s: %v", name, err)
	}
	var out []timedLine
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.Contains(line, "timing ") {
			continue
		}
		stamp, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		at, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			t.Fatalf("provision.log %s stamp %q: %v", name, stamp, err)
		}
		out = append(out, timedLine{at: at, text: rest})
	}
	return out
}

func requireSilentMemberFacts(t *testing.T, ws, nameB string, skipped bool) {
	t.Helper()
	got := readSceneFacts(t, ws, "use")
	body, err := json.Marshal(got.GroupMembers)
	if err != nil {
		t.Fatal(err)
	}
	for _, mem := range got.GroupMembers {
		if mem.Stage != nameB {
			continue
		}
		if skipped && !mem.RestoreSkipped {
			t.Fatalf("silent %s skipped restore but group-member restore-skipped=false: %s", nameB, body)
		}
		if !skipped && mem.RestoreSkipped {
			t.Fatalf("silent %s restored but group-member restore-skipped=true: %s", nameB, body)
		}
		return
	}
	t.Fatalf("no group-member %s: %s", nameB, body)
}

func requireSilentMemberRestored(t *testing.T, ctx context.Context, m *machine.Manager, nameB string, useStart time.Time) bool {
	t.Helper()
	lines := readProvisionTimings(t, m, nameB)
	cut := useStart.Truncate(time.Second)
	var restoreAt time.Time
	var restoreText string
	for _, l := range lines {
		if l.at.Before(cut) {
			continue
		}
		if strings.Contains(l.text, "timing restore-") {
			restoreAt = l.at
			restoreText = l.text
			break
		}
	}
	if restoreAt.IsZero() {
		var dump strings.Builder
		for _, l := range lines {
			fmt.Fprintf(&dump, "\n  %s %s", l.at.Format(time.RFC3339), l.text)
		}
		t.Fatalf("no timing restore-* / restore-skipped on %s after use started %v%s", nameB, useStart, dump.String())
	}
	t.Logf("silent %s after use: %s %s", nameB, restoreAt.Format(time.RFC3339), restoreText)
	for _, l := range lines {
		if l.at.Before(restoreAt) {
			continue
		}
		if strings.Contains(l.text, "timing boot-seconds") {
			t.Fatalf("silent member booted after use restore (%s): %s %s", restoreText, l.at.Format(time.RFC3339), l.text)
		}
	}
	rec, err := m.Store.Load(nameB)
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.State(ctx, rec)
	if err != nil {
		t.Fatalf("domstate %s: %v", nameB, err)
	}
	if state != "shut off" {
		t.Fatalf("silent member %s state %q, want shut off", nameB, state)
	}
	return strings.Contains(restoreText, "restore-skipped")
}

func sceneOverlapsStage(spans []runSpan, scene, stage string) bool {
	var target, others []runSpan
	for _, s := range spans {
		if s.Scene == scene {
			target = append(target, s)
			continue
		}
		if s.Stage == stage {
			others = append(others, s)
		}
	}
	for _, x := range target {
		for _, y := range others {
			if x.Start.Before(y.End) && y.Start.Before(x.End) {
				return true
			}
		}
	}
	return false
}

func requireRunFinished(t *testing.T, m *machine.Manager, nameA, nameB string, pgid *atomic.Int32) {
	t.Helper()
	unlock, err := m.Store.LockMany(nameA, nameB)
	if err != nil {
		t.Fatalf("LockMany(%s, %s) after run: %v", nameA, nameB, err)
	}
	unlock()
	pending := filepath.Join(m.Store.Root, "pending")
	ents, err := os.ReadDir(pending)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("pending: %v", err)
	}
	if len(ents) > 0 {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("pending not empty: %v", names)
	}
	if left := pidsInGroup(int(pgid.Load())); len(left) > 0 {
		t.Fatalf("run children still alive in pgid %d: %v", pgid.Load(), left)
	}
	pgid.Store(0)
}

func requireIncompleteGroupMessage(t *testing.T, stdout, stderr, member string) {
	t.Helper()
	msg := stdout + "\n" + stderr
	if !strings.Contains(msg, "incomplete") || !strings.Contains(msg, "generation") {
		t.Fatalf("want incomplete group naming generation:\n%s", msg)
	}
	if !strings.Contains(msg, " "+member+" on ") && !strings.Contains(msg, ": "+member+" on ") {
		t.Fatalf("want member %s in incomplete message:\n%s", member, msg)
	}
}

type diskPrints struct {
	disk  machine.FilePrint
	nvram machine.FilePrint
}

func mustDiskPrints(t *testing.T, rec *machine.Record) diskPrints {
	t.Helper()
	disk, err := statFilePrint(rec.Disk)
	if err != nil {
		t.Fatalf("stat disk %s: %v", rec.Disk, err)
	}
	nvram, err := statFilePrint(rec.NVRAM)
	if err != nil {
		t.Fatalf("stat nvram %s: %v", rec.NVRAM, err)
	}
	return diskPrints{disk: disk, nvram: nvram}
}

func statFilePrint(path string) (machine.FilePrint, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return machine.FilePrint{}, err
	}
	return machine.FilePrint{
		Path:    path,
		Inode:   st.Ino,
		Size:    st.Size,
		MtimeNs: st.Mtim.Nano(),
		CtimeNs: st.Ctim.Nano(),
	}, nil
}
