// Package production stitches several scenes and the transitions between them
// into one video: record each scene to a clip, render each transition to a clip,
// normalize them to a common geometry/fps, and ffmpeg-concat in order.
package production

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/This-Is-NPC/backstage/internal/encode"
	"github.com/This-Is-NPC/backstage/internal/engine"
	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/prompter"
	"github.com/This-Is-NPC/backstage/internal/recorder"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/stage"
	"github.com/This-Is-NPC/backstage/internal/take"
	"github.com/This-Is-NPC/backstage/internal/transition"
)

// Options drive a production render.
type Options struct {
	Context      context.Context
	Project      *scene.Project
	Prod         scene.Production
	OutPath      string  // final mp4; empty → <project>/<record.out>/production.mp4
	ShowStaging  bool    // include the stage montage in scene clips
	KeepSegments bool    // keep intermediate clips for debugging
	Speed        float64 // scene timing multiplier (1 = real time)
	Version      string  // Backstage binary version written into scene-clip facts
}

// segment is one ordered piece of the final video.
type segment struct {
	kind     string // "scene" | "transition"
	name     string // scene name, or transition (Use) name
	from, to string // surrounding scenes (transitions only)
	// speed is how to present this take. Empty is real time.
	speed []scene.Segment
}

type interruptGuard interface {
	Stop() error
	Release()
}

var newInterruptGuard = func(stop func() error, onInterrupt func()) interruptGuard {
	return engine.NewInterruptGuard(stop, onInterrupt)
}

var runEngine = func(p *scene.Project, s *scene.Scene, opts engine.Options) error {
	eng, err := engine.NewForScene(p, s)
	if err != nil {
		return err
	}
	return eng.Run(s, opts)
}

var teardownHost = func() error {
	return (&stage.Hypr{}).Teardown()
}

var retimeClip = retime

var importTake = take.ImportContext

type producer struct {
	opts       Options
	reserved   map[string]bool
	generation string
	segDir     string
	speed      float64
	cleanup    func()
	raw        []string
	first      string
	phases     facts.ProductionPhases
}

// plan flattens a production into an ordered list of scene/transition segments.
// A transition "after X" is placed between X and the next scene; a transition
// with an empty "after" is an intro, placed before the first scene.
func plan(prod scene.Production) []segment {
	after := map[string]scene.TransitionUse{}
	var intro *scene.TransitionUse
	for _, tu := range prod.Transitions {
		if tu.After == "" {
			t := tu
			intro = &t
			continue
		}
		after[tu.After] = tu
	}
	var segs []segment
	if intro != nil && len(prod.Scenes) > 0 {
		segs = append(segs, segment{kind: "transition", name: intro.Use, to: prod.Scenes[0].Scene})
	}
	for i, ref := range prod.Scenes {
		sc := ref.Scene
		segs = append(segs, segment{kind: "scene", name: sc, speed: ref.Speed})
		if i < len(prod.Scenes)-1 {
			if tu, ok := after[sc]; ok {
				segs = append(segs, segment{
					kind: "transition", name: tu.Use,
					from: sc, to: prod.Scenes[i+1].Scene,
				})
			}
		}
	}
	return segs
}

// AdHoc builds a production from a scene-name list, optionally inserting the same
// transition between every consecutive pair.
func AdHoc(scenes []string, trans string) scene.Production {
	refs := make([]scene.SceneRef, 0, len(scenes))
	for _, name := range scenes {
		refs = append(refs, scene.SceneRef{Scene: name})
	}
	p := scene.Production{Scenes: refs}
	if trans != "" {
		for i := 0; i < len(scenes)-1; i++ {
			p.Transitions = append(p.Transitions, scene.TransitionUse{After: scenes[i], Use: trans})
		}
	}
	return p
}

// Run renders the production and returns the final mp4 path.
func Run(opts Options) (string, error) {
	started := time.Now()
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	p := opts.Project
	if err := p.ValidateProduction(opts.Prod); err != nil {
		return "", err
	}
	reserved, err := managedStages(p, opts.Prod)
	if err != nil {
		return "", err
	}
	if len(reserved) > 0 {
		m, err := machine.New()
		if err != nil {
			return "", err
		}
		names := []string{}
		for name := range reserved {
			names = append(names, name)
		}
		release, err := m.Store.LockMany(names...)
		if err != nil {
			return "", err
		}
		defer release()
	}
	speed := opts.Speed
	if speed <= 0 {
		speed = 1
	}
	// fps/w/h share the in-scene resolver so the two paths can't drift; production
	// then probes a recorded clip below to turn native (0) w/h into real pixels.
	fps, _, _ := p.ResolveRenderDims()

	if p.Render.W > 0 && p.Render.H > 0 {
		if _, err := encode.Encoder(p.Render.Encoder).Probe(opts.Context, p.Render.W, p.Render.H, fps); err != nil {
			return "", err
		}
	}

	segDir, err := os.MkdirTemp("", "backstage-prod-*")
	if err != nil {
		return "", err
	}
	if !opts.KeepSegments {
		defer os.RemoveAll(segDir)
	} else {
		fmt.Printf(">> segments: %s\n", segDir)
	}
	cleanupSegmentsOnInterrupt := func() {
		if !opts.KeepSegments {
			_ = os.RemoveAll(segDir)
		}
	}

	env := p.PropEnv()

	// 1. resolve target geometry. If config omitted either dimension, record the
	// first scene clip early so transitions can receive real {{w}}/{{h}} values.
	segs := plan(opts.Prod)
	pr := &producer{
		opts:       opts,
		reserved:   reserved,
		generation: engine.NewStateGeneration(),
		segDir:     segDir,
		speed:      speed,
		cleanup:    cleanupSegmentsOnInterrupt,
		raw:        make([]string, len(segs)),
	}
	firstSceneIndex := -1
	for i, sg := range segs {
		if sg.kind == "scene" {
			firstSceneIndex = i
			break
		}
	}
	w, h := p.Render.W, p.Render.H
	if w == 0 || h == 0 {
		if firstSceneIndex == -1 {
			return "", fmt.Errorf("production has no scene clips")
		}
		if err := pr.recordScene(firstSceneIndex, segs[firstSceneIndex]); err != nil {
			return "", err
		}
		if pr.first == "" {
			return "", fmt.Errorf("production has no scene clips")
		}
		info, err := probeVideo(pr.opts.Context, pr.first)
		if err != nil {
			return "", err
		}
		if w == 0 {
			w = info.Width
		}
		if h == 0 {
			h = info.Height
		}
	}

	if p.Render.W == 0 || p.Render.H == 0 {
		if _, err := encode.Encoder(p.Render.Encoder).Probe(opts.Context, w, h, fps); err != nil {
			return "", err
		}
	}

	// 2. render remaining segments in production order.
	for i, sg := range segs {
		if pr.raw[i] != "" {
			continue
		}
		if sg.kind == "scene" {
			if err := pr.recordScene(i, sg); err != nil {
				return "", err
			}
		} else {
			transitionStarted := time.Now()
			clip := filepath.Join(segDir, fmt.Sprintf("%03d-%s.mp4", i, sg.kind))
			t := p.Transitions[sg.name]
			v := transition.Vars{Out: clip, W: w, H: h, FPS: fps, From: sg.from, To: sg.to}
			if t.RenderMode() == scene.RenderLive {
				fmt.Printf(">> live transition %q (%s → %s) → clip\n", sg.name, sg.from, sg.to)
				if err := recordLiveTransition(p, t, v, clip, cleanupSegmentsOnInterrupt); err != nil {
					return "", err
				}
			} else {
				fmt.Printf(">> transition %q (%s → %s) → clip\n", sg.name, sg.from, sg.to)
				if err := renderOfflineTransition(t.Cmd, v, env, p.Dir, cleanupSegmentsOnInterrupt); err != nil {
					return "", err
				}
			}
			pr.raw[i] = clip
			pr.phases.Transitions += time.Since(transitionStarted).Seconds()
		}
	}

	// 3. normalize each clip to the same geometry/fps/pixfmt so concat is clean.
	normalizeStarted := time.Now()
	norm, err := normalizeClips(opts.Context, pr.raw, segDir, p.Render, w, h, fps, cleanupSegmentsOnInterrupt)
	if err != nil {
		return "", err
	}
	pr.phases.Normalize = time.Since(normalizeStarted).Seconds()

	// 4. concat into the final video.
	out := opts.OutPath
	if out == "" {
		out, err = p.OutputPath(p.Record.Out, "production.mp4")
		if err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return "", err
	}
	concatStarted := time.Now()
	if err := concatFinal(opts.Context, norm, out, segDir, cleanupSegmentsOnInterrupt); err != nil {
		return "", err
	}
	pr.phases.Concat = time.Since(concatStarted).Seconds()
	pr.phases.Total = time.Since(started).Seconds()
	if err := facts.Write(facts.Path(out), facts.Facts{Backstage: opts.Version, Result: facts.ResultOK, ProductionPhases: &pr.phases}); err != nil {
		return "", err
	}
	fmt.Printf(">> production timings: total=%.3f record=%.3f retime=%.3f transitions=%.3f normalize=%.3f concat=%.3f\n", pr.phases.Total, pr.phases.Record, pr.phases.Retime, pr.phases.Transitions, pr.phases.Normalize, pr.phases.Concat)
	return out, nil
}

func (pr *producer) recordScene(i int, sg segment) error {
	started := time.Now()
	p := pr.opts.Project
	clip := filepath.Join(pr.segDir, fmt.Sprintf("%03d-%s.mp4", i, sg.kind))
	path, err := p.ScenePathSafe(sg.name)
	if err != nil {
		return err
	}
	s, err := scene.LoadScene(path)
	if err != nil {
		return err
	}
	if err := s.Validate(p); err != nil {
		return err
	}
	fmt.Printf(">> scene %q → clip\n", sg.name)
	runErr := runEngine(p, s, engine.Options{
		Context: pr.opts.Context, ReservedStages: pr.reserved,
		Record: true, OutPath: clip, ShowStaging: pr.opts.ShowStaging, Speed: pr.speed,
		OnInterrupt: pr.cleanup, Version: pr.opts.Version,
		StateGeneration: pr.generation,
	})
	pubErr := pr.finishTake(s, clip, runErr)
	var teardownErr error
	if s.VM == "" {
		teardownErr = teardownHost()
	}
	if runErr != nil {
		if pubErr != nil {
			runErr = errors.Join(runErr, pubErr)
		}
		if teardownErr != nil {
			return fmt.Errorf("scene %q: %w; teardown: %v", sg.name, runErr, teardownErr)
		}
		return runErr
	}
	if pubErr != nil {
		if teardownErr != nil {
			return fmt.Errorf("scene %q: %w; teardown: %v", sg.name, pubErr, teardownErr)
		}
		return pubErr
	}
	if teardownErr != nil {
		return fmt.Errorf("teardown after scene %q: %w", sg.name, teardownErr)
	}
	// And then how to present it. The take on disk keeps the time it
	// really took; what goes into the production is a retimed copy.
	//
	// After the recording and never during it: a scene played fast is a
	// machine given less time, and the whole point of a long take is that
	// the machine had every second of it.
	shown := clip
	pr.phases.Record += time.Since(started).Seconds()
	if len(sg.speed) > 0 {
		retimeStarted := time.Now()
		fmt.Printf(">> scene %q → retimed\n", sg.name)
		shown, err = retimeClip(pr.opts.Context, clip, sg.speed, p.Render, pr.cleanup)
		if err != nil {
			return err
		}
		pr.phases.Retime += time.Since(retimeStarted).Seconds()
	}
	pr.raw[i] = shown
	if pr.first == "" {
		pr.first = shown
	}
	return nil
}

func publishEligible(speed float64, showStaging bool) bool {
	return speed == 1 && !showStaging
}

func successfulEndState(end *facts.EndState) bool {
	return end != nil && end.Snapshot != "" && end.Image != "" && end.Status == ""
}

func (pr *producer) finishTake(s *scene.Scene, clip string, runErr error) error {
	if !publishEligible(pr.speed, pr.opts.ShowStaging) {
		return nil
	}
	name := s.Name
	factsPath := facts.Path(clip)
	f, err := facts.Read(factsPath)
	if err != nil {
		if runErr != nil {
			return nil
		}
		return fmt.Errorf("scene %q facts: %w", name, err)
	}
	ok := f.Result == facts.ResultOK
	failed := f.Result == facts.ResultStepsFailed || f.Result == facts.ResultShort || f.Result == facts.ResultCaptureFailed
	if s.VMEnd != nil {
		if errors.Is(runErr, engine.ErrCaptureFailed) || !successfulEndState(f.EndState) {
			ok = false
			failed = true
		}
	}
	if !ok && !failed {
		return nil
	}
	ctx := pr.opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var sess atomic.Pointer[take.Session]
	guard := newInterruptGuard(func() error {
		cancel()
		return nil
	}, func() {
		if s := sess.Load(); s != nil {
			s.Interrupt()
		}
		if pr.cleanup != nil {
			pr.cleanup()
		}
	})
	defer guard.Release()
	imported, err := importTake(ctx, pr.opts.Project, name, clip, factsPath, func(s *take.Session) {
		sess.Store(s)
	})
	if err != nil {
		return fmt.Errorf("scene %q: import take: %w", name, err)
	}
	sess.Store(imported)
	if ok {
		pub, err := imported.Publish(ctx)
		if err != nil {
			return err
		}
		fmt.Printf(">> published %q %s\n", name, pub.StableClip)
		return nil
	}
	path, err := imported.KeepAttempt()
	if err != nil {
		return err
	}
	fmt.Printf(">> attempt %q %s\n", name, path)
	return nil
}

func renderOfflineTransition(cmdText string, v transition.Vars, env []string, dir string, onInterrupt func()) error {
	cmd := transition.BuildCommand(cmdText, v, env, dir)
	if err := runProductionCommand(cmd, onInterrupt); err != nil {
		return fmt.Errorf("transition command failed: %w", err)
	}
	return transition.VerifyOutput(v.Out)
}

var runProductionCommand = runProductionCommandImpl

func runProductionCommandImpl(cmd *exec.Cmd, onInterrupt func()) error {
	var stderr bytes.Buffer
	capture := cmd.Stderr == nil
	if capture {
		cmd.Stderr = &stderr
	}
	scene.SetProcessGroup(cmd)
	cmdGuard := &engine.CommandGuard{}
	guard := newInterruptGuard(func() error {
		cmdGuard.Interrupt()
		return nil
	}, func() {
		if onInterrupt != nil {
			onInterrupt()
		}
	})
	defer guard.Release()

	if err := cmdGuard.Start(cmd); err != nil {
		return err
	}
	defer cmdGuard.Done(cmd)
	err := cmd.Wait()
	if message := strings.TrimSpace(stderr.String()); err != nil && capture && message != "" {
		return fmt.Errorf("%w: %s", err, message)
	}
	return err
}

// recordLiveTransition records the screen while a live prop drives the overlay,
// then verifies a non-empty mp4 landed at clip. SIGINT/SIGTERM during the prop
// kills the prop, stops the recorder (exactly once), removes the temp clip, runs
// onInterrupt (cleans the production temp dir so it doesn't leak on Ctrl-C), and
// exits. The recorder-stop guard and signal handling are shared with engine.Run
// via engine.InterruptGuard so the two paths can't drift.
func recordLiveTransition(p *scene.Project, t scene.Transition, v transition.Vars, clip string, onInterrupt func()) error {
	// Readiness preflight before recording: a missing hyprctl must fail fast, not
	// finalize an overlay-less segment. A live transition segment records a prop
	// over the compositor overlay and does NOT use the prompter terminal, so only
	// hyprctl is required here (PreflightHypr), not the configured terminal.
	if err := (&prompter.Hypr{}).PreflightHypr(); err != nil {
		return err
	}
	// A live prop does NOT own the clip file; the recorder writes {{out}}. Omit
	// {{out}} from the live substitution so the prop can't clobber the recording.
	cmd, err := p.PropCommand(t.Live.Prop, transition.SubstituteLiveArgs(t.Live.Args, v))
	if err != nil {
		return err
	}
	propGuard := &engine.CommandGuard{}

	rec := recorder.NewGPU(p.Record.Monitor, p.Record.FPS)

	// Arm the guard before starting the recorder, mirroring engine.Run. rec.Start
	// blocks up to ~10s waiting for the output file; if no guard were installed
	// during that wait, a SIGINT/SIGTERM would orphan the gpu-screen-recorder child
	// (GPU encoder held, file written forever). The guard's stop path cancels future
	// prop starts before stopping the recorder, so a signal during recorder warmup
	// cannot let the prop start in the main goroutine before os.Exit runs.
	//
	// The guard owns prop cancellation and recorder shutdown before running
	// onInterrupt, so onInterrupt only does temp teardown and never references the
	// guard. Interrupt order: cancel/kill prop, stop recorder (bounded on signal),
	// remove the clip, then onInterrupt (clean the production temp dir so it doesn't
	// leak on Ctrl-C).
	guard := newInterruptGuard(
		func() error {
			propGuard.Interrupt()
			_, err := rec.Stop()
			return err
		},
		func() {
			// Recorder cleanup is owned by the guard.
			_ = os.Remove(clip)
			if onInterrupt != nil {
				onInterrupt()
			}
		},
	)
	defer guard.Release()

	if err := rec.Start(clip); err != nil {
		// Guard armed but the prop never started. GPU.Start owns cleanup for any
		// recorder child it spawned; Release only removes the signal handler.
		guard.Release()
		return err
	}

	if err := propGuard.Start(cmd); err != nil {
		stopErr := guard.Stop()
		guard.Release()
		return errors.Join(fmt.Errorf("live transition %s: %w", t.Live.Prop, err), stopErr)
	}

	runErr := cmd.Wait()
	propGuard.Done(cmd)
	stopErr := guard.Stop()
	guard.Release()
	if runErr != nil {
		return errors.Join(fmt.Errorf("live transition %s: %w", t.Live.Prop, runErr), stopErr)
	}
	if stopErr != nil {
		return stopErr
	}
	// Verify a non-empty clip landed, mirroring the offline transition.VerifyOutput
	// invariant: a prop that exits 0 with no/short recording must not pass silently.
	fi, err := os.Stat(clip)
	if err != nil {
		return fmt.Errorf("live transition %s wrote no output at %s: %w", t.Live.Prop, clip, err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("live transition %s output is empty: %s", t.Live.Prop, clip)
	}
	return nil
}

func concatFinal(ctx context.Context, clips []string, out, workDir string, onInterrupt func()) error {
	outDir := filepath.Dir(out)
	tmp, err := os.CreateTemp(outDir, ".production-*.mp4")
	if err != nil {
		return err
	}
	tmpOut := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpOut)
		return err
	}
	cleanupConcatOnInterrupt := func() {
		if onInterrupt != nil {
			onInterrupt()
		}
		_ = os.Remove(tmpOut)
	}
	if err := concat(ctx, clips, tmpOut, workDir, cleanupConcatOnInterrupt); err != nil {
		_ = os.Remove(tmpOut)
		return err
	}
	if err := os.Rename(tmpOut, out); err != nil {
		_ = os.Remove(tmpOut)
		return err
	}
	return nil
}

// concat joins normalized clips (same codec/geometry) via the concat demuxer.
func concat(ctx context.Context, clips []string, out, workDir string, onInterrupt func()) error {
	list := filepath.Join(workDir, "concat.txt")
	var b strings.Builder
	for _, c := range clips {
		abs, err := filepath.Abs(c)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "file '%s'\n", concatEscape(abs))
	}
	if err := os.WriteFile(list, []byte(b.String()), 0o644); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-v", "error", "-f", "concat", "-safe", "0",
		"-i", list, "-c", "copy", out)
	if err := runProductionCommand(cmd, onInterrupt); err != nil {
		return fmt.Errorf("concat: %w", err)
	}
	return nil
}

func concatEscape(path string) string {
	return strings.ReplaceAll(path, `'`, `'\''`)
}
