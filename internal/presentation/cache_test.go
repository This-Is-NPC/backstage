package presentation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "backstage-rcache-")
	if err != nil {
		panic(err)
	}
	renderCacheRoot = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func useTempCache(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	prev := renderCacheRoot
	renderCacheRoot = root
	t.Cleanup(func() { renderCacheRoot = prev })
	return root
}

func openTestCache(t *testing.T) *renderCache {
	t.Helper()
	useTempCache(t)
	c, err := openRenderCache(context.Background(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func tinyClip(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required")
	}
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if err := run(context.Background(), "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=blue:s=160x90:d=1:r=10", "-pix_fmt", "yuv420p", path); err != nil {
		t.Fatal(err)
	}
	return path
}

func tinyCompiled(t *testing.T, clip string) CompiledTrack {
	t.Helper()
	return CompiledTrack{
		Track: Track{Segments: []Segment{{From: 0, To: 0.4, Rate: 1}}},
		Media: Media{Path: clip, Duration: 1, HasVideo: true},
	}
}

func listExt(t *testing.T, dir, ext string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ext) && !strings.HasPrefix(e.Name(), ".tmp-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestThreadCountsShareTrackKey(t *testing.T) {
	c := openTestCache(t)
	tr := tinyCompiled(t, tinyClip(t))
	work := t.TempDir()
	if _, _, err := c.prepareTrack(context.Background(), tr, work, "a", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.prepareTrack(context.Background(), tr, t.TempDir(), "b", 10, 8, 3, 1); err != nil {
		t.Fatal(err)
	}
	got := listExt(t, filepath.Join(c.root, "tracks"), ".mkv")
	if len(got) != 1 {
		t.Fatalf("thread counts must share one entry: %v", got)
	}
	if c.hits.Tracks != 1 || c.misses.Tracks != 1 {
		t.Fatalf("hits=%+v misses=%+v", c.hits, c.misses)
	}
}

func TestSameBytesDifferentPathShareKey(t *testing.T) {
	c := openTestCache(t)
	clip := tinyClip(t)
	other := filepath.Join(t.TempDir(), "copy.mp4")
	if err := os.WriteFile(other, mustRead(t, clip), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.prepareTrack(context.Background(), tinyCompiled(t, clip), t.TempDir(), "a", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.prepareTrack(context.Background(), tinyCompiled(t, other), t.TempDir(), "b", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	got := listExt(t, filepath.Join(c.root, "tracks"), ".mkv")
	if len(got) != 1 {
		t.Fatalf("key used path instead of content: %v", got)
	}
}

func TestClipCutFpsInvalidateOnlyThatEntry(t *testing.T) {
	c := openTestCache(t)
	clip := tinyClip(t)
	tr := tinyCompiled(t, clip)
	if _, _, err := c.prepareTrack(context.Background(), tr, t.TempDir(), "a", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	otherPath := filepath.Join(t.TempDir(), "other.mp4")
	if err := run(context.Background(), "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=red:s=160x90:d=1:r=10", "-pix_fmt", "yuv420p", otherPath); err != nil {
		t.Fatal(err)
	}
	other := tinyCompiled(t, otherPath)
	if _, _, err := c.prepareTrack(context.Background(), other, t.TempDir(), "b", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	before := append([]string(nil), listExt(t, filepath.Join(c.root, "tracks"), ".mkv")...)
	if len(before) != 2 {
		t.Fatal(before)
	}
	tr.Segments[0].To = 0.3
	if _, _, err := c.prepareTrack(context.Background(), tr, t.TempDir(), "c", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.prepareTrack(context.Background(), other, t.TempDir(), "d", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	after := listExt(t, filepath.Join(c.root, "tracks"), ".mkv")
	if len(after) != 3 {
		t.Fatalf("cut should add one key, keep the other: %v", after)
	}
	if _, _, err := c.prepareTrack(context.Background(), other, t.TempDir(), "e", 12, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if n := len(listExt(t, filepath.Join(c.root, "tracks"), ".mkv")); n != 4 {
		t.Fatalf("fps should add one key: %d", n)
	}
}

func TestAudioInputInvalidatesOnlyAudio(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required")
	}
	c := openTestCache(t)
	dir := t.TempDir()
	voice := filepath.Join(dir, "a.wav")
	if err := run(context.Background(), "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=1", voice); err != nil {
		t.Fatal(err)
	}
	p := &Plan{Document: Document{Duration: 1}, AudioParts: []AudioPart{{ID: "v", Path: voice, To: 0.5, Rate: 1, Duration: 0.5, Volume: 1}}}
	if _, _, err := c.mix(context.Background(), p, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	tr := tinyCompiled(t, tinyClip(t))
	if _, _, err := c.prepareTrack(context.Background(), tr, t.TempDir(), "t", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	tracksBefore := listExt(t, filepath.Join(c.root, "tracks"), ".mkv")
	voice2 := filepath.Join(dir, "b.wav")
	if err := run(context.Background(), "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000:duration=1", voice2); err != nil {
		t.Fatal(err)
	}
	p.AudioParts[0].Path = voice2
	if _, _, err := c.mix(context.Background(), p, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if n := len(listExt(t, filepath.Join(c.root, "audio"), ".wav")); n != 2 {
		t.Fatalf("audio keys %d", n)
	}
	if got := listExt(t, filepath.Join(c.root, "tracks"), ".mkv"); len(got) != len(tracksBefore) {
		t.Fatalf("track cache changed: %v vs %v", got, tracksBefore)
	}
}

func TestMemoMtimeRereadsContent(t *testing.T) {
	c := openTestCache(t)
	clip := tinyClip(t)
	if _, err := c.hashFile(clip); err != nil {
		t.Fatal(err)
	}
	n := contentHashReads.Load()
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(clip, later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := c.hashFile(clip); err != nil {
		t.Fatal(err)
	}
	if contentHashReads.Load() == n {
		t.Fatal("memo ignored mtime")
	}
}

func TestCorruptMemoDoesNotFailRender(t *testing.T) {
	c := openTestCache(t)
	if err := os.WriteFile(filepath.Join(c.root, "memo.json"), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	c.progress = &log
	tr := tinyCompiled(t, tinyClip(t))
	if _, _, err := c.prepareTrack(context.Background(), tr, t.TempDir(), "t", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log.Bytes(), []byte("memo unreadable")) {
		t.Fatalf("missing warning:\n%s", log.String())
	}
}

func TestInterruptedPrepareLeavesNoEntry(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required")
	}
	c := openTestCache(t)
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	if err := run(context.Background(), "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=1280x720:rate=30:duration=30", "-pix_fmt", "yuv420p", clip); err != nil {
		t.Fatal(err)
	}
	tr := CompiledTrack{Track: Track{Segments: []Segment{{From: 0, To: 8, Rate: 1}}}, Media: Media{Path: clip, Duration: 30, HasVideo: true}}
	work := filepath.Join(t.TempDir(), "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := c.prepareTrack(ctx, tr, work, "track-0", 10, 1, 1, 1)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ents, _ := os.ReadDir(filepath.Join(c.root, "tracks"))
		seenTmp := false
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), ".tmp-") {
				seenTmp = true
			}
		}
		if seenTmp {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("prepare finished before tmp: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("tmp never appeared")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if got := listExt(t, filepath.Join(c.root, "tracks"), ".mkv"); len(got) != 0 {
		t.Fatalf("interrupted render left cache entry: %v", got)
	}
}

func TestPruneSkipsSharedLockAndKeepsLockFile(t *testing.T) {
	c := openTestCache(t)
	tr := tinyCompiled(t, tinyClip(t))
	if _, _, err := c.prepareTrack(context.Background(), tr, t.TempDir(), "t", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	mkv := listExt(t, filepath.Join(c.root, "tracks"), ".mkv")
	if len(mkv) != 1 {
		t.Fatal(mkv)
	}
	rep, err := PruneRenderCache(context.Background(), c.root, CachePruneOptions{MaxSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Removed) != 0 {
		t.Fatalf("prune deleted a locked entry: %+v", rep)
	}
	if _, err = os.Stat(filepath.Join(c.root, "tracks", strings.TrimSuffix(mkv[0], ".mkv")+".lock")); err != nil {
		t.Fatal("prune removed the lock file")
	}
	if _, err = os.Stat(filepath.Join(c.root, "tracks", mkv[0])); err != nil {
		t.Fatal("prune removed the locked entry")
	}
}

func TestPruneNeverDeletesLockAndNextRenderWorks(t *testing.T) {
	c := openTestCache(t)
	tr := tinyCompiled(t, tinyClip(t))
	if _, _, err := c.prepareTrack(context.Background(), tr, t.TempDir(), "t", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	mkv := listExt(t, filepath.Join(c.root, "tracks"), ".mkv")
	if len(mkv) != 1 {
		t.Fatal(mkv)
	}
	key := strings.TrimSuffix(mkv[0], ".mkv")
	lock := filepath.Join(c.root, "tracks", key+".lock")
	rep, err := PruneRenderCache(context.Background(), c.root, CachePruneOptions{MaxSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Removed) != 1 {
		t.Fatalf("prune: %+v", rep)
	}
	if _, err = os.Stat(lock); err != nil {
		t.Fatal("prune deleted <key>.lock")
	}
	c2, err := openRenderCache(context.Background(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if _, _, err = c2.prepareTrack(context.Background(), tr, t.TempDir(), "t", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if c2.misses.Tracks != 1 {
		t.Fatalf("re-prepare after prune: %+v", c2.misses)
	}
}

func TestPruneRemovesOrphansAndMemoStale(t *testing.T) {
	c := openTestCache(t)
	clip := tinyClip(t)
	if _, err := c.hashFile(clip); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(c.root, "tracks", ".tmp-"+strings.Repeat("a", 64)+"-dead.mkv")
	if err := os.WriteFile(orphan, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.root, "tracks", strings.Repeat("a", 64)+".lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(t.TempDir(), "gone.mp4")
	if err := os.WriteFile(gone, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.hashFile(gone); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(clip, later, later); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	rep, err := PruneRenderCache(context.Background(), c.root, CachePruneOptions{MaxSize: DefaultCacheMaxSize})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Orphans) != 1 {
		t.Fatalf("orphans %+v", rep)
	}
	if _, err = os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan remains")
	}
	if rep.MemoPruned < 2 {
		t.Fatalf("memo prune %d want at least gone+mtime", rep.MemoPruned)
	}
}

func TestPruneRemovesWriteMetaTmp(t *testing.T) {
	c := openTestCache(t)
	dir := filepath.Join(c.root, "segments")
	key := strings.Repeat("ab", 32)
	tmp := filepath.Join(dir, ".tmp-"+key+"-deadbeef.meta.json")
	if err := os.WriteFile(tmp, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, key+".lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	rep, err := PruneRenderCache(context.Background(), c.root, CachePruneOptions{MaxSize: DefaultCacheMaxSize})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("segments", filepath.Base(tmp))
	found := false
	for _, o := range rep.Orphans {
		if o == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("orphans %+v want %s", rep.Orphans, want)
	}
	if _, err = os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("meta tmp remains")
	}
}

func TestLockForFillCanceledDoesNotRecompute(t *testing.T) {
	c := openTestCache(t)
	var log bytes.Buffer
	c.progress = &log
	key := strings.Repeat("11", 32)
	lk, err := tryLockExclusive(filepath.Join(c.root, "tracks", key+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	filled := 0
	_, err = c.getOrFill(ctx, "tracks", key, ".mkv", filepath.Join(t.TempDir(), "w.mkv"), "chunk-0", nil, func(string) (map[string]string, error) {
		filled++
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if filled != 0 {
		t.Fatal("fill ran")
	}
	if bytes.Contains(log.Bytes(), []byte("recomputing without cache")) {
		t.Fatalf("log=%s", log.String())
	}
}

func TestNewTakeGenerationChangesKey(t *testing.T) {
	stubMedia(t)
	p := recordingProject(t)
	publishDemo(t, p, "gen-one")
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	c := openTestCache(t)
	h1, err := c.hashFile(plan.Tracks["clip"].Media.Path)
	if err != nil {
		t.Fatal(err)
	}
	_ = plan.Close()
	publishDemo(t, p, "gen-two-different-bytes")
	plan, err = Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	h2, err := c.hashFile(plan.Tracks["clip"].Media.Path)
	if err != nil {
		t.Fatal(err)
	}
	segs := []Segment{{From: 0, To: 0.4, Rate: 1}}
	if trackCacheKey(h1, segs, 10, 1, c.ffmpeg) == trackCacheKey(h2, segs, 10, 1, c.ffmpeg) {
		t.Fatal("new generation kept the old key")
	}
}

func TestConcurrentPrepareSameKey(t *testing.T) {
	root := useTempCache(t)
	clip := tinyClip(t)
	tr := tinyCompiled(t, clip)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := openRenderCache(context.Background(), io.Discard)
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			work, err := os.MkdirTemp("", "work-")
			if err != nil {
				errs <- err
				return
			}
			defer os.RemoveAll(work)
			_, _, err = c.prepareTrack(context.Background(), tr, work, "t", 10, 1, 1, 1)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := len(listExt(t, filepath.Join(root, "tracks"), ".mkv")); n != 1 {
		t.Fatalf("concurrent writers: %d entries", n)
	}
}

func TestReadErrorRecomputes(t *testing.T) {
	c := openTestCache(t)
	tr := tinyCompiled(t, tinyClip(t))
	if _, _, err := c.prepareTrack(context.Background(), tr, t.TempDir(), "a", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	mkv := listExt(t, filepath.Join(c.root, "tracks"), ".mkv")
	if err := os.Remove(filepath.Join(c.root, "tracks", mkv[0])); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	c.progress = &log
	if _, _, err := c.prepareTrack(context.Background(), tr, t.TempDir(), "b", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if c.misses.Tracks < 2 {
		t.Fatalf("missing recompute: %+v log=%s", c.misses, log.String())
	}
}

func TestPruneDryRunJSONShape(t *testing.T) {
	c := openTestCache(t)
	tr := tinyCompiled(t, tinyClip(t))
	if _, _, err := c.prepareTrack(context.Background(), tr, t.TempDir(), "t", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	rep, err := PruneRenderCache(context.Background(), c.root, CachePruneOptions{MaxSize: 1, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.DryRun || len(rep.Removed) != 1 {
		t.Fatalf("%+v", rep)
	}
	if got := listExt(t, filepath.Join(c.root, "tracks"), ".mkv"); len(got) != 1 {
		t.Fatal("dry-run deleted")
	}
}

func TestSharedLockBlocksExclusive(t *testing.T) {
	c := openTestCache(t)
	tr := tinyCompiled(t, tinyClip(t))
	if _, _, err := c.prepareTrack(context.Background(), tr, t.TempDir(), "t", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	mkv := listExt(t, filepath.Join(c.root, "tracks"), ".mkv")
	lock := filepath.Join(c.root, "tracks", strings.TrimSuffix(mkv[0], ".mkv")+".lock")
	f, err := tryLockExclusive(lock)
	if err == nil {
		_ = f.Close()
		t.Fatal("exclusive lock succeeded while render holds LOCK_SH")
	}
}

func failCacheLink(_, _ string) error {
	return errors.New("forced link failure")
}

func TestLinkFailureSharedKeepsEntry(t *testing.T) {
	c := openTestCache(t)
	tr := tinyCompiled(t, tinyClip(t))
	if _, _, err := c.prepareTrack(context.Background(), tr, t.TempDir(), "a", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	mkv := listExt(t, filepath.Join(c.root, "tracks"), ".mkv")
	if len(mkv) != 1 {
		t.Fatal(mkv)
	}
	dest := filepath.Join(c.root, "tracks", mkv[0])
	sum := fileSHA(t, dest)
	prev := linkCacheFile
	linkCacheFile = failCacheLink
	t.Cleanup(func() { linkCacheFile = prev })
	var log bytes.Buffer
	c.progress = &log
	if _, _, err := c.prepareTrack(context.Background(), tr, t.TempDir(), "b", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if c.misses.Tracks != 2 || c.hits.Tracks != 0 {
		t.Fatalf("hits=%+v misses=%+v", c.hits, c.misses)
	}
	if fileSHA(t, dest) != sum {
		t.Fatal("shared-lock link failure changed the cache entry")
	}
	if !bytes.Contains(log.Bytes(), []byte("recomputing without cache")) {
		t.Fatalf("missing warning:\n%s", log.String())
	}
}

func TestLinkFailureExclusiveKeepsValidEntry(t *testing.T) {
	c := openTestCache(t)
	tr := tinyCompiled(t, tinyClip(t))
	if _, _, err := c.prepareTrack(context.Background(), tr, t.TempDir(), "a", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	mkv := listExt(t, filepath.Join(c.root, "tracks"), ".mkv")
	dest := filepath.Join(c.root, "tracks", mkv[0])
	sum := fileSHA(t, dest)
	st, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	ino := fileInode(st)
	prev := linkCacheFile
	linkCacheFile = failCacheLink
	cacheFillExclusive = true
	t.Cleanup(func() {
		linkCacheFile = prev
		cacheFillExclusive = false
	})
	c2, err := openRenderCache(context.Background(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	var log bytes.Buffer
	c2.progress = &log
	if _, _, err = c2.prepareTrack(context.Background(), tr, t.TempDir(), "b", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(dest)
	if err != nil {
		t.Fatal("exclusive lock deleted a valid entry")
	}
	if fileInode(after) != ino || fileSHA(t, dest) != sum {
		t.Fatal("valid entry was replaced")
	}
	if !bytes.Contains(log.Bytes(), []byte("recomputing without cache")) {
		t.Fatalf("missing warning:\n%s", log.String())
	}
}

func TestZeroSizeEntryRepublishes(t *testing.T) {
	c := openTestCache(t)
	tr := tinyCompiled(t, tinyClip(t))
	if _, _, err := c.prepareTrack(context.Background(), tr, t.TempDir(), "a", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	mkv := listExt(t, filepath.Join(c.root, "tracks"), ".mkv")
	dest := filepath.Join(c.root, "tracks", mkv[0])
	if err := os.Chmod(dest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.prepareTrack(context.Background(), tr, t.TempDir(), "b", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dest)
	if err != nil || fi.Size() == 0 {
		t.Fatal("zero-size entry was not republished")
	}
}

func TestPruneCorruptMetaIsLeastUsed(t *testing.T) {
	c := openTestCache(t)
	if _, _, err := c.prepareTrack(context.Background(), tinyCompiled(t, tinyClip(t)), t.TempDir(), "a", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other.mp4")
	if err := run(context.Background(), "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=red:s=160x90:d=1:r=10", "-pix_fmt", "yuv420p", other); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.prepareTrack(context.Background(), tinyCompiled(t, other), t.TempDir(), "b", 10, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	tracks := filepath.Join(c.root, "tracks")
	mkv := listExt(t, tracks, ".mkv")
	if len(mkv) != 2 {
		t.Fatal(mkv)
	}
	keep := mkv[0]
	drop := mkv[1]
	if err := os.WriteFile(filepath.Join(tracks, strings.TrimSuffix(drop, ".mkv")+".meta.json"), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	keepSize := int64(0)
	if fi, err := os.Stat(filepath.Join(tracks, keep)); err == nil {
		keepSize = fi.Size()
	}
	rep, err := PruneRenderCache(context.Background(), c.root, CachePruneOptions{MaxSize: keepSize})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(tracks, drop)); !os.IsNotExist(err) {
		t.Fatalf("corrupt-meta entry kept: %+v", rep)
	}
	if _, err = os.Stat(filepath.Join(tracks, keep)); err != nil {
		t.Fatal("readable-meta entry was removed")
	}
}

func TestCacheKeyJSONOmitsThreadsAndPath(t *testing.T) {
	a := trackCacheKey("abc", []Segment{{From: 0, To: 1, Rate: 1}}, 12, 1, "ffmpeg")
	b := trackCacheKey("abc", []Segment{{From: 0, To: 1, Rate: 1}}, 12, 1, "ffmpeg")
	if a != b {
		t.Fatal(a, b)
	}
	body, _ := json.Marshal(trackKeyV1{
		V: "v1", Kind: "track", Src: "abc",
		Segments: []keySegment{{From: num(0), To: num(1), Rate: num(1)}},
		FPS:      12, FFmpeg: "ffmpeg", Encode: trackEncodeArgs(1),
	})
	if bytes.Contains(body, []byte("thread")) || bytes.Contains(body, []byte("/tmp")) {
		t.Fatalf("key material leaked: %s", body)
	}
}
