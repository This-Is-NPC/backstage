// Package production stitches several scenes and the transitions between them
// into one video: record each scene to a clip, render each transition to a clip,
// normalize them to a common geometry/fps, and ffmpeg-concat in order.
package production

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/This-Is-NPC/backstage/internal/engine"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/prompter"
	"github.com/This-Is-NPC/backstage/internal/recorder"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/stage"
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
	raw := make([]string, len(segs))
	firstSceneClip := ""
	recordScene := func(i int, sg segment) error {
		clip := filepath.Join(segDir, fmt.Sprintf("%03d-%s.mp4", i, sg.kind))
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
		eng, err := engine.NewForScene(p, s)
		if err != nil {
			return err
		}
		runErr := eng.Run(s, engine.Options{
			Context: opts.Context, ReservedStages: reserved,
			Record: true, OutPath: clip, ShowStaging: opts.ShowStaging, Speed: speed,
			OnInterrupt: cleanupSegmentsOnInterrupt,
		})
		var teardownErr error
		if s.VM == "" {
			teardownErr = (&stage.Hypr{}).Teardown()
		}
		if runErr != nil {
			if teardownErr != nil {
				return fmt.Errorf("scene %q: %w; teardown: %v", sg.name, runErr, teardownErr)
			}
			return runErr
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
		if len(sg.speed) > 0 {
			fmt.Printf(">> scene %q → retimed\n", sg.name)
			shown, err = Retime(clip, sg.speed, "")
			if err != nil {
				return err
			}
		}
		raw[i] = shown
		if firstSceneClip == "" {
			firstSceneClip = shown
		}
		return nil
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
		if err := recordScene(firstSceneIndex, segs[firstSceneIndex]); err != nil {
			return "", err
		}
		if firstSceneClip == "" {
			return "", fmt.Errorf("production has no scene clips")
		}
		probeW, probeH, err := probeDims(firstSceneClip)
		if err != nil {
			return "", err
		}
		if w == 0 {
			w = probeW
		}
		if h == 0 {
			h = probeH
		}
	}

	// 2. render remaining segments in production order.
	for i, sg := range segs {
		if raw[i] != "" {
			continue
		}
		if sg.kind == "scene" {
			if err := recordScene(i, sg); err != nil {
				return "", err
			}
		} else {
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
			raw[i] = clip
		}
	}

	// 3. normalize each clip to the same geometry/fps/pixfmt so concat is clean.
	var norm []string
	for i, c := range raw {
		if c == "" {
			return "", fmt.Errorf("segment %d produced no clip", i)
		}
		n := filepath.Join(segDir, fmt.Sprintf("n%03d.mp4", i))
		if err := normalize(c, n, w, h, fps, cleanupSegmentsOnInterrupt); err != nil {
			return "", err
		}
		norm = append(norm, n)
	}

	// 4. concat into the final video.
	out := opts.OutPath
	if out == "" {
		out, err = p.SafePath(p.Record.Out, "production.mp4")
		if err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return "", err
	}
	if err := concatFinal(norm, out, segDir, cleanupSegmentsOnInterrupt); err != nil {
		return "", err
	}
	return out, nil
}

func renderOfflineTransition(cmdText string, v transition.Vars, env []string, dir string, onInterrupt func()) error {
	cmd := transition.BuildCommand(cmdText, v, env, dir)
	if err := runProductionCommand(cmd, onInterrupt); err != nil {
		return fmt.Errorf("transition command failed: %w", err)
	}
	return transition.VerifyOutput(v.Out)
}

func runProductionCommand(cmd *exec.Cmd, onInterrupt func()) error {
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
	return cmd.Wait()
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
	// Verify a non-empty clip landed, mirroring the offline transition.Render
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

// probeDims returns a video's width and height via ffprobe.
func probeDims(path string) (int, int, error) {
	out, err := exec.Command("ffprobe", "-v", "error",
		"-select_streams", "v:0", "-show_entries", "stream=width,height",
		"-of", "csv=p=0:s=x", path).Output()
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("ffprobe %s: unexpected %q", path, out)
	}
	w, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe %s: invalid width %q", path, parts[0])
	}
	h, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe %s: invalid height %q", path, parts[1])
	}
	if w == 0 || h == 0 {
		return 0, 0, fmt.Errorf("ffprobe %s: zero dimensions", path)
	}
	return w, h, nil
}

// normalize re-encodes a clip to a fixed geometry/fps/pixfmt (audio dropped).
func normalize(in, out string, w, h, fps int, onInterrupt func()) error {
	vf := fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d",
		w, h, w, h, fps)
	cmd := exec.Command("ffmpeg", "-y", "-i", in, "-vf", vf,
		"-pix_fmt", "yuv420p", "-c:v", "libx264", "-an", out)
	cmd.Stderr = os.Stderr
	if err := runProductionCommand(cmd, onInterrupt); err != nil {
		return fmt.Errorf("normalize %s: %w", in, err)
	}
	return nil
}

func concatFinal(clips []string, out, workDir string, onInterrupt func()) error {
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
	if err := concat(clips, tmpOut, workDir, cleanupConcatOnInterrupt); err != nil {
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
func concat(clips []string, out, workDir string, onInterrupt func()) error {
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
	cmd := exec.Command("ffmpeg", "-y", "-f", "concat", "-safe", "0",
		"-i", list, "-c", "copy", out)
	cmd.Stderr = os.Stderr
	if err := runProductionCommand(cmd, onInterrupt); err != nil {
		return fmt.Errorf("concat: %w", err)
	}
	return nil
}

func concatEscape(path string) string {
	return strings.ReplaceAll(path, `'`, `'\''`)
}
