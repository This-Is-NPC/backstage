package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/guest"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/pane"
	"github.com/This-Is-NPC/backstage/internal/prompter"
	"github.com/This-Is-NPC/backstage/internal/recorder"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/stage"
	"github.com/This-Is-NPC/backstage/internal/take"
)

// Options tune a run. Record off + Speed < 1 is a rehearsal (dry-run).
type Options struct {
	Context context.Context
	// ReservedStages are held by a surrounding production for its whole run.
	ReservedStages map[string]bool
	Record         bool
	Speed          float64 // delay multiplier; 1 = real time, smaller = faster rehearsal

	// OutPath overrides where the recording is written. Empty uses the default
	// <project>/<record.out>/<scene>.mp4. Used by the production pipeline to
	// record each scene to its own clip.
	OutPath string
	// ShowStaging starts recording before the stage is built, so the stage
	// montage appears in the video. Default (false) starts after the stage is
	// ready, hiding the setup.
	ShowStaging bool
	// OnInterrupt runs after recorder/popup/stage cleanup but before the interrupt
	// handler exits. Production uses this to remove its segment work directory.
	OnInterrupt func()
	// Version is the Backstage binary version written into clip facts.
	Version string
	// Adopt lets play replace a snapshot that has no origin, after ConfirmAdopt.
	Adopt bool
	// ReplaceState lets a rehearsal replace a snapshot a recording made.
	ReplaceState bool
	// ConfirmAdopt asks the operator to type the snapshot name. Used with Adopt.
	ConfirmAdopt func(snapshot string) error
}

// Engine runs a scene over the stage/recorder/prompter/pane drivers.
type Engine struct {
	Managed     *machine.Manager
	ManagedName string
	Project     *scene.Project
	Stager      stage.Stager
	Rec         recorder.Recorder
	Prompt      prompter.Prompter

	Speed float64
	// PaneDriver overrides the default tmux driver. A vm stage sets it,
	// because on that stage a target names a computer and not a pane.
	PaneDriver    pane.Driver
	pane          pane.Driver
	rehearsing    bool
	cmdGuard      *CommandGuard
	startImage    string
	startSnapshot string
	inputsSHA256  string
	takeSess      atomic.Pointer[take.Session]
	stateSaved    bool
	capturing     atomic.Bool
	captureMu     sync.Mutex
	captureDone   chan struct{}
	managedRec    *machine.Record
	leafProject   string
	endFacts      endFactsWrite
	timings       facts.Timings
}

// ErrCaptureFailed is returned when vm-end cannot commit a snapshot.
var ErrCaptureFailed = errors.New("vm-end capture failed")

type endFactsWrite struct {
	result  string
	end     *facts.EndState
	written bool
}

func (w endFactsWrite) publishedOK() bool {
	return w.written && w.result == facts.ResultOK && w.end != nil && w.end.Snapshot != "" && w.end.Image != "" && w.end.Status == ""
}

var beginManaged = func(m *machine.Manager, ctx context.Context, r *machine.Record, mode, snapshot, after, project string, recording bool) (*guest.Guest, error) {
	return m.Begin(ctx, r, mode, snapshot, after, project, recording)
}

var finishManaged = func(m *machine.Manager, r *machine.Record, g *guest.Guest, project, name string, recording bool) error {
	return m.Finish(r, g, project, name, recording)
}

var replaceTakeState = func(m *machine.Manager, ctx context.Context, r *machine.Record, name string, origin machine.SnapshotOrigin, adopt bool) (machine.ReplaceResult, error) {
	return m.ReplaceSnapshot(ctx, r, name, origin, adopt)
}

var captureWaitTimeout = 30 * time.Second

var onWaitCapture = func() {}

type recordingGuest interface {
	RecordingGuest() (*guest.Guest, string)
}

type sessionTimer interface {
	SessionTimings() (map[string]float64, *float64)
}

// New builds an Engine with the default Hyprland/gpu drivers for a project.
func New(p *scene.Project) *Engine {
	return &Engine{
		Project: p,
		Stager:  &stage.Hypr{},
		Rec:     recorder.NewGPU(p.Record.Monitor, p.Record.FPS),
		Prompt:  &prompter.Hypr{},
		Speed:   1,
	}
}

// NewForScene builds an Engine with the drivers the scene asks for.
//
// A scene with no `vm` gets the ordinary stage: a tmux layout in a window on
// this machine, recorded off a monitor. A scene that names a vm gets all four
// drivers swapped at once -- stage, recorder, panes, and in time the prompter
// -- because the machine being filmed changes every one of them together. That
// is why they are chosen here and not each in its own place: a run with a guest
// stage and a host recorder would record this desktop while typing on another.
func NewForScene(p *scene.Project, s *scene.Scene) (*Engine, error) {
	if s.Type == "visual" {
		return nil, fmt.Errorf("visual scenes use backstage render or preview, not recording commands")
	}
	e := New(p)
	if s == nil || s.VM == "" {
		return e, nil
	}
	cfg, ok := p.VMs[s.VM]
	if !ok {
		return nil, fmt.Errorf("scene %q runs on vm %q, which backstage.json does not declare",
			s.Name, s.VM)
	}
	if err := s.ValidateVMStart(p); err != nil {
		return nil, err
	}
	var box *guest.Guest
	if cfg.Stage != "" {
		manager, err := machine.New()
		if err != nil {
			return nil, err
		}
		r, err := manager.Store.Load(cfg.Stage)
		if err != nil {
			return nil, err
		}
		box, err = manager.Guest(r)
		if err != nil {
			return nil, err
		}
		e.Managed = manager
		e.ManagedName = cfg.Stage
		cfg.Domain = box.Domain
		cfg.User = box.User
		cfg.URI = box.URI
	}
	if cfg.Domain == "" {
		return nil, fmt.Errorf("vm %q names no libvirt domain", s.VM)
	}
	if cfg.User == "" {
		return nil, fmt.Errorf("vm %q names no user; the stage films that account's session "+
			"and connects as it", s.VM)
	}
	if box == nil {
		box = guest.New(cfg.Domain, cfg.User, cfg.Admin, cfg.Key, cfg.URI)
	}
	box.Open = cfg.Open
	box.Language = cfg.Language
	e.Stager = stage.NewVM(box)

	// The scene wins over the vm, because the scene is what knows whether it is
	// about to kill the session it is being recorded from.
	which := cfg.Recorder
	if s.Recorder != "" {
		which = s.Recorder
	}
	switch which {
	case "", "inside":
		e.Rec = recorder.NewWF(box, p.Record.FPS)
	case "framebuffer":
		e.Rec = recorder.NewFramebuffer(cfg.Domain, cfg.URI, p.Record.FPS)
	default:
		return nil, fmt.Errorf("scene %q asks for recorder %q; say `inside` or `framebuffer`",
			s.Name, which)
	}
	e.PaneDriver = pane.NewGuest(box)
	return e, nil
}

// Run stages the scene's layout, optionally records, executes every step, then
// stops. The layout is assumed validated against the project (scene.Validate).
func (e *Engine) Run(s *scene.Scene, opts Options) (runErr error) {
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.Speed = opts.Speed
	if e.Speed <= 0 {
		e.Speed = 1
	}
	e.startImage = ""
	e.startSnapshot = ""
	e.inputsSHA256 = ""
	e.stateSaved = false
	e.timings = facts.Timings{}
	e.managedRec = nil
	e.leafProject = ""
	e.endFacts = endFactsWrite{}
	prevRehearsing := e.rehearsing
	e.rehearsing = !opts.Record
	defer func() { e.rehearsing = prevRehearsing }()

	layout, ok := e.Project.Layouts[s.LayoutName()]
	if !ok {
		return fmt.Errorf("layout %q not in config", s.LayoutName())
	}

	// Preflight the prompter/host before any recording. Any run that WILL record a
	// Hypr-driven overlay must fail fast, not finalize an overlay-less video. The
	// readiness check matches what the scene actually needs:
	//   - a dialog step opens the prompter terminal → needs hyprctl AND the terminal
	//     (full Preflight);
	//   - a live transition step records a prop over the compositor overlay but never
	//     opens the terminal → needs hyprctl only (PreflightHypr), so it must not
	//     fail on a host missing the terminal.
	// Only fail-fast when actually recording: a rehearse/dry-run (Record==false)
	// produces no video, so it must not require hyprctl/terminal on a non-Hypr host.
	if opts.Record && e.Prompt != nil {
		switch {
		case e.hasDialogStep(s):
			if err := e.Prompt.Preflight(e.Project.Term); err != nil {
				return err
			}
		case e.recordsHyprOverlay(s):
			if err := e.Prompt.PreflightHypr(); err != nil {
				return err
			}
		}
	}

	var recMu sync.Mutex
	recArmed := false
	recStopped := false
	// When the recorder became warm, so a take can be weighed against the
	// window it was filmed in. See checkTake.
	var recArmedAt time.Time
	stopRec := func() error {
		recMu.Lock()
		if !opts.Record || !recArmed || recStopped {
			recMu.Unlock()
			return nil
		}
		recStopped = true
		recMu.Unlock()
		fmt.Println(">> stop recording")
		_, err := e.Rec.Stop()
		return err
	}
	// Cancel active command starts, stop the recorder, and tear down overlays on
	// interrupt, then exit.
	// Shared with production.recordLiveTransition via InterruptGuard so the
	// command-cancel + recorder-stop-once + signal-exit pattern can't drift between
	// the two sites.
	// (recArmed/recStopped stay under recMu here because startRec sets them
	// concurrently; the guard provides the signal handling and exit.)
	//
	// The guard owns command cancellation and recorder Stop before running this
	// onInterrupt, so the closure must not touch the recorder or the guard itself --
	// it only does overlay and caller cleanup. Not referencing the outer guard also
	// removes the construction-window nil-deref the closure used to risk.
	prevCmdGuard := e.cmdGuard
	cmdGuard := &CommandGuard{}
	e.cmdGuard = cmdGuard
	defer func() { e.cmdGuard = prevCmdGuard }()

	e.takeSess.Store(nil)
	// Registered before the interrupt guard so the recorder stops first.
	defer func() { e.finishTakeSession(&runErr) }()

	guard := newInterruptGuard(
		func() error {
			cancel()
			cmdGuard.Interrupt()
			return stopRec()
		},
		func() { e.cleanupOnInterrupt(opts.OnInterrupt) },
	)
	defer func() {
		if err := guard.Stop(); err != nil {
			runErr = errors.Join(runErr, err)
		}
		guard.Release()
	}()

	if e.Managed != nil {
		if !opts.ReservedStages[e.ManagedName] {
			release, err := e.Managed.Store.LockMany(e.ManagedName)
			if err != nil {
				return err
			}
			defer release()
		}
		r, err := e.Managed.Store.Load(e.ManagedName)
		if err != nil {
			return err
		}
		project, err := filepath.Abs(e.Project.Dir)
		if err != nil {
			return err
		}
		if resolved, err := filepath.EvalSymlinks(project); err == nil {
			project = resolved
		}
		e.leafProject = project
		// reuse and continue keep the image the take started on. clean
		// overwrites this with the restored snapshot after Begin.
		e.startImage = r.Source.Image
		if err := e.checkVMEnd(r, s, project, opts); err != nil {
			return err
		}
		snapshot, after := "", ""
		if s.VMStart != nil {
			snapshot = s.VMStart.Snapshot
			after = s.VMStart.After
		}
		g, err := beginManaged(e.Managed, ctx, r, s.VMStartMode(), snapshot, after, project, opts.Record)
		e.mergeStart(e.Managed.StartTimes)
		if err != nil {
			return err
		}
		if vm, ok := e.Stager.(*stage.VM); ok {
			g.Open = vm.Guest.Open
			g.Language = vm.Guest.Language
			*vm.Guest = *g
			vm.Continue = s.VMStartMode() == "continue"
		}
		if s.VMStartMode() == "clean" {
			e.startSnapshot = snapshot
			if e.startSnapshot == "" {
				e.startSnapshot = "initial"
			}
			e.startImage = r.Snapshots[e.startSnapshot]
			if e.startImage == "" {
				e.startImage = r.Source.Image
			}
		}
		e.managedRec = r
		defer func() {
			if runErr == nil && !e.stateSaved {
				runErr = finishManaged(e.Managed, r, g, project, s.Name, opts.Record)
			}
		}()
	}
	if opts.Record || s.VMEnd != nil {
		digest, err := scene.InputsDigest(e.Project, s, scene.DigestOptions{
			Speed:       e.Speed,
			ShowStaging: opts.ShowStaging,
			StartState:  scene.StartStateToken(s, e.startImage),
		})
		if err != nil {
			return err
		}
		e.inputsSHA256 = digest
	}
	if s.VMStartMode() != "continue" {
		if err := e.runHooks(s); err != nil {
			return err
		}
	}

	out := opts.OutPath
	prepareOut := func() (string, error) {
		if out != "" {
			return out, nil
		}
		if !opts.Record {
			return "", nil
		}
		if sess := e.takeSess.Load(); sess != nil {
			return sess.Clip(), nil
		}
		pending, err := take.BeginContext(ctx, e.Project, s.Name)
		if err != nil {
			return "", err
		}
		e.takeSess.Store(pending)
		out = pending.Clip()
		return out, nil
	}
	startRec := func() error {
		if !opts.Record {
			return nil
		}
		path, err := prepareOut()
		if err != nil {
			return err
		}
		recMu.Lock()
		recArmed = true
		recStopped = false
		recMu.Unlock()
		fmt.Println(">> start recording")
		if err := e.Rec.Start(path); err != nil {
			return err
		}
		recMu.Lock()
		recArmedAt = time.Now()
		recMu.Unlock()
		return nil
	}

	usesStage := len(layout.Panes) > 0

	// ShowStaging: capture the stage montage too (record before staging).
	if opts.ShowStaging && e.Managed == nil && s.VM == "" {
		if err := startRec(); err != nil {
			return err
		}
	}

	if usesStage || s.VM != "" {
		fmt.Printf(">> stage layout: %s\n", s.LayoutName())
		m, err := e.Stager.Setup(layout, e.Project)
		if err != nil {
			return err
		}
		if timed, ok := e.Stager.(sessionTimer); ok {
			e.mergeSession(timed.SessionTimings())
			e.logSessionTimings()
		}
		if e.PaneDriver != nil {
			e.pane = e.PaneDriver
		} else {
			e.pane = pane.NewTmux(m)
		}
		e.sleep(stageWarm)
	} else {
		fmt.Printf(">> stage layout: %s (none)\n", s.LayoutName())
		e.pane = nil
	}

	// Default: start after the stage is ready, hiding the setup.
	if !opts.ShowStaging || s.VM != "" {
		if err := startRec(); err != nil {
			return err
		}
	}

	// Continue-on-error is intentional: for a live recorder a partial take beats
	// a discarded one, so a failed step is logged and joined into runErr (surfaced
	// to the caller) rather than aborting the remaining steps.
	for i, st := range s.Steps {
		if err := e.runStep(i, st); err != nil {
			fmt.Fprintf(os.Stderr, "   !! step %d: %v\n", i+1, err)
			runErr = errors.Join(runErr, fmt.Errorf("step %d: %w", i+1, err))
		}
	}

	e.sleep(endWait)
	if opts.Record {
		recMu.Lock()
		window := time.Since(recArmedAt)
		recMu.Unlock()
		if err := stopRec(); err != nil {
			return err
		}
		// Joined and not returned: the take is on disk and a short one is still
		// worth keeping and looking at, the same reason a failed step does not
		// abandon the rest of the scene. What must not happen is finishing quietly.
		stepsFailed := runErr != nil
		if err := checkTake(out, window); err != nil {
			fmt.Fprintf(os.Stderr, "   !! %v\n", err)
			runErr = errors.Join(runErr, err)
		}
		result := facts.ResultOK
		switch {
		case stepsFailed:
			result = facts.ResultStepsFailed
		case runErr != nil:
			result = facts.ResultShort
		}
		if err := e.writeClipFacts(out, s, opts.Version, result, nil); err != nil {
			return errors.Join(runErr, err)
		}
		var captureErr error
		if result == facts.ResultOK && s.VMEnd != nil {
			if err := e.saveEndState(ctx, s, out, opts); err != nil {
				captureErr = err
				runErr = errors.Join(runErr, err)
			}
		}
		publish := result == facts.ResultOK && captureErr == nil
		if s.VMEnd != nil && (errors.Is(runErr, ErrCaptureFailed) || !e.endFacts.publishedOK()) {
			publish = false
		}
		if sess := e.takeSess.Load(); sess != nil && !sess.Finished() {
			if !publish {
				if !e.endFacts.written {
					_ = os.Chmod(filepath.Dir(sess.Clip()), 0o700)
				}
				path, err := sess.KeepAttempt()
				if err != nil {
					return errors.Join(runErr, err)
				}
				if err := e.retryEndFacts(sess.Clip(), s, opts.Version); err != nil {
					runErr = errors.Join(runErr, err)
				}
				fmt.Printf(">> done. %s  (stage open — backstage kill)\n", path)
			} else {
				pub, err := sess.Publish(ctx)
				if err != nil {
					return errors.Join(runErr, err)
				}
				fmt.Printf(">> done. %s  (stage open — backstage kill)\n", pub.StableClip)
			}
		} else if sess == nil || !sess.Finished() {
			fmt.Printf(">> done. %s  (stage open — backstage kill)\n", out)
		}
	} else {
		if runErr == nil && s.VMEnd != nil {
			if err := e.saveEndState(ctx, s, "", opts); err != nil {
				runErr = err
			}
		}
		fmt.Println(">> rehearsal done (no recording).  (stage open — backstage kill)")
	}
	return runErr
}

func (e *Engine) writeClipFacts(clip string, s *scene.Scene, version, result string, end *facts.EndState) error {
	f := facts.Facts{
		Backstage:    version,
		Result:       result,
		Made:         time.Now().Format(time.RFC3339),
		InputsSHA256: e.inputsSHA256,
	}
	if src, ok := e.Stager.(recordingGuest); ok {
		g, omarchy := src.RecordingGuest()
		if omarchy == "" {
			return fmt.Errorf("the stage never read the guest's Omarchy version")
		}
		if g != nil {
			f.Stage = g.StageName
			f.Origin = g.Origin
			f.StartMode = g.StartMode
			f.Snapshot = g.Snapshot
			f.ISOVersion = g.ISOVersion
			f.ISOChecksum = g.ISOChecksum
			f.Recipe = g.Recipe
			f.Domain = g.Domain
			f.User = g.User
			f.Address = g.Address
		}
		f.Omarchy = omarchy
	}
	if s.VMStartMode() == "clean" {
		snap := e.startSnapshot
		if snap == "" && s.VMStart != nil && s.VMStart.Snapshot != "" {
			snap = s.VMStart.Snapshot
		}
		if snap == "" {
			snap = "initial"
		}
		f.StartState = &facts.StartState{Snapshot: snap}
		f.StartImage = e.startImage
	}
	f.EndState = end
	if !e.timings.Empty() {
		t := e.timings
		if len(t.StagePhases) > 0 {
			t.StagePhases = maps.Clone(t.StagePhases)
		}
		f.Timings = &t
	}
	return facts.Write(facts.Path(clip), f)
}

func (e *Engine) mergeStart(t machine.StartTimes) {
	e.timings.RestoreStopSeconds = t.RestoreStopSeconds
	e.timings.RestoreActivateSeconds = t.RestoreActivateSeconds
	e.timings.BootSeconds = t.BootSeconds
}

func (e *Engine) mergeSession(phases map[string]float64, session *float64) {
	if len(phases) > 0 {
		e.timings.StagePhases = maps.Clone(phases)
	}
	e.timings.SessionSeconds = session
}

func (e *Engine) mergeCapture(r machine.ReplaceResult) {
	if r.ShutdownSeconds != nil {
		e.timings.ShutdownSeconds = r.ShutdownSeconds
	}
	if r.CaptureSeconds != nil {
		e.timings.CaptureSeconds = r.CaptureSeconds
	}
	if r.CaptureBytes != nil {
		e.timings.CaptureBytes = r.CaptureBytes
	}
	if r.CaptureApparentBytes != nil {
		e.timings.CaptureApparentBytes = r.CaptureApparentBytes
	}
}

func (e *Engine) logSessionTimings() {
	if e.Managed == nil || e.managedRec == nil {
		return
	}
	if e.timings.SessionSeconds != nil {
		e.Managed.LogTiming(e.managedRec, "session-seconds", *e.timings.SessionSeconds)
	}
	names := make([]string, 0, len(e.timings.StagePhases))
	for name := range e.timings.StagePhases {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		e.Managed.LogTiming(e.managedRec, "stage-phases."+name, e.timings.StagePhases[name])
	}
}

func (e *Engine) checkVMEnd(r *machine.Record, s *scene.Scene, project string, opts Options) error {
	if s.VMEnd == nil {
		return nil
	}
	name := s.VMEnd.Snapshot
	if _, exists := r.Snapshots[name]; exists {
		if current, ok := r.Origin(name); ok {
			if !opts.Record && current.Take == machine.TakeRecording && !opts.ReplaceState {
				return fmt.Errorf("rehearsal cannot replace recording snapshot %q", name)
			}
		} else if opts.Adopt && opts.ConfirmAdopt != nil {
			if err := opts.ConfirmAdopt(name); err != nil {
				return err
			}
		}
	}
	return machine.CheckReplace(r, name, machine.SnapshotOrigin{Project: project, Scene: s.Name}, opts.Adopt)
}

func (e *Engine) saveEndState(ctx context.Context, s *scene.Scene, clip string, opts Options) error {
	e.startCapturePhase()
	defer e.endCapturePhase()
	if e.Managed == nil || e.managedRec == nil || s.VMEnd == nil {
		return errors.New("vm-end requires a managed stage")
	}
	kind := machine.TakeRecording
	if !opts.Record {
		kind = machine.TakeRehearsal
	}
	origin := machine.SnapshotOrigin{
		Project:      e.leafProject,
		Scene:        s.Name,
		InputsSHA256: e.inputsSHA256,
		StartImage:   e.startImage,
		Take:         kind,
		Backstage:    opts.Version,
	}
	result, err := replaceTakeState(e.Managed, ctx, e.managedRec, s.VMEnd.Snapshot, origin, opts.Adopt)
	e.mergeCapture(result)
	if err != nil {
		e.endFacts = endFactsWrite{
			result: facts.ResultCaptureFailed,
			end:    &facts.EndState{Snapshot: s.VMEnd.Snapshot, Status: "failed"},
		}
		if clip != "" {
			if werr := e.writeClipFacts(clip, s, opts.Version, e.endFacts.result, e.endFacts.end); werr != nil {
				return errors.Join(ErrCaptureFailed, err, werr)
			}
			e.endFacts.written = true
		}
		return errors.Join(ErrCaptureFailed, err)
	}
	e.stateSaved = true
	if result.Warning != "" {
		fmt.Fprintf(os.Stderr, "warning: %s\n", result.Warning)
	}
	if clip != "" && result.Image != nil {
		e.endFacts = endFactsWrite{
			result: facts.ResultOK,
			end:    &facts.EndState{Snapshot: s.VMEnd.Snapshot, Image: result.Image.ID},
		}
		if werr := e.writeClipFacts(clip, s, opts.Version, e.endFacts.result, e.endFacts.end); werr != nil {
			return werr
		}
		e.endFacts.written = true
	}
	return nil
}

func (e *Engine) retryEndFacts(clip string, s *scene.Scene, version string) error {
	if clip == "" || e.endFacts.end == nil || e.endFacts.written {
		return nil
	}
	if err := e.writeClipFacts(clip, s, version, e.endFacts.result, e.endFacts.end); err != nil {
		return err
	}
	e.endFacts.written = true
	return nil
}

func (e *Engine) startCapturePhase() {
	e.captureMu.Lock()
	e.captureDone = make(chan struct{})
	e.capturing.Store(true)
	e.captureMu.Unlock()
}

func (e *Engine) endCapturePhase() {
	e.capturing.Store(false)
	e.captureMu.Lock()
	done := e.captureDone
	e.captureMu.Unlock()
	if done != nil {
		close(done)
	}
}

func (e *Engine) waitCapture(timeout time.Duration) {
	onWaitCapture()
	e.captureMu.Lock()
	done := e.captureDone
	e.captureMu.Unlock()
	if done == nil {
		return
	}
	if timeout <= 0 {
		<-done
		return
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}

func (e *Engine) finishTakeSession(runErr *error) {
	sess := e.takeSess.Load()
	if sess == nil || sess.Finished() {
		return
	}
	if !sess.HasClip() {
		if err := sess.Discard(); err != nil && runErr != nil {
			*runErr = errors.Join(*runErr, err)
		}
		return
	}
	path, err := sess.KeepAttempt()
	if err != nil {
		if runErr != nil {
			*runErr = errors.Join(*runErr, err)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "   !! take kept at %s\n", path)
}

func (e *Engine) cleanupOnInterrupt(extra func()) {
	// Primary recorder/command cleanup is owned by the guard; repeat the command
	// interrupt here so direct cleanupOnInterrupt tests and future callers stay safe.
	e.interruptActiveCommand()
	if e.Prompt != nil {
		_ = e.Prompt.Close()
	}
	if e.Stager != nil {
		_ = e.Stager.Teardown()
	}
	if e.capturing.Load() {
		e.waitCapture(captureWaitTimeout)
	}
	if sess := e.takeSess.Load(); sess != nil {
		sess.Interrupt()
	}
	if extra != nil {
		extra()
	}
}

func (e *Engine) interruptActiveCommand() {
	if e.cmdGuard != nil {
		e.cmdGuard.Interrupt()
	}
}

// hasDialogStep reports whether the scene has a dialog step, which opens the
// prompter terminal and so requires the full hyprctl+terminal preflight. Aliases
// are resolved so an aliased dialog step is still detected.
func (e *Engine) hasDialogStep(s *scene.Scene) bool {
	for _, st := range s.Steps {
		if action, _ := e.resolve(st); action == "dialog" {
			return true
		}
	}
	return false
}

// recordsHyprOverlay reports whether the scene will record any Hypr-driven
// overlay — a dialog step (popup) or a live transition step (recorded prop) —
// so the host-readiness preflight runs before recording for every such path.
// Aliases are resolved so an aliased dialog/transition step is still detected.
func (e *Engine) recordsHyprOverlay(s *scene.Scene) bool {
	for _, st := range s.Steps {
		action, _ := e.resolve(st)
		switch action {
		case "dialog":
			return true
		case "transition":
			// Only a live transition records a Hypr overlay; an offline-cmd
			// transition used as a step is rejected by validation, but guard here.
			if t, ok := e.Project.Transitions[st.Value]; ok && t.RenderMode() == scene.RenderLive {
				return true
			}
		}
	}
	return false
}

// runHooks runs the setup hook for a fresh scene, else the reset hook.
func (e *Engine) runHooks(s *scene.Scene) error {
	h := e.Project.Hooks
	switch {
	case s.Fresh && h.Setup != "":
		fmt.Println(">> fresh: setup hook")
		return e.runScript(h.Setup)
	case s.ResetEnabled() && h.Reset != "":
		fmt.Println(">> reset hook")
		return e.runScript(h.Reset)
	}
	return nil
}

// runScript runs a project-relative hook script with the project env, in the
// project directory.
func (e *Engine) runScript(rel string) error {
	path, err := e.Project.InputPath(rel)
	if err != nil {
		return err
	}
	cmd := exec.Command(path)
	cmd.Dir = e.Project.Dir
	cmd.Env = e.Project.PropEnv()
	if vm, ok := e.Stager.(*stage.VM); ok && e.Managed != nil {
		g := vm.Guest
		cmd.Env = append(cmd.Env, "BACKSTAGE_STAGE="+e.ManagedName, "BACKSTAGE_VM_ADDRESS="+g.Address, "BACKSTAGE_VM_USER="+g.User, "BACKSTAGE_VM_ADMIN="+g.Admin, "BACKSTAGE_VM_KEY="+g.KeyFile, "BACKSTAGE_VM_DOMAIN="+g.Domain, "BACKSTAGE_VM_KNOWN_HOSTS="+g.KnownHosts)
	}
	scene.SetProcessGroup(cmd)
	var runErr error
	if e.cmdGuard != nil {
		runErr = e.runCommand(cmd)
	} else {
		runErr = runInterruptibleCommand(cmd)
	}
	if runErr != nil {
		return fmt.Errorf("hook %s: %w", rel, runErr)
	}
	return nil
}

func (e *Engine) runCommand(cmd *exec.Cmd) error {
	g := e.cmdGuard
	if g == nil {
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Wait()
	}
	if err := g.Start(cmd); err != nil {
		return err
	}
	defer g.Done(cmd)
	return cmd.Wait()
}

func runInterruptibleCommand(cmd *exec.Cmd) error {
	cmdGuard := &CommandGuard{}
	guard := newInterruptGuard(func() error {
		cmdGuard.Interrupt()
		return nil
	}, nil)
	defer guard.Release()

	if err := cmdGuard.Start(cmd); err != nil {
		return err
	}
	defer cmdGuard.Done(cmd)
	return cmd.Wait()
}
