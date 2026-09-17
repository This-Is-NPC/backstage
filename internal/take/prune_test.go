package take

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestPruneAbandonedPendingWithoutFactsDeletes(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	pending := s.dir
	closeFile(s.lease)
	s.lease = nil
	s.finished = true
	got, err := Prune(context.Background(), p, PruneOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Removed) != 1 || got.Removed[0] != pending {
		t.Fatalf("removed: %v", got.Removed)
	}
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		t.Fatal("pending without facts should be gone")
	}
	if _, err := os.Stat(paths.attemptsDir()); !os.IsNotExist(err) {
		if entries, _ := os.ReadDir(paths.attemptsDir()); len(entries) != 0 {
			t.Fatalf("should not create an attempt: %v", entries)
		}
	}
}

func TestPruneAbandonedPendingWithFactsMovesToAttempts(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "keep")
	closeFile(s.lease)
	s.lease = nil
	s.finished = true
	got, err := Prune(context.Background(), p, PruneOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Moved) != 1 {
		t.Fatalf("moved: %v", got.Moved)
	}
	if _, err := os.Stat(filepath.Join(got.Moved[0], factsName)); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(paths.sceneDir()); pendingLeft(entries) {
		t.Fatalf("pending should have moved: %v", entries)
	}
}

func TestPruneSkipsPendingInUse(t *testing.T) {
	p, _ := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Discard()
	got, err := Prune(context.Background(), p, PruneOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Skipped) != 1 {
		t.Fatalf("skipped: %v", got.Skipped)
	}
	if _, err := os.Stat(s.dir); err != nil {
		t.Fatal("in-use pending was removed")
	}
}

func TestPruneSkipsLeasedGeneration(t *testing.T) {
	p, _ := testProject(t)
	first, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, first, "old")
	pub1, err := first.Publish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, second, "new")
	if _, err := second.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	lease, err := os.Open(filepath.Join(filepath.Dir(pub1.Clip), leaseName))
	if err != nil {
		t.Fatal(err)
	}
	if err := flock(lease, syscall.LOCK_SH); err != nil {
		t.Fatal(err)
	}
	defer closeFile(lease)
	got, err := Prune(context.Background(), p, PruneOptions{OlderThan: time.Nanosecond, Now: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range got.Removed {
		if path == filepath.Dir(pub1.Clip) {
			t.Fatalf("removed leased generation: %v", got.Removed)
		}
	}
	if _, err := os.Stat(pub1.Clip); err != nil {
		t.Fatal("leased generation missing")
	}
	if len(got.Skipped) == 0 {
		t.Fatal("expected the leased orphan to be skipped")
	}
}

func TestPruneAgeUsesIDNotMtime(t *testing.T) {
	p, paths := testProject(t)
	oldID := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC).Format(idLayout) + "-aaaa"
	orphan := paths.generationDir(oldID)
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, clipName), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, factsName), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, leaseName), []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(orphan, now, now); err != nil {
		t.Fatal(err)
	}

	got, err := Prune(context.Background(), p, PruneOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Removed) != 0 {
		t.Fatalf("no-flag prune removed an orphan generation: %v", got.Removed)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal(err)
	}

	got, err = Prune(context.Background(), p, PruneOptions{OlderThan: 24 * time.Hour, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, path := range got.Removed {
		if path == orphan {
			found = true
		}
	}
	if !found {
		t.Fatalf("old id should be removed by age, not mtime: %v", got.Removed)
	}
}

func TestPruneRepairsDivergentProjection(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "src")
	if _, err := s.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StableClip(), []byte("torn"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Prune(context.Background(), p, PruneOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Repaired) != 1 {
		t.Fatalf("repaired: %v", got.Repaired)
	}
	body, err := os.ReadFile(paths.StableClip())
	if err != nil || string(body) != "src-clip" {
		t.Fatalf("projection: %s %v", body, err)
	}
}

func TestPruneNeverRemovesManifestGeneration(t *testing.T) {
	p, _ := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "keep")
	pub, err := s.Publish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := Prune(context.Background(), p, PruneOptions{OlderThan: time.Nanosecond, MaxSize: 1, Now: time.Now().Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range got.Removed {
		if path == filepath.Dir(pub.Clip) {
			t.Fatalf("removed current generation: %v", got.Removed)
		}
	}
	if _, err := os.Stat(pub.Clip); err != nil {
		t.Fatal(err)
	}
}

func pendingLeft(entries []os.DirEntry) bool {
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), pendingPrefix) {
			return true
		}
	}
	return false
}

func TestPruneInvalidManifestAbortsScene(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "ok")
	if _, err := s.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	oldID := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC).Format(idLayout) + "-aaaa"
	orphan := paths.generationDir(oldID)
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, clipName), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	other, err := Begin(p, "other")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other.Clip(), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	closeFile(other.lease)
	other.lease = nil
	other.finished = true
	if err := os.WriteFile(paths.Manifest(), []byte(`{"version":9}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Prune(context.Background(), p, PruneOptions{OlderThan: time.Nanosecond, Now: time.Now().Add(time.Hour)})
	if err == nil || !strings.Contains(err.Error(), "demo") {
		t.Fatalf("invalid manifest should list the skipped scene: %v", err)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal("invalid manifest prune must not remove generations")
	}
	if _, err := os.Stat(other.dir); !os.IsNotExist(err) {
		t.Fatalf("other scene should still be pruned: %v", got)
	}
}

func TestPruneUnreadableManifestAbortsScene(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "ok")
	if _, err := s.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	oldID := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC).Format(idLayout) + "-aaaa"
	orphan := paths.generationDir(oldID)
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, clipName), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths.Manifest(), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(paths.Manifest(), 0o644) })
	_, err = Prune(context.Background(), p, PruneOptions{OlderThan: time.Nanosecond, Now: time.Now().Add(time.Hour)})
	if err == nil {
		t.Fatal("unreadable manifest should abort prune")
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal("unreadable manifest prune must not remove generations")
	}
}

func TestPrunePendingWithClipMovesWithoutFacts(t *testing.T) {
	p, _ := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Clip(), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	closeFile(s.lease)
	s.lease = nil
	s.finished = true
	got, err := Prune(context.Background(), p, PruneOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Moved) != 1 {
		t.Fatalf("moved: %v", got.Moved)
	}
	if _, err := os.Stat(filepath.Join(got.Moved[0], clipName)); err != nil {
		t.Fatal(err)
	}
}

func TestPruneMaxSizeSelectsByAgeAcrossScenes(t *testing.T) {
	dir := t.TempDir()
	p := &scene.Project{Dir: dir, Record: scene.RecordCfg{Out: "recordings"}}
	oldID := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC).Format(idLayout) + "-aaaa"
	zeta, err := ForScene(p, "zeta")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(zeta.attemptDir(oldID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zeta.attemptDir(oldID), clipName), bytes.Repeat([]byte("z"), 200), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := Begin(p, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, first, "old-alpha")
	pub1, err := first.Publish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Begin(p, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, second, "new-alpha")
	if _, err := second.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	opts := PruneOptions{MaxSize: 150, Now: time.Now()}
	dry, err := Prune(context.Background(), p, PruneOptions{MaxSize: opts.MaxSize, Now: opts.Now, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(zeta.attemptDir(oldID)); err != nil {
		t.Fatal("dry-run must not remove the old attempt")
	}
	if _, err := os.Stat(filepath.Dir(pub1.Clip)); err != nil {
		t.Fatal("dry-run must not remove the newer alpha orphan")
	}
	got, err := Prune(context.Background(), p, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !samePathSet(dry.Removed, got.Removed) {
		t.Fatalf("dry-run removed %v, real removed %v", dry.Removed, got.Removed)
	}
	if _, err := os.Stat(zeta.attemptDir(oldID)); !os.IsNotExist(err) {
		t.Fatalf("old zeta attempt should be removed first: %v", got.Removed)
	}
	if _, err := os.Stat(filepath.Dir(pub1.Clip)); err != nil {
		t.Fatal("newer alpha orphan was removed before the older zeta attempt")
	}
	if _, err := os.Stat(filepath.Join(dir, "recordings", "alpha.mp4")); err != nil {
		t.Fatal("current alpha take was removed")
	}
}

func TestPruneDryRunMatchesRealAndCreatesNothing(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Clip(), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	closeFile(s.lease)
	s.lease = nil
	s.finished = true
	tmp := filepath.Join(paths.OutDir, ".demo.mp4.abcd.tmp")
	if err := os.WriteFile(tmp, []byte("tmp"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(paths.sceneDir())
	if err != nil {
		t.Fatal(err)
	}
	dry, err := Prune(context.Background(), p, PruneOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadDir(paths.sceneDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatal("dry-run changed the scene directory")
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Fatal("dry-run removed a temp")
	}
	real, err := Prune(context.Background(), p, PruneOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !samePathSet(dry.Moved, real.Moved) || !samePathSet(dry.Removed, real.Removed) {
		t.Fatalf("dry-run %v vs real %v", dry, real)
	}
}

func samePathSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string{}, a...)
	bs := append([]string{}, b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func TestPruneDryRunReadOnlyOutput(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "ok")
	if _, err := s.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	chmodTree(t, paths.OutDir, 0o555)
	t.Cleanup(func() { chmodTree(t, paths.OutDir, 0o755) })
	if _, err := Prune(context.Background(), p, PruneOptions{DryRun: true}); err != nil {
		t.Fatal(err)
	}
}

func TestPruneHonorsContext(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "ok")
	if _, err := s.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	held, err := createLock(paths.lockPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := flock(held, syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer closeFile(held)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := Prune(ctx, p, PruneOptions{}); err == nil {
		t.Fatal("expected context cancel while waiting for the lock")
	}
}

func TestPruneRemovesAbandonedTemps(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "ok")
	if _, err := s.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(paths.OutDir, ".demo.take.json.xxxx.tmp")
	if err := os.WriteFile(tmp, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Prune(context.Background(), p, PruneOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, path := range got.Removed {
		if path == tmp {
			found = true
		}
	}
	if !found {
		t.Fatalf("temp not removed: %v", got.Removed)
	}
}

func TestPruneDryRunDoesNotCreateLock(t *testing.T) {
	p, paths := testProject(t)
	if err := os.MkdirAll(paths.sceneDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Prune(context.Background(), p, PruneOptions{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.lockPath()); !os.IsNotExist(err) {
		t.Fatal("dry-run created a lock file")
	}
}

func TestPruneEscapingSceneDirAborts(t *testing.T) {
	p, paths := testProject(t)
	outside := filepath.Join(p.Dir, "outside-scene")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(paths.OutDir, takesDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, paths.sceneDir()); err != nil {
		t.Fatal(err)
	}
	other, err := Begin(p, "other")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other.Clip(), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	closeFile(other.lease)
	other.lease = nil
	other.finished = true
	_, err = Prune(context.Background(), p, PruneOptions{})
	if err == nil || !strings.Contains(err.Error(), "demo") {
		t.Fatalf("escaping scene should be listed: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("prune must not remove the escaped directory")
	}
	if _, err := os.Stat(other.dir); !os.IsNotExist(err) {
		t.Fatal("other scene should still be pruned")
	}
}

func TestPruneIgnoresFreshCreatingAndReapsOld(t *testing.T) {
	p, paths := testProject(t)
	freshID := newID(time.Now())
	fresh := paths.creatingDir(freshID)
	if err := os.MkdirAll(fresh, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fresh, clipName), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldID := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC).Format(idLayout) + "-aaaa"
	old := paths.creatingDir(oldID)
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, clipName), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, leaseName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Prune(context.Background(), p, PruneOptions{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("fresh creating dir was reaped")
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old creating dir left: %v", got)
	}
	if _, err := os.Stat(paths.attemptDir(oldID)); err != nil {
		t.Fatal("old creating with clip should become an attempt")
	}
}

func TestPruneDoesNotFollowAttemptsSymlink(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "ok")
	if _, err := s.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(p.Dir, "outside-attempts")
	oldID := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC).Format(idLayout) + "-aaaa"
	if err := os.MkdirAll(filepath.Join(outside, oldID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, oldID, clipName), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.sceneDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, paths.attemptsDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := Prune(context.Background(), p, PruneOptions{OlderThan: time.Nanosecond, Now: time.Now().Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, oldID, clipName)); err != nil {
		t.Fatal("prune followed attempts symlink and removed outside files")
	}
}

func TestPruneRepairRereadsCurrentGeneration(t *testing.T) {
	p, paths := testProject(t)
	first, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, first, "g1")
	if _, err := first.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	afterCollectForTest = func() {
		next, err := Begin(p, "demo")
		if err != nil {
			t.Errorf("begin g2: %v", err)
			return
		}
		writeSession(t, next, "g2")
		if _, err := next.Publish(context.Background()); err != nil {
			t.Errorf("publish g2: %v", err)
		}
	}
	t.Cleanup(func() { afterCollectForTest = nil })
	if _, err := Prune(context.Background(), p, PruneOptions{}); err != nil {
		t.Fatal(err)
	}
	man, err := readManifest(paths.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(paths.generationDir(man.Generation), clipName))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(paths.StableClip())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("projection %q does not match manifest generation %q", got, want)
	}
	if string(got) != "g2-clip" {
		t.Fatalf("projection rolled back to g1: %s", got)
	}
}

func TestPruneMaxSizeExcludesSkippedSceneBytes(t *testing.T) {
	dir := t.TempDir()
	p := &scene.Project{Dir: dir, Record: scene.RecordCfg{Out: "recordings"}}
	big, err := ForScene(p, "big")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(big.sceneDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(big.sceneDir(), "blob"), bytes.Repeat([]byte("b"), 5*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(big.Manifest(), []byte(`{"version":9}`), 0o644); err != nil {
		t.Fatal(err)
	}
	oldID := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC).Format(idLayout) + "-aaaa"
	small, err := ForScene(p, "small")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(small.attemptDir(oldID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(small.attemptDir(oldID), clipName), bytes.Repeat([]byte("s"), 100), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Prune(context.Background(), p, PruneOptions{MaxSize: 1000, Now: time.Now()})
	if err == nil || !strings.Contains(err.Error(), "big") {
		t.Fatalf("expected skip of big: %v", err)
	}
	if _, err := os.Stat(small.attemptDir(oldID)); err != nil {
		t.Fatal("small attempt was removed because skipped scene bytes inflated --max-size")
	}
}

func TestPruneSkipsLeasedAttempt(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "keep")
	attempt, err := s.KeepAttempt()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := os.Open(filepath.Join(filepath.Dir(attempt), leaseName))
	if err != nil {
		t.Fatal(err)
	}
	if err := flock(lease, syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer closeFile(lease)
	got, err := Prune(context.Background(), p, PruneOptions{OlderThan: time.Nanosecond, Now: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(attempt)); err != nil {
		t.Fatalf("leased attempt was removed: %v", got.Removed)
	}
	if len(got.Skipped) == 0 {
		t.Fatal("expected leased attempt to be skipped")
	}
	_ = paths.Scene
}

func TestPruneDoesNotHoldAllSceneLocks(t *testing.T) {
	dir := t.TempDir()
	p := &scene.Project{Dir: dir, Record: scene.RecordCfg{Out: "recordings"}}
	alphaSess, err := Begin(p, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, alphaSess, "a")
	if _, err := alphaSess.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	beta, err := ForScene(p, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(beta.OutDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beta.StableClip(), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beta.StableFacts(), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(beta.sceneDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := createLock(beta.lockPath()); err != nil {
		t.Fatal(err)
	}

	betaHeld := make(chan struct{})
	pruneHoldsAlpha := make(chan struct{})
	afterSceneLockForTest = func(name string) {
		if name == "alpha" {
			close(pruneHoldsAlpha)
			time.Sleep(30 * time.Millisecond)
		}
	}
	t.Cleanup(func() { afterSceneLockForTest = nil })

	errCh := make(chan error, 2)
	go func() {
		h, err := Open(p, "beta")
		if err != nil {
			errCh <- err
			return
		}
		close(betaHeld)
		select {
		case <-pruneHoldsAlpha:
		case <-time.After(2 * time.Second):
			_ = h.Close()
			errCh <- fmt.Errorf("timed out waiting for prune to hold alpha")
			return
		}
		// Prune is inside the alpha collect lock (or still holding it if
		// it keeps every lock). Opening alpha now is the deadlock order.
		opened := make(chan error, 1)
		go func() {
			ha, err := Open(p, "alpha")
			if err != nil {
				opened <- err
				return
			}
			_ = ha.Close()
			opened <- nil
		}()
		select {
		case err := <-opened:
			if err != nil {
				_ = h.Close()
				errCh <- err
				return
			}
		case <-time.After(2 * time.Second):
			_ = h.Close()
			errCh <- fmt.Errorf("timed out opening alpha while prune held a scene lock")
			return
		}
		errCh <- h.Close()
	}()
	go func() {
		<-betaHeld
		_, err := Prune(context.Background(), p, PruneOptions{OlderThan: time.Nanosecond, Now: time.Now().Add(time.Hour)})
		errCh <- err
	}()
	timeout := time.After(5 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatal(err)
			}
		case <-timeout:
			t.Fatal("prune and legacy reader deadlocked")
		}
	}
}

func TestPruneRepairSkipsUnreadableManifestAndContinues(t *testing.T) {
	dir := t.TempDir()
	p := &scene.Project{Dir: dir, Record: scene.RecordCfg{Out: "recordings"}}
	first, err := Begin(p, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, first, "a")
	if _, err := first.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	second, err := Begin(p, "beta")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, second, "b")
	if _, err := second.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	alpha, err := ForScene(p, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := ForScene(p, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alpha.StableClip(), []byte("dirty-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beta.StableClip(), []byte("dirty-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	afterCollectForTest = func() {
		if err := os.Chmod(alpha.Manifest(), 0o000); err != nil {
			t.Errorf("chmod: %v", err)
		}
	}
	t.Cleanup(func() {
		afterCollectForTest = nil
		_ = os.Chmod(alpha.Manifest(), 0o644)
	})
	_, err = Prune(context.Background(), p, PruneOptions{})
	if err == nil || !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("expected alpha skipped: %v", err)
	}
	got, err := os.ReadFile(beta.StableClip())
	if err != nil || string(got) != "b-clip" {
		t.Fatalf("beta should still be repaired: %s %v", got, err)
	}
}
