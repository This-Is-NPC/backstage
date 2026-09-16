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
	workerNumCPU     = budget.NumCPU
	workerMemAvail   = budget.ReadMemAvailable
	workerRSSBytes   = uint64(workerRSS)
	workerCost       = workerCPUCost
	minChunkFramesFn = minChunkFrames
	testChunks       [][2]int
	failAtFrame      func(chunk, n int) error
	observeCommand   func(name string, args []string)
)

type chunkRange struct {
	Index, First, End int
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

func resolveWorkers(cfgWorkers, frames, fps int) (int, error) {
	w := cfgWorkers
	if w == 0 {
		avail, err := workerMemAvail()
		if err != nil {
			return 0, err
		}
		w = max(1, min(workerNumCPU()/workerCost, int(budget.MemoryBudget(avail)/workerRSSBytes), frames/minChunkFramesFn(fps)))
	}
	return min(max(w, 1), frames), nil
}

func splitChunks(first, end, workers int) []chunkRange {
	if testChunks != nil {
		out := make([]chunkRange, len(testChunks))
		for i, c := range testChunks {
			out[i] = chunkRange{Index: i, First: c[0], End: c[1]}
		}
		return out
	}
	n := end - first
	if n < 1 {
		return nil
	}
	if workers < 1 {
		workers = 1
	}
	if workers > n {
		workers = n
	}
	out := make([]chunkRange, workers)
	base := n / workers
	rem := n % workers
	at := first
	for i := 0; i < workers; i++ {
		size := base
		if i < rem {
			size++
		}
		out[i] = chunkRange{Index: i, First: at, End: at + size}
		at += size
	}
	return out
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
