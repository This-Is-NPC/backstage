package presentation

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"
)

func stillTransitionTimes(p *Plan, ch chunkRange) (before, after float64, ok bool) {
	if !chunkTransition(p, ch) || ch.Event < 1 {
		return 0, 0, false
	}
	e := p.Document.Timeline[ch.Event]
	if e.Transition.xfadeName() != "" {
		for _, at := range []float64{e.At, e.Transition.Duration} {
			frames := at * float64(p.FPS)
			if math.Abs(frames-math.Round(frames)) > 1e-7 {
				return 0, 0, false
			}
		}
	}
	if (e.Transition.Effect != "fade" && e.Transition.xfadeName() == "") || len(e.Slots) > 0 || len(p.Document.Timeline[ch.Event-1].Slots) > 0 {
		return 0, 0, false
	}
	before = e.At - 1/float64(p.FPS)
	after = e.At + e.Transition.Duration
	next := p.Document.Duration
	if ch.Event+1 < len(p.Document.Timeline) {
		next = p.Document.Timeline[ch.Event+1].At
	}
	if before < p.Document.Timeline[ch.Event-1].At || after >= next {
		return 0, 0, false
	}
	return before, after, true
}

func stillTransitionCandidate(p *Plan, ch chunkRange, r *Renderer) bool {
	_, _, ok := stillTransitionTimes(p, ch)
	if !ok || !r.eventStatic(ch.Event-1) || !r.eventStatic(ch.Event) {
		return false
	}
	for n := ch.First; n < ch.End; n++ {
		if p.graphicsMoving(float64(n) / float64(p.FPS)) {
			return false
		}
		if len(visibleCaptions(p.Text, float64(n)/float64(p.FPS))) > 0 {
			return false
		}
	}
	return true
}

func stillTransitionEligible(r *Renderer, p *Plan, ch chunkRange, res *chunkResult) (layerDecision, error) {
	before, after, _ := stillTransitionTimes(p, ch)
	for _, t := range []float64{before, after} {
		pr, dur, err := r.probeTimed(t)
		if err != nil {
			return layerDecision{}, err
		}
		res.probe.add(dur)
		if pr.DOMHash == "" || len(pr.Geometry) > 0 || pr.AnimationsActive || pr.SMIL || pr.Canvas || pr.Video || len(pr.DynamicElements) > 0 {
			return layerDecision{Reason: "dynamic HTML"}, nil
		}
	}
	return layerDecision{Layered: true}, nil
}

func renderStillTransitionChunk(ctx context.Context, r *Renderer, p *Plan, work string, ch chunkRange, encodeThreads int, out string, res *chunkResult) error {
	before, after, _ := stillTransitionTimes(p, ch)
	old, firstTimes, err := r.frameTimed(before, nil)
	d1 := firstTimes.screenshot
	if err != nil {
		return err
	}
	now, secondTimes, err := r.frameTimed(after, nil)
	d2 := secondTimes.screenshot
	if err != nil {
		return err
	}
	res.shot.add(d1)
	res.shot.add(d2)
	res.screenshotBytes += int64(len(old) + len(now))
	start := time.Now()
	err = composeStillTransition(ctx, p, ch, old, now, encodeThreads, work, out, composedFrameObserver(p))
	res.composite = roundSec(time.Since(start))
	return err
}

func composeStillTransition(ctx context.Context, p *Plan, ch chunkRange, old, now []byte, encodeThreads int, work, out string, observe func(int, []byte) error) error {
	dir := filepath.Join(work, fmt.Sprintf("fade-%d", ch.Index))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	oldPath := filepath.Join(dir, "old.png")
	nowPath := filepath.Join(dir, "new.png")
	if err := os.WriteFile(oldPath, old, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(nowPath, now, 0o600); err != nil {
		return err
	}
	args := append(append([]string{"-v", "error", "-y"}, stillInput(p.FPS, oldPath)...), stillInput(p.FPS, nowPath)...)
	e := p.Document.Timeline[ch.Event]
	offset := float64(ch.First)/float64(p.FPS) - e.At
	alpha := fmt.Sprintf("min(1,max(0,(T+%s)/%s))", num(offset), num(e.Transition.Duration))
	hold := stillHold(p.FPS)
	graph := fmt.Sprintf("[0:v]format=gbrp,%s[a];[1:v]format=gbrp,%s[b];[a][b]blend=all_expr='A*pow(1-%s,2)+B*%s'[out]", hold, hold, alpha, alpha)
	if effect := e.Transition.xfadeName(); effect != "" {
		graph = fmt.Sprintf("[0:v]format=gbrp,%s[a];[1:v]format=gbrp,%s[b];[a][b]xfade=transition=%s:duration=%s:offset=0,trim=start=%s,setpts=PTS-STARTPTS[out]", hold, hold, effect, num(e.Transition.Duration), num(offset))
	}
	return encodeGraph(ctx, p, args, graph, ch.First, ch.End-ch.First, encodeThreads, out, observe)
}
