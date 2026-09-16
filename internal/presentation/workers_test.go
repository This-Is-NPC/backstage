package presentation

import (
	"errors"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/budget"
)

func mustWorkers(t *testing.T, cfg, frames, fps int) int {
	t.Helper()
	w, err := resolveWorkers(cfg, frames, fps)
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
	if got := mustWorkers(t, 3, 90, 30); got != 3 {
		t.Fatalf("explicit 3: %d", got)
	}
	if got := mustWorkers(t, 8, 5, 30); got != 5 {
		t.Fatalf("cap to frames: %d", got)
	}
	if got := mustWorkers(t, -3, 90, 30); got != 1 {
		t.Fatalf("negative: %d", got)
	}
}

func TestResolveWorkersAutoCPU(t *testing.T) {
	resetSeams()
	defer resetSeams()
	workerNumCPU = func() int { return 8 }
	workerMemAvail = func() (uint64, error) { return 100 << 30, nil }
	minChunkFramesFn = func(int) int { return 1 }
	if got := mustWorkers(t, 0, 90, 30); got != 4 {
		t.Fatalf("8 cpu / 2: %d", got)
	}
	workerNumCPU = func() int { return 1 }
	if got := mustWorkers(t, 0, 90, 30); got != 1 {
		t.Fatalf("1 cpu: %d", got)
	}
}

func TestResolveWorkersAutoMemory(t *testing.T) {
	resetSeams()
	defer resetSeams()
	workerNumCPU = func() int { return 12 }
	minChunkFramesFn = func(int) int { return 1 }
	workerRSSBytes = 2 << 30
	workerMemAvail = func() (uint64, error) { return 10 << 30, nil }
	if budget.MemoryBudget(10<<30) != 8<<30 {
		t.Fatal("budget")
	}
	if got := mustWorkers(t, 0, 90, 30); got != 4 {
		t.Fatalf("8GiB / 2GiB: %d", got)
	}
	workerMemAvail = func() (uint64, error) { return budget.HostMemoryReserve, nil }
	if got := mustWorkers(t, 0, 90, 30); got != 1 {
		t.Fatalf("reserve only: %d", got)
	}
}

func TestResolveWorkersAutoFrames(t *testing.T) {
	resetSeams()
	defer resetSeams()
	workerNumCPU = func() int { return 12 }
	workerMemAvail = func() (uint64, error) { return 100 << 30, nil }
	minChunkFramesFn = minChunkFrames
	if got := mustWorkers(t, 0, 96, 12); got != 4 {
		t.Fatalf("96/24: %d", got)
	}
	if got := mustWorkers(t, 0, 4, 10); got != 1 {
		t.Fatalf("short film: %d", got)
	}
}

func TestResolveWorkersMemAvailError(t *testing.T) {
	resetSeams()
	defer resetSeams()
	boom := errors.New("meminfo: boom")
	workerMemAvail = func() (uint64, error) { return 0, boom }
	w, err := resolveWorkers(0, 90, 30)
	if w != 0 || !errors.Is(err, boom) {
		t.Fatalf("auto w=%d err=%v", w, err)
	}
	w, err = resolveWorkers(3, 90, 30)
	if err != nil || w != 3 {
		t.Fatalf("explicit still works: w=%d err=%v", w, err)
	}
}

func TestSplitChunksCoversRange(t *testing.T) {
	resetSeams()
	defer resetSeams()
	got := splitChunks(10, 20, 3)
	want := []chunkRange{{0, 10, 14}, {1, 14, 17}, {2, 17, 20}}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%d: %+v want %+v", i, got[i], want[i])
		}
	}
	got = splitChunks(0, 4, 1)
	if len(got) != 1 || got[0] != (chunkRange{0, 0, 4}) {
		t.Fatalf("%+v", got)
	}
}

func TestSplitChunksTestSeam(t *testing.T) {
	resetSeams()
	defer resetSeams()
	testChunks = [][2]int{{0, 3}, {3, 7}}
	got := splitChunks(0, 90, 4)
	if len(got) != 2 || got[0] != (chunkRange{0, 0, 3}) || got[1] != (chunkRange{1, 3, 7}) {
		t.Fatalf("%+v", got)
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
