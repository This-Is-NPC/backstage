package take

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func testProject(t *testing.T) (*scene.Project, Paths) {
	t.Helper()
	dir := t.TempDir()
	p := &scene.Project{Dir: dir, Record: scene.RecordCfg{Out: "recordings"}}
	paths, err := ForScene(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	return p, paths
}

func writeSession(t *testing.T, s *Session, body string) {
	t.Helper()
	if err := os.WriteFile(s.Clip(), []byte(body+"-clip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.FactsFile(), []byte(body+"-facts"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPublishThenOpenReadsGeneration(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "ok")
	pub, err := s.Publish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h, err := Open(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if h.Clip != pub.Clip {
		t.Fatalf("reader clip %s, want generation %s", h.Clip, pub.Clip)
	}
	got, err := os.ReadFile(paths.StableClip())
	if err != nil || string(got) != "ok-clip" {
		t.Fatalf("projection: %s %v", got, err)
	}
}

func TestInterruptBlockedDuringCommitCriticalSection(t *testing.T) {
	p, paths := testProject(t)
	first, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, first, "old")
	if _, err := first.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	next, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, next, "new")
	started := make(chan struct{})
	finished := make(chan struct{})
	next.hook = func(name string) error {
		if name != afterRenameBeforeManifest {
			return nil
		}
		go func() {
			close(started)
			next.Interrupt()
			close(finished)
		}()
		<-started
		deadline := time.Now().Add(50 * time.Millisecond)
		for time.Now().Before(deadline) {
			select {
			case <-finished:
				t.Error("Interrupt returned while the commit still held the session mutex")
				return nil
			default:
				time.Sleep(2 * time.Millisecond)
			}
		}
		return nil
	}
	_, err = next.Publish(context.Background())
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Interrupt stayed blocked after the critical section")
	}
	assertPublishedOrAttempt(t, p, paths, next, err)
}

func TestConcurrentInterruptLeavesValidTakeOrAttempt(t *testing.T) {
	steps := []string{
		beforeRenameGeneration, afterRenameBeforeManifest, afterRenameGeneration, beforeManifest, afterManifest,
		beforeProjectClip, afterProjectClip, beforeProjectFacts, afterProjectFacts,
	}
	for _, step := range steps {
		t.Run(step, func(t *testing.T) {
			p, paths := testProject(t)
			first, err := Begin(p, "demo")
			if err != nil {
				t.Fatal(err)
			}
			writeSession(t, first, "old")
			if _, err := first.Publish(context.Background()); err != nil {
				t.Fatal(err)
			}
			next, err := Begin(p, "demo")
			if err != nil {
				t.Fatal(err)
			}
			writeSession(t, next, "new")
			var wg sync.WaitGroup
			next.hook = func(name string) error {
				if name != step {
					return nil
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					next.Interrupt()
				}()
				return nil
			}
			_, err = next.Publish(context.Background())
			wg.Wait()
			assertPublishedOrAttempt(t, p, paths, next, err)
		})
	}
}

func assertPublishedOrAttempt(t *testing.T, p *scene.Project, paths Paths, s *Session, pubErr error) {
	t.Helper()
	man, manErr := readManifest(paths.Manifest())
	if manErr != nil {
		t.Fatalf("manifest: %v", manErr)
	}
	gen := paths.generationDir(man.Generation)
	if _, err := os.Stat(gen); err != nil {
		t.Fatalf("manifest without generation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(gen, leaseName)); err != nil {
		t.Fatalf("generation has no lease: %v", err)
	}
	h, err := Open(p, "demo")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	h.Close()
	if pubErr != nil {
		if _, err := os.Stat(paths.generationDir(s.ID)); !os.IsNotExist(err) {
			t.Fatal("failed publish left a generation that is not current")
		}
		if _, err := os.Stat(paths.attemptDir(s.ID)); err != nil {
			t.Fatalf("failed publish did not keep an attempt: %v", err)
		}
		return
	}
	if man.Generation != s.ID {
		t.Fatalf("successful publish left manifest at %s, want %s", man.Generation, s.ID)
	}
}

func TestPublishFailureBeforeManifestBecomesAttempt(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "fail")
	s.hook = func(step string) error {
		if step == beforeRenameGeneration {
			return errors.New("interrupted")
		}
		return nil
	}
	_, err = s.Publish(context.Background())
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("publish error: %v", err)
	}
	if _, err := os.Stat(paths.Manifest()); !os.IsNotExist(err) {
		t.Fatalf("manifest should be absent: %v", err)
	}
	if _, err := os.Stat(paths.StableClip()); !os.IsNotExist(err) {
		t.Fatal("stable path should be unchanged")
	}
	entries, err := os.ReadDir(paths.attemptsDir())
	if err != nil || len(entries) != 1 {
		t.Fatalf("attempts: %v %v", entries, err)
	}
}

func TestWriteStablePathDoesNotChangeGeneration(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "gen")
	pub, err := s.Publish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StableClip(), []byte("mutated"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(pub.Clip)
	if err != nil || string(got) != "gen-clip" {
		t.Fatalf("generation changed: %s %v", got, err)
	}
}

func TestPublisherWaitsForSceneLock(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "wait")
	held, err := createLock(paths.lockPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := flock(held, syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := s.Publish(context.Background())
		done <- err
	}()
	time.Sleep(80 * time.Millisecond)
	select {
	case err := <-done:
		closeFile(held)
		t.Fatalf("published without waiting: %v", err)
	default:
	}
	closeFile(held)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	h, err := Open(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	h.Close()
}

func TestPublishLockWaitCancelledBecomesAttempt(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "cancel")
	held, err := createLock(paths.lockPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := flock(held, syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer closeFile(held)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = s.Publish(ctx)
	if err == nil {
		t.Fatal("expected wait cancellation")
	}
	entries, err := os.ReadDir(paths.attemptsDir())
	if err != nil || len(entries) != 1 {
		t.Fatalf("cancelled wait should keep an attempt: %v %v", entries, err)
	}
}

func TestFailedTakeStaysOffStablePath(t *testing.T) {
	p, paths := testProject(t)
	ok, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, ok, "good")
	if _, err := ok.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	bad, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, bad, "bad")
	path, err := bad.KeepAttempt()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(path, filepath.Join("attempts", bad.ID)) {
		t.Fatalf("attempt path: %s", path)
	}
	body, err := os.ReadFile(paths.StableClip())
	if err != nil || string(body) != "good-clip" {
		t.Fatalf("stable changed: %s %v", body, err)
	}
	h, err := Open(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	got, _ := os.ReadFile(h.Clip)
	if string(got) != "good-clip" {
		t.Fatalf("manifest moved to the failed take: %s", got)
	}
}

func TestBeginDoesNotWaitForSceneLock(t *testing.T) {
	p, paths := testProject(t)
	held, err := createLock(paths.lockPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := flock(held, syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer closeFile(held)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.dir, pendingPrefix) {
		t.Fatalf("pending path: %s", s.dir)
	}
	_ = s.Discard()
}

func TestInterruptBeforeCommitMovesToAttempts(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "keep")
	s.Interrupt()
	if !s.Finished() {
		t.Fatal("interrupt should finish the session")
	}
	entries, err := os.ReadDir(paths.attemptsDir())
	if err != nil || len(entries) != 1 {
		t.Fatalf("attempts: %v %v", entries, err)
	}
	if _, err := os.Stat(paths.Manifest()); !os.IsNotExist(err) {
		t.Fatal("interrupt before commit must not write a manifest")
	}
}

func TestInterruptAfterCommitRemovesTempsOnly(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "ok")
	if _, err := s.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(paths.OutDir, ".demo.mp4.zzzz.tmp")
	if err := os.WriteFile(tmp, []byte("tmp"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.trackTemp(tmp)
	s.Interrupt()
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("post-commit interrupt must remove projection temps")
	}
	h, err := Open(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	h.Close()
	if _, err := os.Stat(paths.generationDir(s.ID)); err != nil {
		t.Fatal("post-commit interrupt must keep the generation")
	}
}

func TestOverwritesTakeRefusesStableAndTakesPaths(t *testing.T) {
	p, paths := testProject(t)
	for _, out := range []string{paths.StableClip(), paths.StableFacts(), paths.Manifest(), filepath.Join(paths.sceneDir(), "x.mp4")} {
		if err := OverwritesTake(p, "demo", out); err == nil {
			t.Fatalf("allowed overwrite of %s", out)
		}
	}
	safe := filepath.Join(p.Dir, "exports", "demo.mp4")
	if err := OverwritesTake(p, "demo", safe); err != nil {
		t.Fatal(err)
	}
}

func TestValidID(t *testing.T) {
	if !validID("20200102T030405.000000000Z-abcd") {
		t.Fatal("valid id rejected")
	}
	for _, id := range []string{"", "nope", "20200102T030405Z-abcd", "20200102T030405.000000000Z-ABCD", "20200102T030405.000000000Z-abc"} {
		if validID(id) {
			t.Fatalf("invalid id accepted: %q", id)
		}
	}
}
