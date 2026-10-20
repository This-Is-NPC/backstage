package presentation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func chunkGraphicsMoving(p *Plan, ch chunkRange) bool {
	for n := ch.First; n < ch.End; n++ {
		if p.graphicsMoving(float64(n) / float64(p.FPS)) {
			return true
		}
	}
	return false
}

// The template remains still; only independent editorial graphics animate.
// Video pixels never enter Chromium in this path.
func animatedChunkPath(r *Renderer, p *Plan, ch chunkRange, res *chunkResult) (chunkPath, error) {
	probes := make([]probeFrame, 2)
	for i, n := range []int{ch.First, ch.End - 1} {
		pr, dur, err := r.probeAt(float64(n)/float64(p.FPS), false)
		if err != nil {
			return chunkPath{}, err
		}
		res.probe.add(dur)
		probes[i] = pr
		if pr.Canvas || pr.Video || len(pr.DynamicElements) > 0 {
			return chunkPath{dec: layerDecision{Reason: "dynamic template"}}, nil
		}
	}
	first, last := probes[0], probes[1]
	if dec := stableSlotGeometry(p, ch, first, last); !dec.Layered {
		return chunkPath{dec: dec}, nil
	}
	if ok, reason := staticGuard(first, last); !ok {
		return chunkPath{dec: layerDecision{Reason: reason}, violation: true}, nil
	}
	return chunkPath{animated: true, probes: probes, dec: layerDecision{Layered: true}}, nil
}

func renderAnimatedChunk(ctx context.Context, r *Renderer, p *Plan, work string, tracks map[string]string, ch chunkRange, threads int, out string, res *chunkResult, probe probeFrame, meta *trackMeta) error {
	dir, err := os.MkdirTemp(work, "animation-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	t := float64(ch.First) / float64(p.FPS)
	below, first, shotBelow, shotAbove, belowHit, aboveHit, err := r.captureEventLayers(t, ch.Event, probe.Geometry, fmt.Sprintf("animation-%d", ch.First))
	if err != nil {
		return err
	}
	if !belowHit {
		res.shot.add(shotBelow)
		res.screenshotBytes += int64(len(below))
	}
	if !aboveHit {
		res.shot.add(shotAbove)
		res.screenshotBytes += int64(len(first))
	}
	for n := ch.First; n < ch.End; n++ {
		png := first
		if n > ch.First {
			var shot time.Duration
			png, shot, err = r.captureOneLayer(float64(n)/float64(p.FPS), "above")
			if err != nil {
				return err
			}
			res.shot.add(shot)
			res.screenshotBytes += int64(len(png))
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%06d.png", n-ch.First)), png, 0600); err != nil {
			return err
		}
	}
	boxes := make([]slotBox, len(probe.Geometry))
	for i, g := range probe.Geometry {
		boxes[i] = snapSlot(g)
	}
	started := time.Now()
	err = compositeLayers(ctx, p, ch, tracks, boxes, layerImages{Below: below, AboveSequence: filepath.Join(dir, "%06d.png")}, threads, work, out, meta, composedFrameObserver(p))
	res.composite = roundSec(time.Since(started))
	res.animatedOverlayFrames = ch.End - ch.First
	return err
}
