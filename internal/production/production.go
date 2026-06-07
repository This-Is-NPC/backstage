// Package production stitches several scenes and the transitions between them
// into one video: record each scene to a clip, render each transition to a clip,
// normalize them to a common geometry/fps, and ffmpeg-concat in order.
package production

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/This-Is-NPC/backstage/internal/engine"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/stage"
	"github.com/This-Is-NPC/backstage/internal/transition"
)

// Options drive a production render.
type Options struct {
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
		segs = append(segs, segment{kind: "transition", name: intro.Use, to: prod.Scenes[0]})
	}
	for i, sc := range prod.Scenes {
		segs = append(segs, segment{kind: "scene", name: sc})
		if i < len(prod.Scenes)-1 {
			if tu, ok := after[sc]; ok {
				segs = append(segs, segment{
					kind: "transition", name: tu.Use,
					from: sc, to: prod.Scenes[i+1],
				})
			}
		}
	}
	return segs
}

// AdHoc builds a production from a scene-name list, optionally inserting the same
// transition between every consecutive pair.
func AdHoc(scenes []string, trans string) scene.Production {
	p := scene.Production{Scenes: scenes}
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
	speed := opts.Speed
	if speed <= 0 {
		speed = 1
	}
	fps := p.Render.FPS
	if fps == 0 {
		fps = p.Record.FPS
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

	env := append(os.Environ(), projectEnv(p)...)

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
		runErr := engine.New(p).Run(s, engine.Options{
			Record: true, OutPath: clip, ShowStaging: opts.ShowStaging, Speed: speed,
		})
		teardownErr := (&stage.Hypr{}).Teardown()
		if runErr != nil {
			if teardownErr != nil {
				return fmt.Errorf("scene %q: %w; teardown: %v", sg.name, runErr, teardownErr)
			}
			return runErr
		}
		if teardownErr != nil {
			return fmt.Errorf("teardown after scene %q: %w", sg.name, teardownErr)
		}
		raw[i] = clip
		if firstSceneClip == "" {
			firstSceneClip = clip
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
			fmt.Printf(">> transition %q (%s → %s) → clip\n", sg.name, sg.from, sg.to)
			t := p.Transitions[sg.name]
			v := transition.Vars{Out: clip, W: w, H: h, FPS: fps, From: sg.from, To: sg.to}
			if err := transition.Render(t.Cmd, v, env, p.Dir); err != nil {
				return "", err
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
		if err := normalize(c, n, w, h, fps); err != nil {
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
	if err := concat(norm, out, segDir); err != nil {
		return "", err
	}
	return out, nil
}

func projectEnv(p *scene.Project) []string {
	var env []string
	for k, v := range p.Env {
		env = append(env, k+"="+v)
	}
	return env
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
func normalize(in, out string, w, h, fps int) error {
	vf := fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d",
		w, h, w, h, fps)
	cmd := exec.Command("ffmpeg", "-y", "-i", in, "-vf", vf,
		"-pix_fmt", "yuv420p", "-c:v", "libx264", "-an", out)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("normalize %s: %w", in, err)
	}
	return nil
}

// concat joins normalized clips (same codec/geometry) via the concat demuxer.
func concat(clips []string, out, workDir string) error {
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
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("concat: %w", err)
	}
	return nil
}

func concatEscape(path string) string {
	return strings.ReplaceAll(path, `'`, `'\''`)
}
