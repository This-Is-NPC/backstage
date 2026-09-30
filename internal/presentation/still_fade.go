package presentation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func stillFadeTimes(p *Plan, ch chunkRange) (before, after float64, ok bool) {
	if !chunkTransition(p, ch) || ch.Event < 1 {
		return 0, 0, false
	}
	e := p.Document.Timeline[ch.Event]
	if e.Transition.Effect != "fade" || len(e.Slots) > 0 || len(p.Document.Timeline[ch.Event-1].Slots) > 0 {
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

func stillFadeCandidate(p *Plan, ch chunkRange, r *Renderer) bool {
	_, _, ok := stillFadeTimes(p, ch)
	if !ok || !r.eventStatic(ch.Event-1) || !r.eventStatic(ch.Event) {
		return false
	}
	for n := ch.First; n < ch.End; n++ {
		if len(visibleCaptions(p.Text, float64(n)/float64(p.FPS))) > 0 {
			return false
		}
	}
	return true
}

func stillFadeEligible(r *Renderer, p *Plan, ch chunkRange, res *chunkResult) (layerDecision, error) {
	before, after, _ := stillFadeTimes(p, ch)
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

func renderStillFadeChunk(ctx context.Context, r *Renderer, p *Plan, work string, ch chunkRange, encodeThreads int, out string, res *chunkResult) error {
	before, after, _ := stillFadeTimes(p, ch)
	old, d1, err := r.captureOneLayer(before, "below")
	if err != nil {
		return err
	}
	now, d2, err := r.captureOneLayer(after, "below")
	if err != nil {
		return err
	}
	res.shot.add(d1)
	res.shot.add(d2)
	res.screenshotBytes += int64(len(old) + len(now))
	start := time.Now()
	err = composeStillFade(ctx, p, ch, old, now, encodeThreads, work, out, composedFrameObserver(p))
	res.composite = roundSec(time.Since(start))
	return err
}

func composeStillFade(ctx context.Context, p *Plan, ch chunkRange, old, now []byte, encodeThreads int, work, out string, observe func(int, []byte) error) error {
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
	return encodeGraph(ctx, p, args, graph, ch.First, ch.End-ch.First, encodeThreads, out, observe)
}
