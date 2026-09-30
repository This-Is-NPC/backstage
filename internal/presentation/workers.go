package presentation

import (
	"fmt"
	"path/filepath"
	"sort"

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

// libx264 omits the B-frame DTS delay from shorter encodes, and concatenating
// segments with and without that delay yields irregular timestamps.
const minSegmentFrames = 3

func minChunkFrames(fps int) int {
	return max(16, 2*fps)
}

func planWorkers(p *Plan) int {
	if p == nil || p.Project == nil {
		return 0
	}
	return p.Project.Render.Workers
}

// resolveWorkers sizes the automatic pool by missed frames rather than missed
// chunks: each worker starts its own Chromium, and short chunks cut at
// transitions and captions do not add work that another Chromium can absorb.
func resolveWorkers(cfgWorkers, nMiss, missFrames, fps int) (int, error) {
	if nMiss < 1 {
		return 0, nil
	}
	w := cfgWorkers
	if w == 0 {
		avail, err := workerMemAvail()
		if err != nil {
			return 0, err
		}
		size := minChunkFrames(fps)
		w = max(1, min(workerNumCPU()/workerCost, int(budget.MemoryBudget(avail)/workerRSSBytes), (missFrames+size-1)/size))
	}
	return min(max(w, 1), nMiss), nil
}

// splitByFrames cuts jobs into workers contiguous slices whose largest frame
// total is as small as possible.
func splitByFrames(jobs []chunkJob, workers int) [][2]int {
	if len(jobs) == 0 {
		return nil
	}
	workers = min(max(workers, 1), len(jobs))
	sums := make([]int, len(jobs)+1)
	lo := 0
	for i, j := range jobs {
		sums[i+1] = sums[i] + j.End - j.First
		lo = max(lo, j.End-j.First)
	}
	frames := func(s [2]int) int { return sums[s[1]] - sums[s[0]] }
	greedy := func(limit int) [][2]int {
		var out [][2]int
		at := 0
		for i := 1; i < len(jobs); i++ {
			if frames([2]int{at, i + 1}) > limit {
				out = append(out, [2]int{at, i})
				at = i
			}
		}
		return append(out, [2]int{at, len(jobs)})
	}
	for hi := sums[len(jobs)]; lo < hi; {
		mid := (lo + hi) / 2
		if len(greedy(mid)) <= workers {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	out := greedy(lo)
	for len(out) < workers {
		at := -1
		for i, s := range out {
			if s[1]-s[0] > 1 && (at < 0 || frames(s) > frames(out[at])) {
				at = i
			}
		}
		s := out[at]
		out = append(out[:at+1], out[at:]...)
		out[at], out[at+1] = [2]int{s[0], s[1] - 1}, [2]int{s[1] - 1, s[1]}
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
		var cuts []int
		addCut := func(at float64) {
			cut, _ := intervalFrames(at, at, p.FPS)
			for float64(cut)/float64(p.FPS) < at {
				cut++
			}
			if cut > evFirst && cut < evEnd {
				cuts = append(cuts, cut)
			}
		}
		if i > 0 && e.Transition.Duration > 0 {
			addCut(e.At + e.Transition.Duration)
		}
		for _, span := range p.Text {
			if span.At >= span.End {
				continue
			}
			for _, at := range [...]float64{span.At, span.End} {
				addCut(at)
			}
		}
		sort.Ints(cuts)
		for lo := evFirst; lo < evEnd; lo += size {
			hi := min(lo+size, evEnd)
			bounds := []int{lo}
			for _, cut := range cuts {
				if cut-bounds[len(bounds)-1] >= minSegmentFrames && hi-cut >= minSegmentFrames {
					bounds = append(bounds, cut)
				}
			}
			bounds = append(bounds, hi)
			var pieces [][2]int
			for k := 0; k+1 < len(bounds); k++ {
				if a, b := max(bounds[k], first), min(bounds[k+1], end); a < b {
					pieces = append(pieces, [2]int{a, b})
				}
			}
			if len(pieces) > 1 && pieces[0][1]-pieces[0][0] < minSegmentFrames {
				pieces[1][0] = pieces[0][0]
				pieces = pieces[1:]
			}
			if n := len(pieces); n > 1 && pieces[n-1][1]-pieces[n-1][0] < minSegmentFrames {
				pieces[n-2][1] = pieces[n-1][1]
				pieces = pieces[:n-1]
			}
			for _, pc := range pieces {
				out = append(out, chunkRange{Index: idx, First: pc[0], End: pc[1], Event: i})
				idx++
			}
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
