package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/budget"
	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/machine"
)

// Opt-in only. The scheduler execs os.Executable(), so a go test parent would
// spawn the test binary. This builds cmd/backstage and runs the parent as
// that CLI so children are real backstage processes.
//
//	BACKSTAGE_VM_INTEGRATION=1 go test ./internal/cli -run TestRealParallelStages -v -count=1 -timeout 150m
func TestRealParallelStages(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Minute)
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
	writeParallelWorkspace(t, ws, nameA, nameB)
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	cpus := budget.NumCPU()
	mem, memErr := budget.ReadMemAvailable()
	t.Logf("host budget NumCPU=%d MemAvailable=%d (%s) memoryBudget=%d", cpus, mem, fmtBytes(mem), budget.MemoryBudget(mem))
	if memErr != nil {
		t.Logf("MemAvailable: %v", memErr)
	}

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

	cmd := exec.CommandContext(ctx, bin, "play", "--stale", ws, "--json")
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
		mu          sync.Mutex
		stdoutBuf   strings.Builder
		stderrBuf   strings.Builder
		transitions []stampedProgress
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
				transitions = append(transitions, stampedProgress{at: time.Now(), line: p})
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
	stdout := stdoutBuf.String()
	stderr := stderrBuf.String()
	evs := append([]stampedProgress(nil), transitions...)
	mu.Unlock()
	t.Logf("stdout:\n%s", stdout)
	if stderr != "" {
		t.Logf("stderr (full):\n%s", stderr)
	}

	if waitErr != nil {
		t.Fatalf("play --stale exit: %v\nstdout:\n%s\nstderr:\n%s", waitErr, stdout, stderr)
	}

	rep, err := parseFinalJobReport(stdout)
	if err != nil {
		t.Fatalf("final document: %v\n%s", err, stdout)
	}
	if len(rep.Jobs) != 4 {
		t.Fatalf("jobs %d, want 4: %+v", len(rep.Jobs), rep.Jobs)
	}
	byScene := map[string]jobReportItem{}
	for _, j := range rep.Jobs {
		if j.Status != jobOK {
			t.Fatalf("job %s scene %s status %s, want ok", j.ID, j.Scene, j.Status)
		}
		byScene[j.Scene] = j
		logPath := filepath.Join(stateHome, "backstage", "jobs", rep.RunID, j.ID+".log")
		body, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("job log %s: %v", logPath, err)
		}
		if len(body) == 0 {
			t.Fatalf("job log empty: %s", logPath)
		}
	}
	for _, name := range []string{"pa", "ca", "pb", "cb"} {
		if _, ok := byScene[name]; !ok {
			t.Fatalf("missing job %s in %+v", name, rep.Jobs)
		}
	}

	paOK := firstSceneStatus(evs, "pa", jobOK)
	caRun := firstSceneStatus(evs, "ca", jobRunning)
	pbOK := firstSceneStatus(evs, "pb", jobOK)
	cbRun := firstSceneStatus(evs, "cb", jobRunning)
	if paOK.IsZero() || caRun.IsZero() || pbOK.IsZero() || cbRun.IsZero() {
		t.Fatalf("missing transition timestamps pa-ok=%v ca-run=%v pb-ok=%v cb-run=%v evs=%s", paOK, caRun, pbOK, cbRun, formatStamped(evs))
	}
	if caRun.Before(paOK) {
		t.Fatalf("ca running before pa ok: pa-ok=%v ca-run=%v evs=%s", paOK, caRun, formatStamped(evs))
	}
	if cbRun.Before(pbOK) {
		t.Fatalf("cb running before pb ok: pb-ok=%v cb-run=%v evs=%s", pbOK, cbRun, formatStamped(evs))
	}

	spans := runningSpans(evs)
	if !stagesOverlap(spans, nameA, nameB) {
		t.Fatalf("no overlapping running interval between %s and %s\n  spans=%s\n  NumCPU=%d MemAvailable=%d (%s) err=%v memoryBudget=%d spec=%d CPU %s",
			nameA, nameB, formatSpans(spans), cpus, mem, fmtBytes(mem), memErr, budget.MemoryBudget(mem),
			machine.DefaultSpec().CPUs, fmtBytes(machine.DefaultSpec().Memory))
	}

	recA, err := m.Store.Load(nameA)
	if err != nil {
		t.Fatal(err)
	}
	recB, err := m.Store.Load(nameB)
	if err != nil {
		t.Fatal(err)
	}
	readyA := recA.Snapshots["ready-a"]
	readyB := recB.Snapshots["ready-b"]
	if readyA == "" || readyB == "" {
		t.Fatalf("captured images ready-a=%q ready-b=%q", readyA, readyB)
	}
	caFacts := readSceneFacts(t, ws, "ca")
	cbFacts := readSceneFacts(t, ws, "cb")
	if caFacts.StartImage != readyA {
		t.Fatalf("ca start-image=%q want %q", caFacts.StartImage, readyA)
	}
	if cbFacts.StartImage != readyB {
		t.Fatalf("cb start-image=%q want %q", cbFacts.StartImage, readyB)
	}
	for _, name := range []string{"pa", "pb"} {
		f := readSceneFacts(t, ws, name)
		if f.Timings == nil || f.Timings.CatalogWaitSeconds == nil {
			t.Fatalf("%s facts missing catalog-wait-seconds: %+v", name, f.Timings)
		}
	}

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

func createAcceptStage(t *testing.T, ctx context.Context, m *machine.Manager, name string) {
	t.Helper()
	release, err := m.Store.LockMany(name, "image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	spec := machine.DefaultSpec()
	if v := os.Getenv("BACKSTAGE_TEST_OMARCHY"); v != "" {
		spec.Omarchy = v
	}
	if _, err := m.Create(ctx, name, spec); err != nil {
		t.Logf("create %s failed; cleanup will delete a partial record: %v", name, err)
		release()
		t.Fatalf("create %s (retained for diagnosis until cleanup): %v", name, err)
	}
	release()
}

func deleteStage(t *testing.T, ctx context.Context, m *machine.Manager, name string) {
	t.Helper()
	rec, err := m.Store.Load(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		t.Logf("cleanup load %s: %v", name, err)
		return
	}
	if err := m.Delete(ctx, rec); err != nil {
		t.Errorf("cleanup delete %s: %v", name, err)
	}
}

func writeParallelWorkspace(t *testing.T, ws, nameA, nameB string) {
	t.Helper()
	writeFile(t, filepath.Join(ws, "backstage.json"), fmt.Sprintf(`{
		"record": {"out": "recordings", "fps": 30},
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"vms": {
			"a": {"stage": %q},
			"b": {"stage": %q}
		}
	}`, nameA, nameB))
	scenes := []struct {
		name, vm, start, end string
	}{
		{"pa", "a", "initial", "ready-a"},
		{"ca", "a", "ready-a", ""},
		{"pb", "b", "initial", "ready-b"},
		{"cb", "b", "ready-b", ""},
	}
	for _, s := range scenes {
		end := ""
		if s.end != "" {
			end = fmt.Sprintf(`,"vm-end":{"snapshot":%q}`, s.end)
		}
		writeFile(t, filepath.Join(ws, "scenes", s.name+".json"), fmt.Sprintf(
			`{"name":%q,"layout":"solo","vm":%q,"vm-start":{"mode":"clean","snapshot":%q}%s,"steps":[{"action":"wait"}]}`,
			s.name, s.vm, s.start, end))
	}
}

func readSceneFacts(t *testing.T, ws, scene string) facts.Facts {
	t.Helper()
	got, err := facts.Read(facts.Path(filepath.Join(ws, "recordings", scene+".mp4")))
	if err != nil {
		t.Fatalf("facts %s: %v", scene, err)
	}
	return got
}

func logStageTimings(t *testing.T, m *machine.Manager, name string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(m.Store.Dir(name), "provision.log"))
	if err != nil {
		t.Logf("provision.log %s: %v", name, err)
		return
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, "timing ") {
			t.Logf("%s %s", name, line)
		}
	}
}

func parseFinalJobReport(stdout string) (jobReport, error) {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var rep jobReport
		if json.Unmarshal([]byte(line), &rep) != nil || rep.RunID == "" || len(rep.Jobs) == 0 {
			continue
		}
		return rep, nil
	}
	return jobReport{}, errors.New("no final JSON document")
}

type stampedProgress struct {
	at   time.Time
	line jobProgressLine
}

type runSpan struct {
	Job   string
	Scene string
	Stage string
	Start time.Time
	End   time.Time
}

func firstSceneStatus(evs []stampedProgress, scene, status string) time.Time {
	for _, ev := range evs {
		if ev.line.Params.Scene == scene && ev.line.Params.Status == status {
			return ev.at
		}
	}
	return time.Time{}
}

func runningSpans(evs []stampedProgress) []runSpan {
	open := map[string]stampedProgress{}
	var out []runSpan
	for _, ev := range evs {
		id := ev.line.Params.JobID
		if ev.line.Params.Status == jobRunning {
			if _, ok := open[id]; !ok {
				open[id] = ev
			}
			continue
		}
		start, ok := open[id]
		if !ok {
			continue
		}
		out = append(out, runSpan{
			Job: id, Scene: start.line.Params.Scene, Stage: start.line.Params.Stage,
			Start: start.at, End: ev.at,
		})
		delete(open, id)
	}
	return out
}

func stagesOverlap(spans []runSpan, stageA, stageB string) bool {
	var a, b []runSpan
	for _, s := range spans {
		switch s.Stage {
		case stageA:
			a = append(a, s)
		case stageB:
			b = append(b, s)
		}
	}
	for _, x := range a {
		for _, y := range b {
			if x.Start.Before(y.End) && y.Start.Before(x.End) {
				return true
			}
		}
	}
	return false
}

func formatStamped(evs []stampedProgress) string {
	var b strings.Builder
	for _, ev := range evs {
		fmt.Fprintf(&b, "\n  %s %s %s %s %s", ev.at.Format(time.RFC3339Nano), ev.line.Params.JobID, ev.line.Params.Scene, ev.line.Params.Status, ev.line.Params.Phase)
	}
	return b.String()
}

func formatSpans(spans []runSpan) string {
	var b strings.Builder
	for _, s := range spans {
		fmt.Fprintf(&b, "\n  %s %s %s [%s, %s]", s.Job, s.Stage, s.Scene, s.Start.Format(time.RFC3339Nano), s.End.Format(time.RFC3339Nano))
	}
	if b.Len() == 0 {
		return " (none)"
	}
	return b.String()
}

func pidsInGroup(pgid int) []int {
	if pgid <= 0 {
		return nil
	}
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var pids []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		g, err := syscall.Getpgid(pid)
		if err != nil || g != pgid {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

func fmtBytes(n uint64) string {
	return fmt.Sprintf("%.2f GiB", float64(n)/float64(1<<30))
}
