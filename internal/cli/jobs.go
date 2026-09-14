package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/This-Is-NPC/backstage/internal/engine"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/workspace"
)

const (
	jobQueued      = "queued"
	jobWaiting     = "waiting"
	jobRunning     = "running"
	jobOK          = "ok"
	jobFailed      = "failed"
	jobInterrupted = "interrupted"
	jobNotRun      = "not-run"

	phaseRunning = "running"
	phaseCatalog = "image-catalog"
	phaseBudget  = "host-budget"
	maxJobRuns   = 20
)

type jobLauncher func(ctx context.Context, spec jobSpec) (jobProc, error)

type jobProc interface {
	Wait() error
	Signal(os.Signal) error
}

type jobSpec struct {
	Path     string
	Step     workspace.PlanStep
	Opts     engine.Options
	Lock     *os.File
	Progress *os.File
	Log      *os.File
	LogPath  string
}

type jobState struct {
	id      string
	step    workspace.PlanStep
	status  string
	phase   string
	host    bool
	cpus    int
	memory  uint64
	started time.Time
	ended   time.Time
	log     string
	proc    jobProc
	err     error
}

type jobEvent struct {
	id     string
	status string
	phase  string
	err    error
}

type jobProgressLine struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  struct {
		JobID   string `json:"jobId"`
		Status  string `json:"status,omitempty"`
		Stage   string `json:"stage,omitempty"`
		Scene   string `json:"scene,omitempty"`
		Kind    string `json:"kind,omitempty"`
		Phase   string `json:"phase,omitempty"`
		Project string `json:"project,omitempty"`
		Log     string `json:"log,omitempty"`
	} `json:"params"`
}

type jobReport struct {
	RunID    string              `json:"run-id"`
	Jobs     []jobReportItem     `json:"jobs"`
	Saved    []savedSnap         `json:"saved"`
	Warnings []workspace.Warning `json:"warnings,omitempty"`
}

type jobReportItem struct {
	ID      string `json:"id"`
	Stage   string `json:"stage"`
	Scene   string `json:"scene"`
	Project string `json:"project"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Phase   string `json:"phase,omitempty"`
	Log     string `json:"log"`
	Started string `json:"started,omitempty"`
	Ended   string `json:"ended,omitempty"`
}

func (d *depsExec) schedule(plan *workspace.Plan, opts engine.Options, title string) error {
	if d.Out == nil {
		d.Out = os.Stdout
	}
	if !d.JSON {
		writeNamedPlan(d.Out, title, plan)
		for _, w := range plan.Warnings {
			fmt.Fprintf(d.Out, ">> warning: %s: %s\n", w.Path, w.Error)
		}
	} else {
		for _, w := range plan.Warnings {
			writeJSONWarning(d.Out, w)
		}
	}

	snaps := plan.AdoptSnapshots
	if len(snaps) == 0 && plan.AdoptSnapshot != "" {
		snaps = []string{plan.AdoptSnapshot}
	}
	if len(snaps) > 0 && opts.ConfirmAdopt != nil {
		label := snaps[0]
		if len(snaps) > 1 {
			label = strings.Join(snaps, " ")
		}
		if err := opts.ConfirmAdopt(label); err != nil {
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
	var lockByName map[string]*os.File
	if len(names) > 0 {
		if d.Lock != nil {
			release, err := d.Lock(names...)
			if err != nil {
				return err
			}
			defer release()
		} else {
			held, release, err := d.Store.LockHold(names...)
			if err != nil {
				return err
			}
			defer release()
			lockByName = map[string]*os.File{}
			for _, h := range held {
				lockByName[h.Name] = h.File
			}
		}
	}
	opts.ReservedStages = reserved

	before := snapshotImages(d.Store, plan.Stages)
	runID, runDir, runLock, err := d.createRunDir()
	if err != nil {
		return err
	}
	defer runLock.Close()
	if err := pruneJobRuns(d.jobsDir(), runID, maxJobRuns); err != nil {
		return err
	}

	avail, err := d.memAvail()
	if err != nil {
		return err
	}
	memBudget := memoryBudget(avail)
	cpus := d.cpus()
	kind := "play"
	if !opts.Record {
		kind = "rehearse"
	}

	jobs := make([]*jobState, 0, len(plan.Steps))
	for i, step := range plan.Steps {
		j := &jobState{
			id:     fmt.Sprintf("job-%d", i+1),
			step:   step,
			status: jobQueued,
			host:   step.Stage == "",
		}
		if !j.host {
			j.cpus, j.memory = d.stageSpec(step.Stage)
		}
		jobs = append(jobs, j)
	}

	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	launch := d.Launch
	if launch == nil {
		if d.Run != nil {
			launch = inProcessLauncher(d.Run)
		} else {
			launch = execLauncher
		}
	}

	var mu sync.Mutex
	var ran []string
	var failed string
	var failErr error
	var interruptedID string
	var interruptedBefore string
	stopAdmit := false
	events := make(chan jobEvent, len(jobs)*4)
	var reportOnce sync.Once
	printReport := func() {
		reportOnce.Do(func() {
			mu.Lock()
			defer mu.Unlock()
			d.writeFinal(jobs, kind, runID, ran, interruptedID, interruptedBefore, failed, failErr, savedSince(d.Store, plan.Stages, before), plan.Warnings)
		})
	}

	prevInterrupt := opts.OnInterrupt
	opts.OnInterrupt = func() {
		mu.Lock()
		if interruptedID == "" {
			for _, j := range jobs {
				if j.status == jobRunning || j.status == jobWaiting {
					interruptedID = j.step.ID
					break
				}
			}
		}
		mu.Unlock()
		printReport()
		if prevInterrupt != nil {
			prevInterrupt()
		}
	}

	emit := func(j *jobState) {
		d.writeTransition(j, kind)
	}

	setStatus := func(j *jobState, status, phase string) {
		j.status = status
		j.phase = phase
		emit(j)
	}

	active := func() int {
		n := 0
		for _, j := range jobs {
			if j.status == jobRunning || j.status == jobWaiting {
				n++
			}
		}
		return n
	}
	vmActive := func() bool {
		for _, j := range jobs {
			if !j.host && (j.status == jobRunning || j.status == jobWaiting) {
				return true
			}
		}
		return false
	}
	hostBusy := func() bool {
		for _, j := range jobs {
			if j.host && (j.status == jobRunning || j.status == jobWaiting) {
				return true
			}
		}
		return false
	}
	laneBusy := func(stage string) bool {
		if stage == "" {
			return false
		}
		for _, j := range jobs {
			if j.step.Stage == stage && (j.status == jobRunning || j.status == jobWaiting) {
				return true
			}
		}
		return false
	}
	holdsBudget := func(j *jobState) bool {
		if j.host {
			return false
		}
		switch j.status {
		case jobRunning:
			return true
		case jobWaiting:
			return j.phase != phaseBudget
		default:
			return false
		}
	}
	usedCPU := func() int {
		n := 0
		for _, j := range jobs {
			if holdsBudget(j) {
				n += j.cpus
			}
		}
		return n
	}
	usedMem := func() uint64 {
		var n uint64
		for _, j := range jobs {
			if holdsBudget(j) {
				n += j.memory
			}
		}
		return n
	}
	predsOK := func(j *jobState) bool {
		done := map[string]string{}
		for _, o := range jobs {
			done[o.step.ID] = o.status
		}
		for _, id := range j.step.Needs {
			st := done[id]
			if st != jobOK {
				return false
			}
		}
		return true
	}
	predFailed := func(j *jobState) bool {
		done := map[string]string{}
		for _, o := range jobs {
			done[o.step.ID] = o.status
		}
		for _, id := range j.step.Needs {
			switch done[id] {
			case jobFailed, jobInterrupted, jobNotRun:
				return true
			}
		}
		return false
	}
	budgetOK := func(j *jobState) bool {
		if j.host {
			return true
		}
		alone := j.cpus > cpus || j.memory > memBudget
		if alone {
			return !vmActive()
		}
		if usedCPU()+j.cpus > cpus {
			return false
		}
		if usedMem()+j.memory > memBudget {
			return false
		}
		return true
	}

	startJob := func(j *jobState) {
		logPath := filepath.Join(runDir, j.id+".log")
		logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			j.err = err
			j.status = jobFailed
			if failed == "" {
				failed = j.step.ID
				failErr = err
			}
			stopAdmit = true
			emit(j)
			return
		}
		j.log = logPath
		pr, pw, err := os.Pipe()
		if err != nil {
			_ = logf.Close()
			j.err = err
			j.status = jobFailed
			if failed == "" {
				failed = j.step.ID
				failErr = err
			}
			stopAdmit = true
			emit(j)
			return
		}
		j.started = time.Now()
		setStatus(j, jobRunning, phaseRunning)
		stepOpts := opts
		if !j.step.Requested {
			stepOpts.Adopt = false
			stepOpts.ConfirmAdopt = nil
		}
		spec := jobSpec{
			Path:     j.step.Path,
			Step:     j.step,
			Opts:     stepOpts,
			Progress: pw,
			Log:      logf,
			LogPath:  logPath,
		}
		if lockByName != nil && j.step.Stage != "" {
			spec.Lock = lockByName[j.step.Stage]
		}
		proc, err := launch(ctx, spec)
		if err != nil {
			_ = pw.Close()
			_ = logf.Close()
			_ = pr.Close()
			j.err = err
			j.ended = time.Now()
			j.status = jobFailed
			if failed == "" {
				failed = j.step.ID
				failErr = err
			}
			stopAdmit = true
			emit(j)
			return
		}
		j.proc = proc
		go readJobProgress(pr, j.id, events)
		go func(id string, p jobProc, log, w *os.File) {
			err := p.Wait()
			_ = w.Close()
			_ = log.Close()
			events <- jobEvent{id: id, err: err}
		}(j.id, proc, logf, pw)
	}

	admit := func() {
		if stopAdmit || ctx.Err() != nil {
			return
		}
		for _, j := range jobs {
			if j.status != jobQueued {
				continue
			}
			if predFailed(j) {
				j.status = jobNotRun
				emit(j)
				continue
			}
			if !predsOK(j) {
				continue
			}
			if d.Jobs > 0 && active() >= d.Jobs {
				return
			}
			if j.host {
				if vmActive() || hostBusy() {
					continue
				}
			} else {
				if hostBusy() {
					continue
				}
				if laneBusy(j.step.Stage) {
					continue
				}
				if !budgetOK(j) {
					if j.status == jobQueued && (usedCPU() > 0 || usedMem() > 0 || vmActive()) {
						setStatus(j, jobWaiting, phaseBudget)
					}
					continue
				}
				if j.status == jobWaiting && j.phase == phaseBudget {
					setStatus(j, jobQueued, "")
				}
			}
			startJob(j)
		}
	}

	byID := map[string]*jobState{}
	for _, j := range jobs {
		byID[j.id] = j
		emit(j)
	}

	admit()
	pending := func() bool {
		for _, j := range jobs {
			if j.status == jobRunning || j.status == jobWaiting {
				return true
			}
			if j.status == jobQueued && !stopAdmit && ctx.Err() == nil {
				return true
			}
		}
		return false
	}

	finishJob := func(j *jobState, err error) {
		j.ended = time.Now()
		j.proc = nil
		if childLooksInterrupted(err) && ctx.Err() == nil {
			waitParentInterrupt(ctx, parentInterruptGrace)
		}
		parentStop := err != nil && (ctx.Err() != nil || interruptedID == j.step.ID || takeInterrupted(err, ctx))
		if parentStop {
			if interruptedID == "" {
				interruptedID = j.step.ID
			}
			if failErr == nil {
				failErr = interrupted(err)
			}
			j.status = jobInterrupted
			j.err = failErr
			stopAdmit = true
			emit(j)
			return
		}
		if isExit130(err) {
			j.status = jobInterrupted
			j.err = err
			if interruptedID == "" {
				interruptedID = j.step.ID
			}
			if failErr == nil {
				failErr = err
			}
			stopAdmit = true
			emit(j)
			return
		}
		if err != nil {
			j.err = err
			j.status = jobFailed
			if failed == "" {
				failed = j.step.ID
				failErr = err
			}
			stopAdmit = true
			if !d.JSON {
				fmt.Fprintf(d.Out, ">> %s failed: %v\n   %s\n", j.id, err, j.log)
			}
			emit(j)
			return
		}
		j.status = jobOK
		ran = append(ran, j.step.ID)
		emit(j)
		if ctx.Err() != nil && interruptedBefore == "" {
			for _, n := range jobs {
				if n.status == jobQueued {
					interruptedBefore = n.step.ID
					if failErr == nil {
						failErr = interrupted(ctx.Err())
					}
					stopAdmit = true
					break
				}
			}
		}
	}

	requeueBudget := func() {
		for _, q := range jobs {
			if q.status == jobWaiting && q.phase == phaseBudget {
				q.status = jobQueued
				q.phase = ""
			}
		}
	}
	signaled := false
	handle := func(ev jobEvent) {
		j := byID[ev.id]
		if j == nil {
			return
		}
		if ev.status != "" && (j.status == jobRunning || j.status == jobWaiting) {
			if ev.status == jobWaiting || ev.status == jobRunning {
				setStatus(j, ev.status, ev.phase)
			}
			if !stopAdmit && ctx.Err() == nil {
				requeueBudget()
				admit()
			}
			return
		}
		if ev.status == "" {
			finishJob(j, ev.err)
			if !stopAdmit {
				requeueBudget()
				admit()
			}
		}
	}
	for pending() {
		if ctx.Err() != nil && !signaled {
			signaled = true
			stopAdmit = true
			if failErr == nil {
				failErr = interrupted(ctx.Err())
			}
			for _, j := range jobs {
				if j.proc != nil {
					_ = j.proc.Signal(os.Interrupt)
					_ = j.proc.Signal(os.Interrupt)
				}
			}
		}
		if !pending() {
			break
		}
		if signaled {
			handle(<-events)
			continue
		}
		select {
		case ev := <-events:
			handle(ev)
		case <-ctx.Done():
		}
	}

	for _, j := range jobs {
		if j.status == jobQueued || j.status == jobWaiting {
			j.status = jobNotRun
			emit(j)
		}
	}
	if ctx.Err() != nil && failErr == nil {
		failErr = interrupted(ctx.Err())
	}
	if interruptedID == "" && interruptedBefore == "" && failErr != nil && ExitStatus(failErr) == 130 {
		for _, j := range jobs {
			if j.status == jobNotRun {
				interruptedBefore = j.step.ID
				break
			}
		}
	}
	printReport()
	if failErr != nil {
		return failErr
	}
	return nil
}

func readJobProgress(r io.ReadCloser, id string, events chan jobEvent) {
	defer r.Close()
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		var line jobProgressLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if line.Method != "job.progress" {
			continue
		}
		st := line.Params.Status
		if st != jobWaiting && st != jobRunning {
			continue
		}
		events <- jobEvent{id: id, status: st, phase: line.Params.Phase}
	}
}

func (d *depsExec) writeTransition(j *jobState, kind string) {
	if d.JSON {
		line := jobProgressLine{JSONRPC: "2.0", Method: "job.progress"}
		line.Params.JobID = j.id
		line.Params.Status = j.status
		line.Params.Stage = j.step.Stage
		line.Params.Scene = j.step.Scene
		line.Params.Kind = kind
		line.Params.Phase = j.phase
		line.Params.Project = j.step.Project
		line.Params.Log = j.log
		body, err := json.Marshal(line)
		if err == nil {
			fmt.Fprintf(d.Out, "%s\n", body)
		}
		return
	}
	switch j.status {
	case jobQueued:
		return
	case jobWaiting:
		fmt.Fprintf(d.Out, ">> %s %s %s waiting %s\n", j.id, dash(j.step.Stage), j.step.Scene, j.phase)
	case jobRunning:
		fmt.Fprintf(d.Out, ">> %s %s %s running\n", j.id, dash(j.step.Stage), j.step.Scene)
	case jobOK:
		fmt.Fprintf(d.Out, ">> %s ok %s\n", j.id, fmtDuration(j.ended.Sub(j.started)))
	}
}

func (d *depsExec) writeFinal(jobs []*jobState, kind, runID string, ran []string, interruptedID, interruptedBefore, failed string, failErr error, saved []savedSnap, warnings []workspace.Warning) {
	if d.JSON {
		rep := jobReport{RunID: runID, Warnings: warnings}
		for _, j := range jobs {
			item := jobReportItem{
				ID: j.id, Stage: j.step.Stage, Scene: j.step.Scene,
				Project: j.step.Project, Kind: kind, Status: j.status,
				Phase: j.phase, Log: j.log,
			}
			if !j.started.IsZero() {
				item.Started = j.started.UTC().Format(time.RFC3339Nano)
			}
			if !j.ended.IsZero() {
				item.Ended = j.ended.UTC().Format(time.RFC3339Nano)
			}
			rep.Jobs = append(rep.Jobs, item)
		}
		rep.Saved = saved
		body, err := json.Marshal(rep)
		if err == nil {
			fmt.Fprintf(d.Out, "%s\n", body)
		}
		return
	}
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
	for _, j := range jobs {
		if j.step.ID == failed || j.step.ID == interruptedID {
			continue
		}
		if j.status == jobOK || j.status == jobFailed || j.status == jobInterrupted {
			continue
		}
		fmt.Fprintf(d.Out, ">> not run %s\n", j.step.ID)
	}
	for _, s := range saved {
		fmt.Fprintf(d.Out, ">> saved %s %s %s\n", s.stage, s.snapshot, s.image)
	}
}

func writeJSONWarning(out io.Writer, w workspace.Warning) {
	line := struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			Path  string `json:"path"`
			Error string `json:"error"`
		} `json:"params"`
	}{JSONRPC: "2.0", Method: "plan.warning"}
	line.Params.Path = w.Path
	line.Params.Error = w.Error
	body, err := json.Marshal(line)
	if err == nil {
		fmt.Fprintf(out, "%s\n", body)
	}
}

func writeNamedPlan(out io.Writer, title string, plan *workspace.Plan) {
	fmt.Fprintf(out, ">> %s\n", title)
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

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func fmtDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Round(time.Second).Seconds())
	h := s / 3600
	m := (s % 3600) / 60
	sec := s % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm%ds", h, m, sec)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, sec)
	}
	return fmt.Sprintf("%ds", sec)
}

func isExit130(err error) bool {
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == interruptExitCode {
		return true
	}
	return false
}

func childLooksInterrupted(err error) bool {
	if isExit130(err) {
		return true
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	status, ok := ee.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return false
	}
	switch status.Signal() {
	case syscall.SIGINT, syscall.SIGTERM:
		return true
	default:
		return false
	}
}

func (d *depsExec) jobsDir() string {
	if d.JobsDir != "" {
		return d.JobsDir
	}
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "backstage", "jobs")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "backstage-jobs")
	}
	return filepath.Join(home, ".local", "state", "backstage", "jobs")
}

func (d *depsExec) memAvail() (uint64, error) {
	if d.MemAvailable != nil {
		return d.MemAvailable()
	}
	return readMemAvailable()
}

func (d *depsExec) cpus() int {
	if d.NumCPU != nil {
		return d.NumCPU()
	}
	return numCPU()
}

func (d *depsExec) stageSpec(stage string) (int, uint64) {
	def := machine.DefaultSpec()
	if d.Store == nil || stage == "" {
		return def.CPUs, def.Memory
	}
	rec, err := d.Store.Load(stage)
	if err != nil || rec == nil {
		return def.CPUs, def.Memory
	}
	return rec.Spec.CPUs, rec.Spec.Memory
}

func (d *depsExec) createRunDir() (runID, runDir string, lock *os.File, err error) {
	now := time.Now()
	if d.now != nil {
		now = d.now()
	}
	stamp := now.UTC().Format("20060102T150405Z")
	jobsDir := d.jobsDir()
	if err := os.MkdirAll(jobsDir, 0o700); err != nil {
		return "", "", nil, err
	}
	for i := 0; i < 32; i++ {
		suffix, err := d.nextRunSuffix()
		if err != nil {
			return "", "", nil, err
		}
		runID = stamp + "-" + suffix
		runDir = filepath.Join(jobsDir, runID)
		if err := os.Mkdir(runDir, 0o700); err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", "", nil, err
		}
		lock, err = lockRunDir(runDir)
		if err != nil {
			_ = os.RemoveAll(runDir)
			return "", "", nil, err
		}
		return runID, runDir, lock, nil
	}
	return "", "", nil, fmt.Errorf("could not allocate a job run directory")
}

func (d *depsExec) nextRunSuffix() (string, error) {
	if d.runSuffix != nil {
		return d.runSuffix()
	}
	return randomRunSuffix()
}

func randomRunSuffix() (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func lockRunDir(runDir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(runDir, "run.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func runDirLocked(runDir string) bool {
	f, err := os.OpenFile(filepath.Join(runDir, "run.lock"), os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	return err != nil
}

func jobRunStamp(name string) string {
	if len(name) > 17 && name[16] == '-' && strings.HasSuffix(name[:16], "Z") {
		return name[:16]
	}
	return name
}

func pruneJobRuns(root, current string, keep int) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool {
		si, sj := jobRunStamp(names[i]), jobRunStamp(names[j])
		if si != sj {
			return si > sj
		}
		return names[i] > names[j]
	})
	if keep < 1 {
		keep = 1
	}
	newest := map[string]bool{}
	for i, name := range names {
		if i >= keep {
			break
		}
		newest[name] = true
	}
	for _, name := range names {
		if name == current || newest[name] {
			continue
		}
		dir := filepath.Join(root, name)
		if runDirLocked(dir) {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	return nil
}

func inProcessLauncher(run sceneRun) jobLauncher {
	return func(ctx context.Context, spec jobSpec) (jobProc, error) {
		return doneProc{err: run(spec.Path, spec.Opts)}, nil
	}
}

type doneProc struct{ err error }

func (p doneProc) Wait() error { return p.err }

func (p doneProc) Signal(os.Signal) error { return nil }

func execLauncher(_ context.Context, spec jobSpec) (jobProc, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	verb := "play"
	if !spec.Opts.Record {
		verb = "rehearse"
	}
	args := []string{verb, spec.Path}
	extra := childExtraFiles(spec)
	if spec.Lock != nil && spec.Step.Stage != "" {
		args = append(args, "--internal-reserved-stage", spec.Step.Stage, "--internal-reserved-fd", "3")
	}
	if spec.Progress != nil {
		args = append(args, "--internal-progress-fd", strconv.Itoa(3+lockExtraCount(spec)))
	}
	if spec.Opts.Adopt {
		args = append(args, "--adopt", "--internal-adopt-confirmed")
	}
	if spec.Opts.ReplaceState {
		args = append(args, "--replace-state")
	}
	cmd := exec.Command(exe, args...)
	null, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	cmd.Stdin = null
	cmd.Stdout = spec.Log
	cmd.Stderr = spec.Log
	cmd.ExtraFiles = extra
	if err := cmd.Start(); err != nil {
		_ = null.Close()
		return nil, err
	}
	return &execProc{cmd: cmd, null: null}, nil
}

func childExtraFiles(spec jobSpec) []*os.File {
	var extra []*os.File
	if spec.Lock != nil && spec.Step.Stage != "" {
		extra = append(extra, spec.Lock)
	}
	if spec.Progress != nil {
		extra = append(extra, spec.Progress)
	}
	return extra
}

func lockExtraCount(spec jobSpec) int {
	if spec.Lock != nil && spec.Step.Stage != "" {
		return 1
	}
	return 0
}

type execProc struct {
	cmd  *exec.Cmd
	null *os.File
}

func (p *execProc) Wait() error {
	err := p.cmd.Wait()
	_ = p.null.Close()
	return err
}

func (p *execProc) Signal(sig os.Signal) error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Signal(sig)
}

type signalProc struct {
	done chan error
	mu   sync.Mutex
	sigs int
}

func (p *signalProc) Wait() error {
	return <-p.done
}

func (p *signalProc) Signal(os.Signal) error {
	p.mu.Lock()
	p.sigs++
	p.mu.Unlock()
	return nil
}

func writeProgress(w io.Writer, status, phase string) {
	if w == nil {
		return
	}
	line := jobProgressLine{JSONRPC: "2.0", Method: "job.progress"}
	line.Params.Status = status
	line.Params.Phase = phase
	body, err := json.Marshal(line)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "%s\n", body)
}

func attachCatalogProgress(fd int) {
	if fd < 0 {
		return
	}
	f := holdInternalFD(fd, "progress")
	machine.SetCatalogWaitNotify(func(waiting bool) {
		if waiting {
			writeProgress(f, jobWaiting, phaseCatalog)
			return
		}
		writeProgress(f, jobRunning, phaseRunning)
	})
}

func applyInternalChild(opts *engine.Options, stage string, lockFD, progressFD int, adoptConfirmed bool) error {
	if stage != "" || lockFD >= 0 {
		if err := verifyReservedStage(nil, stage, lockFD); err != nil {
			return err
		}
		holdInternalFD(lockFD, stage)
		if opts.ReservedStages == nil {
			opts.ReservedStages = map[string]bool{}
		}
		opts.ReservedStages[stage] = true
	}
	if progressFD >= 0 {
		attachCatalogProgress(progressFD)
	}
	if adoptConfirmed {
		opts.Adopt = true
		opts.ConfirmAdopt = func(string) error { return nil }
	}
	return nil
}
