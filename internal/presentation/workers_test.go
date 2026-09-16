package presentation

import (
	"errors"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/budget"
)

func mustWorkers(t *testing.T, cfg, nMiss int) int {
	t.Helper()
	w, err := resolveWorkers(cfg, nMiss)
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

func TestResolveWorkersMemAvailError(t *testing.T) {
	resetSeams()
	defer resetSeams()
	boom := errors.New("meminfo: boom")
	workerMemAvail = func() (uint64, error) { return 0, boom }
	w, err := resolveWorkers(0, 90)
	if w != 0 || !errors.Is(err, boom) {
		t.Fatalf("auto w=%d err=%v", w, err)
	}
	w, err = resolveWorkers(3, 90)
	if err != nil || w != 3 {
		t.Fatalf("explicit still works: w=%d err=%v", w, err)
	}
}

func TestSplitIndexSlices(t *testing.T) {
	got := splitIndexSlices(10, 3)
	want := [][2]int{{0, 4}, {4, 7}, {7, 10}}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%d: %+v want %+v", i, got[i], want[i])
		}
	}
	got = splitIndexSlices(4, 1)
	if len(got) != 1 || got[0] != [2]int{0, 4} {
		t.Fatalf("%+v", got)
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
	if chunkTransition(p, chunks[0]) || !chunkTransition(p, chunks[1]) || chunkTransition(p, chunks[2]) || !chunkTransition(p, chunks[3]) {
		t.Fatalf("%+v t0=%v t1=%v t2=%v t3=%v", chunks, chunkTransition(p, chunks[0]), chunkTransition(p, chunks[1]), chunkTransition(p, chunks[2]), chunkTransition(p, chunks[3]))
	}
}
