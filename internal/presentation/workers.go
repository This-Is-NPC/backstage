package presentation

import (
	"fmt"
	"path/filepath"

	"github.com/This-Is-NPC/backstage/internal/budget"
)

const (
	workerCPUCost = 2
	workerRSS     = 2 << 30
)

var (
	workerNumCPU         = budget.NumCPU
	workerMemAvail       = budget.ReadMemAvailable
	workerRSSBytes       = uint64(workerRSS)
	workerCost           = workerCPUCost
	failAtFrame          func(chunk, n int) error
	observeCommand       func(name string, args []string)
	observeChunkDecoders func(index int, ids []string)
)

type chunkRange struct {
	Index, First, End, Event int
}

func minChunkFrames(fps int) int {
	return max(16, 2*fps)
}

func planWorkers(p *Plan) int {
	if p == nil || p.Project == nil {
		return 0
	}
	return p.Project.Render.Workers
}

func resolveWorkers(cfgWorkers, nMiss int) (int, error) {
	if nMiss < 1 {
		return 0, nil
	}
	w := cfgWorkers
	if w == 0 {
		avail, err := workerMemAvail()
		if err != nil {
			return 0, err
		}
		w = max(1, min(workerNumCPU()/workerCost, int(budget.MemoryBudget(avail)/workerRSSBytes), nMiss))
	}
	return min(max(w, 1), nMiss), nil
}

func splitIndexSlices(n, workers int) [][2]int {
	if n < 1 {
		return nil
	}
	if workers < 1 {
		workers = 1
	}
	if workers > n {
		workers = n
	}
	out := make([][2]int, workers)
	base := n / workers
	rem := n % workers
	at := 0
	for i := 0; i < workers; i++ {
		size := base
		if i < rem {
			size++
		}
		out[i] = [2]int{at, at + size}
		at += size
	}
	return out
}

func planChunks(p *Plan, first, end int) []chunkRange {
	if first >= end {
		return nil
	}
	var out []chunkRange
	idx := 0
	tl := p.Document.Timeline
	size := minChunkFrames(p.FPS)
	for i, e := range tl {
		next := p.Document.Duration
		if i+1 < len(tl) {
			next = tl[i+1].At
		}
		evFirst, evEnd := intervalFrames(e.At, next, p.FPS)
		if evFirst >= evEnd {
			continue
		}
		for lo := evFirst; lo < evEnd; lo += size {
			hi := min(lo+size, evEnd)
			cFirst := max(lo, first)
			cEnd := min(hi, end)
			if cFirst >= cEnd {
				continue
			}
			out = append(out, chunkRange{Index: idx, First: cFirst, End: cEnd, Event: i})
			idx++
		}
	}
	return out
}

func chunkTransition(p *Plan, ch chunkRange) bool {
	if ch.Event <= 0 || ch.Event >= len(p.Document.Timeline) {
		return false
	}
	e := p.Document.Timeline[ch.Event]
	if e.Transition.Duration <= 0 {
		return false
	}
	limit := e.At + e.Transition.Duration
	for n := ch.First; n < ch.End; n++ {
		if float64(n)/float64(p.FPS) < limit {
			return true
		}
	}
	return false
}

func chunkEncodeThreads(encode, workers int) int {
	if workers < 1 {
		workers = 1
	}
	return max(1, encode/workers)
}

func chunkVideoPath(work string, nChunks, index int) string {
	if nChunks == 1 {
		return filepath.Join(work, "video.mp4")
	}
	return filepath.Join(work, fmt.Sprintf("chunk-%d.mp4", index))
}
