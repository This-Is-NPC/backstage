package presentation

import (
	"errors"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/budget"
)

func mustWorkers(t *testing.T, cfg, nMiss int) int {
	t.Helper()
	w, err := resolveWorkers(cfg, nMiss, nMiss*minChunkFrames(10), 10)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestMinChunkFrames(t *testing.T) {
	if minChunkFrames(10) != 20 {
		t.Fatalf("10fps: %d", minChunkFrames(10))
	}
	if minChunkFrames(12) != 24 {
		t.Fatalf("12fps: %d", minChunkFrames(12))
	}
	if minChunkFrames(1) != 16 {
		t.Fatalf("1fps: %d", minChunkFrames(1))
	}
}

func TestResolveWorkersExplicit(t *testing.T) {
	resetSeams()
	defer resetSeams()
	if got := mustWorkers(t, 3, 90); got != 3 {
		t.Fatalf("explicit 3: %d", got)
	}
	if got := mustWorkers(t, 8, 5); got != 5 {
		t.Fatalf("cap to misses: %d", got)
	}
	if got := mustWorkers(t, -3, 90); got != 1 {
		t.Fatalf("negative: %d", got)
	}
	if got := mustWorkers(t, 3, 0); got != 0 {
		t.Fatalf("no misses: %d", got)
	}
}

func TestResolveWorkersAutoCPU(t *testing.T) {
	resetSeams()
	defer resetSeams()
	workerNumCPU = func() int { return 8 }
	workerMemAvail = func() (uint64, error) { return 100 << 30, nil }
	if got := mustWorkers(t, 0, 90); got != 4 {
		t.Fatalf("8 cpu / 2: %d", got)
	}
	workerNumCPU = func() int { return 1 }
	if got := mustWorkers(t, 0, 90); got != 1 {
		t.Fatalf("1 cpu: %d", got)
	}
}

func TestResolveWorkersAutoMemory(t *testing.T) {
	resetSeams()
	defer resetSeams()
	workerNumCPU = func() int { return 12 }
	workerRSSBytes = 2 << 30
	workerMemAvail = func() (uint64, error) { return 10 << 30, nil }
	if budget.MemoryBudget(10<<30) != 8<<30 {
		t.Fatal("budget")
	}
	if got := mustWorkers(t, 0, 90); got != 4 {
		t.Fatalf("8GiB / 2GiB: %d", got)
	}
	workerMemAvail = func() (uint64, error) { return budget.HostMemoryReserve, nil }
	if got := mustWorkers(t, 0, 90); got != 1 {
		t.Fatalf("reserve only: %d", got)
	}
}

func TestResolveWorkersAutoMisses(t *testing.T) {
	resetSeams()
	defer resetSeams()
	workerNumCPU = func() int { return 12 }
	workerMemAvail = func() (uint64, error) { return 100 << 30, nil }
	if got := mustWorkers(t, 0, 4); got != 4 {
		t.Fatalf("cap to misses: %d", got)
	}
	if got := mustWorkers(t, 0, 1); got != 1 {
		t.Fatalf("one miss: %d", got)
	}
}

func TestResolveWorkersAutoFrames(t *testing.T) {
	resetSeams()
	defer resetSeams()
	workerNumCPU = func() int { return 12 }
	workerMemAvail = func() (uint64, error) { return 100 << 30, nil }
	for _, tc := range []struct{ nMiss, frames, want int }{
		{7, 96, 4},
		{7, 72, 3},
		{7, 73, 4},
		{2, 5, 1},
	} {
		w, err := resolveWorkers(0, tc.nMiss, tc.frames, 12)
		if err != nil || w != tc.want {
			t.Fatalf("%d misses, %d frames: w=%d err=%v want %d", tc.nMiss, tc.frames, w, err, tc.want)
		}
	}
	if w, err := resolveWorkers(7, 7, 72, 12); err != nil || w != 7 {
		t.Fatalf("explicit ignores frames: w=%d err=%v", w, err)
	}
}

func TestResolveWorkersMemAvailError(t *testing.T) {
	resetSeams()
	defer resetSeams()
	boom := errors.New("meminfo: boom")
	workerMemAvail = func() (uint64, error) { return 0, boom }
	w, err := resolveWorkers(0, 90, 900, 10)
	if w != 0 || !errors.Is(err, boom) {
		t.Fatalf("auto w=%d err=%v", w, err)
	}
	w, err = resolveWorkers(3, 90, 900, 10)
	if err != nil || w != 3 {
		t.Fatalf("explicit still works: w=%d err=%v", w, err)
	}
}

func TestSplitByFrames(t *testing.T) {
	jobs := func(sizes ...int) []chunkJob {
		var out []chunkJob
		at := 0
		for i, n := range sizes {
			out = append(out, chunkJob{chunkRange: chunkRange{Index: i, First: at, End: at + n}})
			at += n
		}
		return out
	}
	for _, tc := range []struct {
		sizes   []int
		workers int
		want    [][2]int
	}{
		{[]int{12, 12, 6, 18, 6, 18, 24}, 4, [][2]int{{0, 2}, {2, 4}, {4, 6}, {6, 7}}},
		{[]int{6, 6, 6, 6, 6, 18, 24}, 3, [][2]int{{0, 4}, {4, 6}, {6, 7}}},
		{[]int{24, 24, 24, 24}, 4, [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 4}}},
		{[]int{5, 15, 20}, 3, [][2]int{{0, 1}, {1, 2}, {2, 3}}},
		{[]int{10, 10, 10, 10}, 1, [][2]int{{0, 4}}},
		{[]int{10, 10}, 5, [][2]int{{0, 1}, {1, 2}}},
	} {
		got := splitByFrames(jobs(tc.sizes...), tc.workers)
		if len(got) != len(tc.want) {
			t.Fatalf("%v/%d: got %v want %v", tc.sizes, tc.workers, got, tc.want)
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Fatalf("%v/%d: got %v want %v", tc.sizes, tc.workers, got, tc.want)
			}
		}
	}
	if got := splitByFrames(nil, 3); got != nil {
		t.Fatalf("no jobs: %v", got)
	}
}

func TestPlanChunksEventCuts(t *testing.T) {
	p := &Plan{FPS: 10, Document: Document{Duration: 8, Timeline: []Event{{At: 0}, {At: 2}, {At: 6}}}}
	got := planChunks(p, 0, 80)
	want := []chunkRange{
		{0, 0, 20, 0},
		{1, 20, 40, 1},
		{2, 40, 60, 1},
		{3, 60, 80, 2},
	}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%d: %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestPlanChunksAnchoredThenClipped(t *testing.T) {
	p := &Plan{FPS: 10, Document: Document{Duration: 6, Timeline: []Event{{At: 0}}}}
	full := planChunks(p, 0, 60)
	part := planChunks(p, 0, 50)
	if len(full) != 3 || len(part) != 3 {
		t.Fatalf("full=%+v part=%+v", full, part)
	}
	if full[0] != part[0] || full[1] != part[1] {
		t.Fatalf("interior pieces must share bounds: full=%+v part=%+v", full, part)
	}
	if part[2].First != 40 || part[2].End != 50 || full[2].End != 60 {
		t.Fatalf("partial tail: %+v vs %+v", part[2], full[2])
	}
	mid := planChunks(p, 10, 50)
	if len(mid) != 3 || mid[0] != (chunkRange{0, 10, 20, 0}) || mid[1] != full[1] || mid[2] != (chunkRange{2, 40, 50, 0}) {
		t.Fatalf("mid preview %+v", mid)
	}
}

func TestPlanChunksCaptionVisibilityCuts(t *testing.T) {
	p := &Plan{
		FPS:      10,
		Document: Document{Duration: 4, Timeline: []Event{{At: 0}, {At: 2}}},
		Text: []TextSpan{
			{At: 0.5, End: 1.05, Text: "first"},
			{At: 1.05, End: 2.5, Text: "second"},
			{At: 3.3, End: 3.3, Text: "invisible"},
		},
	}
	want := []chunkRange{
		{0, 0, 5, 0},
		{1, 5, 11, 0},
		{2, 11, 20, 0},
		{3, 20, 25, 1},
		{4, 25, 40, 1},
	}
	got := planChunks(p, 0, 40)
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chunk %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
	preview := planChunks(p, 7, 30)
	if len(preview) != 4 || preview[0] != (chunkRange{0, 7, 11, 0}) || preview[1] != (chunkRange{1, 11, 20, 0}) || preview[2] != (chunkRange{2, 20, 25, 1}) || preview[3] != (chunkRange{3, 25, 30, 1}) {
		t.Fatalf("preview %+v", preview)
	}
}

func TestPlanChunksCaptionBoundaryAfterRoundedFrame(t *testing.T) {
	p := &Plan{
		FPS:      10,
		Document: Document{Duration: 4, Timeline: []Event{{At: 0}}},
		Text:     []TextSpan{{At: 1.00000000001, End: 2, Text: "caption"}},
	}
	got := planChunks(p, 0, 40)
	want := []chunkRange{{0, 0, 11, 0}, {1, 11, 20, 0}, {2, 20, 40, 0}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chunk %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestPlanChunksNoShortCutPieces(t *testing.T) {
	p := &Plan{
		FPS:      10,
		Document: Document{Duration: 4, Timeline: []Event{{At: 0}}},
		Text: []TextSpan{
			{At: 1, End: 1.1, Text: "short"},
			{At: 1.9, End: 3, Text: "late"},
		},
	}
	for _, tc := range []struct {
		first, end int
		want       []chunkRange
	}{
		{0, 40, []chunkRange{{0, 0, 10, 0}, {1, 10, 20, 0}, {2, 20, 30, 0}, {3, 30, 40, 0}}},
		{9, 31, []chunkRange{{0, 9, 20, 0}, {1, 20, 31, 0}}},
	} {
		got := planChunks(p, tc.first, tc.end)
		if len(got) != len(tc.want) {
			t.Fatalf("[%d,%d) got %+v, want %+v", tc.first, tc.end, got, tc.want)
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Fatalf("[%d,%d) chunk %d: got %+v, want %+v", tc.first, tc.end, i, got[i], tc.want[i])
			}
		}
	}
}

func TestChunkEncodeThreads(t *testing.T) {
	if chunkEncodeThreads(8, 2) != 4 {
		t.Fatal(chunkEncodeThreads(8, 2))
	}
	if chunkEncodeThreads(3, 4) != 1 {
		t.Fatal(chunkEncodeThreads(3, 4))
	}
}

func TestChunkVideoPath(t *testing.T) {
	if got := chunkVideoPath("/tmp/work", 1, 0); got != "/tmp/work/video.mp4" {
		t.Fatal(got)
	}
	if got := chunkVideoPath("/tmp/work", 2, 1); got != "/tmp/work/chunk-1.mp4" {
		t.Fatal(got)
	}
}

func TestChunkTransition(t *testing.T) {
	p := &Plan{FPS: 10, Document: Document{Duration: 8, Timeline: []Event{
		{At: 0},
		{At: 2, Transition: Transition{Effect: "fade", Duration: 0.5}},
		{At: 6, Transition: Transition{Effect: "morph", Duration: 0.5}},
	}}}
	chunks := planChunks(p, 0, 80)
	want := []chunkRange{{0, 0, 20, 0}, {1, 20, 25, 1}, {2, 25, 40, 1}, {3, 40, 60, 1}, {4, 60, 65, 2}, {5, 65, 80, 2}}
	if len(chunks) != len(want) {
		t.Fatalf("got %+v, want %+v", chunks, want)
	}
	for i := range want {
		if chunks[i] != want[i] || chunkTransition(p, chunks[i]) != (i == 1 || i == 4) {
			t.Fatalf("chunk %d: %+v, transition=%v", i, chunks[i], chunkTransition(p, chunks[i]))
		}
	}
}

func TestPlanChunksTransitionAndCaptionCuts(t *testing.T) {
	p := &Plan{
		FPS: 10,
		Document: Document{Duration: 4, Timeline: []Event{
			{At: 0},
			{At: 2, Transition: Transition{Effect: "fade", Duration: 0.5}},
		}},
		Text: []TextSpan{{At: 2.5, End: 2.8, Text: "caption"}},
	}
	got := planChunks(p, 0, 40)
	want := []chunkRange{{0, 0, 20, 0}, {1, 20, 25, 1}, {2, 25, 28, 1}, {3, 28, 40, 1}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chunk %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}
