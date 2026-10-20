package presentation

import (
	"bytes"
	"fmt"
	"math"
	"sort"

	"github.com/This-Is-NPC/backstage/internal/facts"
)

type Region = facts.Region

type CameraKeyframe struct {
	At     float64 `json:"at"`
	Region Region  `json:"region"`
	Step   string  `json:"step,omitempty"`
	Ease   string  `json:"ease,omitempty"`
	atRef  *TimeReference
}

func (k *CameraKeyframe) UnmarshalJSON(b []byte) error {
	type plain CameraKeyframe
	return decodeTimed(b, (*plain)(k), &k.atRef)
}

// Camera changes only the source image inside a slot. Text and chrome stay put.
type Camera struct {
	Keyframes []CameraKeyframe `json:"keyframes,omitempty"`
	follow    bool
}

func (c *Camera) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte(`"follow"`)) {
		*c = Camera{follow: true}
		return nil
	}
	type plain Camera
	return decodeJSON(bytes.NewReader(b), (*plain)(c))
}

func validRegion(r Region) bool {
	return validTime(r.X) && validTime(r.Y) && finite(r.W) && finite(r.H) && r.W > 0 && r.H > 0 && r.X+r.W <= 1+1e-9 && r.Y+r.H <= 1+1e-9
}

func (p *Plan) recordedSteps(track string) ([]facts.Step, error) {
	f, err := p.recordedFacts(track)
	return f.Steps, err
}

func (p *Plan) recordedFacts(track string) (facts.Facts, error) {
	path := facts.Path(p.Tracks[track].Media.Path)
	if err := p.addInput(path); err != nil {
		return facts.Facts{}, fmt.Errorf("track %q requires recorded step facts: %w", track, err)
	}
	f, err := facts.Read(path)
	if err != nil {
		return facts.Facts{}, err
	}
	seen := map[string]bool{}
	for _, step := range f.Steps {
		if step.ID != "" && seen[step.ID] {
			return facts.Facts{}, fmt.Errorf("duplicate recorded step %q", step.ID)
		}
		seen[step.ID] = true
		if !validTime(step.Start) || !finite(step.End) || step.End < step.Start {
			return facts.Facts{}, fmt.Errorf("invalid timing for step %q", step.ID)
		}
		if step.Region != nil && !validRegion(*step.Region) {
			return facts.Facts{}, fmt.Errorf("step %q: invalid recorded region", step.ID)
		}
		if point := step.Pointer; point != nil && !validPointer(point) {
			return facts.Facts{}, fmt.Errorf("step %q: invalid recorded pointer", step.ID)
		}
	}
	for i, sample := range f.PointerTrace {
		if !validTime(sample.At) || (i > 0 && sample.At <= f.PointerTrace[i-1].At) || !validPointer(sample.Pointer) {
			return facts.Facts{}, fmt.Errorf("track %q: invalid pointer sample %d", track, i)
		}
	}
	return f, nil
}

func validPointer(p *facts.Pointer) bool {
	return p == nil || (validTime(p.X) && validTime(p.Y) && p.X <= 1 && p.Y <= 1)
}

func (p *Plan) compileCamera(track string, camera Camera) (Camera, error) {
	tr, ok := p.Tracks[track]
	if !ok {
		return camera, fmt.Errorf("camera references unknown track %q", track)
	}
	if camera.follow {
		steps, err := p.recordedSteps(track)
		if err != nil {
			return camera, err
		}
		for _, step := range steps {
			if step.Region == nil {
				continue
			}
			for _, span := range mapSpan(tr, step.Start, step.End) {
				if span.At < p.Document.Duration {
					camera.Keyframes = append(camera.Keyframes, CameraKeyframe{At: span.At, Region: *step.Region})
				}
			}
		}
		if len(camera.Keyframes) == 0 {
			return camera, fmt.Errorf("camera follow requires recorded step regions in track %q", track)
		}
		sort.SliceStable(camera.Keyframes, func(i, j int) bool { return camera.Keyframes[i].At < camera.Keyframes[j].At })
		// Adjacent source actions can land on the same output frame after retiming.
		var keys []CameraKeyframe
		for _, key := range camera.Keyframes {
			if n := len(keys); n > 0 && keys[n-1].At == key.At {
				keys[n-1] = key
			} else {
				keys = append(keys, key)
			}
		}
		camera.Keyframes = keys
	}
	for i := range camera.Keyframes {
		k := &camera.Keyframes[i]
		if k.atRef != nil {
			r := *k.atRef
			if r.Track == "" {
				r.Track = track
			}
			at, err := p.resolveTime(&r)
			if err != nil {
				return camera, err
			}
			k.At = at
		}
		if k.Step != "" {
			steps, err := p.recordedSteps(track)
			if err != nil {
				return camera, err
			}
			found := false
			for _, step := range steps {
				if step.ID == k.Step && step.Region != nil {
					k.Region, found = *step.Region, true
					break
				}
			}
			if !found {
				return camera, fmt.Errorf("step %q has no recorded region", k.Step)
			}
		}
		if k.Ease == "" {
			k.Ease = "cut"
		}
		if !validTime(k.At) || k.At >= p.Document.Duration || (i > 0 && k.At <= camera.Keyframes[i-1].At) || !validRegion(k.Region) || k.Region.W < 1.0/16 || k.Region.H < 1.0/16 || (k.Ease != "cut" && !validEase(k.Ease)) {
			return camera, fmt.Errorf("camera %q keyframe %d needs increasing times, a source region within 0..1 at no more than 16x zoom, and cut or a valid easing", track, i)
		}
		if i > 0 && k.Ease != "cut" {
			before := camera.Keyframes[i-1].Region
			if math.Abs(before.W/before.H-k.Region.W/k.Region.H) > 1e-6 {
				return camera, fmt.Errorf("camera %q: moving regions must have the same aspect ratio; use cut to change it", track)
			}
		}
	}
	return camera, nil
}

func (p *Plan) compileCameras() error {
	for _, id := range sortedKeys(p.Tracks) {
		tr := p.Tracks[id]
		c, err := p.compileCamera(id, tr.Camera)
		if err != nil {
			return err
		}
		tr.Camera = c
		p.Tracks[id] = tr
	}
	for i := range p.Document.Timeline {
		e := &p.Document.Timeline[i]
		for _, id := range sortedKeys(e.Camera) {
			visible := false
			for _, tr := range e.Slots {
				visible = visible || tr == id
			}
			if !visible {
				return fmt.Errorf("event %d: camera track %q is not in a slot", i, id)
			}
			c, err := p.compileCamera(id, e.Camera[id])
			if err != nil {
				return fmt.Errorf("event %d: %w", i, err)
			}
			e.Camera[id] = c
		}
	}
	return nil
}

func (p *Plan) eventCamera(event int, track string) Camera {
	if event >= 0 && event < len(p.Document.Timeline) {
		if c, ok := p.Document.Timeline[event].Camera[track]; ok {
			return c
		}
	}
	return p.Tracks[track].Camera
}

func (c Camera) region(t float64) Region {
	r := Region{W: 1, H: 1}
	for i, k := range c.Keyframes {
		if t >= k.At {
			r = k.Region
			continue
		}
		if i > 0 && k.Ease != "cut" {
			a := c.Keyframes[i-1]
			p := easeProgress((t-a.At)/(k.At-a.At), k.Ease)
			r = Region{X: a.Region.X + (k.Region.X-a.Region.X)*p, Y: a.Region.Y + (k.Region.Y-a.Region.Y)*p, W: a.Region.W + (k.Region.W-a.Region.W)*p, H: a.Region.H + (k.Region.H-a.Region.H)*p}
		}
		break
	}
	return r
}

func easeProgress(p float64, name string) float64 {
	p = max(0, min(1, p))
	if name == "linear" {
		return p
	}
	if name == "ease-in-out" {
		if p < 0.5 {
			return 4 * p * p * p
		}
		q := 2 - 2*p
		return 1 - q*q*q/2
	}
	q := 1 - p
	return 1 - q*q*q
}

// regionExpr is evaluated on the chunk clock by FFmpeg's zoompan filter.
func (c Camera) regionExpr(axis string, clock string) string {
	value := func(r Region) float64 {
		switch axis {
		case "x":
			return r.X
		case "y":
			return r.Y
		default:
			return r.W
		}
	}
	prior := Region{W: 1, H: 1}
	expr := num(value(prior))
	for i, k := range c.Keyframes {
		left := expr
		if i > 0 && k.Ease != "cut" {
			a := c.Keyframes[i-1].At
			p := fmt.Sprintf("clip((%s-%s)/%s,0,1)", clock, num(a), num(k.At-a))
			switch k.Ease {
			case "ease-out":
				p = "(1-pow(1-" + p + ",3))"
			case "ease-in-out":
				p = "if(lt(" + p + ",0.5),4*pow(" + p + ",3),1-pow(-2*" + p + "+2,3)/2)"
			}
			left = fmt.Sprintf("(%s+(%s)*%s)", num(value(prior)), num(value(k.Region)-value(prior)), p)
			// Replace only the previous hold interval with this interpolation.
			left = fmt.Sprintf("if(gte(%s,%s),%s,%s)", clock, num(a), left, expr)
		}
		expr = fmt.Sprintf("if(lt(%s,%s),%s,%s)", clock, num(k.At), left, num(value(k.Region)))
		prior = k.Region
	}
	return expr
}

func cameraScaleFilter(box slotBox, srcW, srcH int) string {
	r := box.Camera.region(box.CameraAt)
	cw, ch := cameraSourceSize(srcW, srcH, r)
	w, h, x, y := containPad(cw, ch, box.CW, box.CH)
	switch box.Fit {
	case "fill":
		w, h, x, y = box.CW, box.CH, 0, 0
	case "cover":
		w, h, x, y = coverPad(cw, ch, box.CW, box.CH)
		x, y = -x, -y
	}
	clock := fmt.Sprintf("(on/%d+%s)", box.CameraFPS, num(box.CameraAt))
	ratio := r.W / r.H
	canvasW, canvasH := srcW, srcH
	if ratio > 1 {
		canvasW = int(math.Ceil(float64(srcW) * ratio))
	} else {
		canvasH = int(math.Ceil(float64(srcH) / ratio))
	}
	scaled := fmt.Sprintf("format=rgba,pad=%d:%d:0:0:color=%s,zoompan=z='%s/(%s)':x='%d*(%s)':y='%d*(%s)':d=1:s=%dx%d:fps=%d,format=rgba", canvasW, canvasH, padTransparent, num(float64(canvasW)/float64(srcW)), box.Camera.regionExpr("w", clock), srcW, box.Camera.regionExpr("x", clock), srcH, box.Camera.regionExpr("y", clock), w, h, box.CameraFPS)

	if box.Fit == "cover" {
		return scaled + fmt.Sprintf(",crop=%d:%d:%d:%d:exact=1", box.CW, box.CH, -x, -y)
	}
	return scaled + fmt.Sprintf(",pad=%d:%d:%d:%d:color=%s", box.CW, box.CH, x, y, padTransparent)
}

func cameraSourceSize(w, h int, r Region) (int, int) {
	// Keep the region aspect ratio without rounding the crop to source pixels.
	return int(math.Round(float64(w) * r.W * 1e6)), int(math.Round(float64(h) * r.H * 1e6))
}

func (p *Plan) cameraChunkStableAspect(ch chunkRange) bool {
	if ch.Event < 0 || ch.Event >= len(p.Document.Timeline) {
		return true
	}
	for _, id := range p.Document.Timeline[ch.Event].Slots {
		c := p.eventCamera(ch.Event, id)
		r := c.region(float64(ch.First) / float64(p.FPS))
		for n := ch.First; n < ch.End; n++ {
			next := c.region(float64(n) / float64(p.FPS))
			// zoompan clamps its zoom to 10. Higher zooms remain exact in frames.
			if next.W < 0.1 || next.H < 0.1 || math.Abs(r.W/r.H-next.W/next.H) > 1e-6 {
				return false
			}
		}
	}
	return true
}
