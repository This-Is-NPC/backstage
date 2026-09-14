package take

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestOpenLegacyWithoutLock(t *testing.T) {
	p, paths := testProject(t)
	if err := os.MkdirAll(paths.OutDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StableClip(), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StableFacts(), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := Open(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if h.Clip != paths.StableClip() {
		t.Fatalf("legacy clip %s", h.Clip)
	}
	if len(h.files) != 0 {
		t.Fatal("legacy without lock should not hold a flock")
	}
}

func TestOpenMissingIsErrNotPublished(t *testing.T) {
	p, _ := testProject(t)
	_, err := Open(p, "demo")
	if !errors.Is(err, ErrNotPublished) {
		t.Fatalf("missing take: %v", err)
	}
}

func TestOpenInvalidManifestFails(t *testing.T) {
	p, paths := testProject(t)
	if err := os.MkdirAll(paths.sceneDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := createLock(paths.lockPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Manifest(), []byte(`{"version":9}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p, "demo"); err == nil {
		t.Fatal("invalid manifest should fail")
	}
}

func TestOpenManifestWithoutLockFails(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "ok")
	if _, err := s.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.lockPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p, "demo"); err == nil || !strings.Contains(err.Error(), "scene lock") {
		t.Fatalf("missing lock: %v", err)
	}
}

func TestOpenManifestWithoutLeaseFails(t *testing.T) {
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
	if err := os.Remove(filepath.Join(paths.generationDir(pub.Generation), leaseName)); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p, "demo"); err == nil || !strings.Contains(err.Error(), "lease") {
		t.Fatalf("missing lease: %v", err)
	}
}

func TestOpenReadOnlyOutput(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "ro")
	if _, err := s.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	chmodTree(t, paths.OutDir, 0o555)
	t.Cleanup(func() { chmodTree(t, paths.OutDir, 0o755) })
	h, err := Open(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	got, err := os.ReadFile(h.Clip)
	if err != nil || string(got) != "ro-clip" {
		t.Fatalf("read-only open: %s %v", got, err)
	}
}

func TestOpenRejectsEscapingSymlink(t *testing.T) {
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
	outside := filepath.Join(p.Dir, "outside.mp4")
	if err := os.WriteFile(outside, []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	clip := filepath.Join(paths.generationDir(pub.Generation), clipName)
	if err := os.Remove(clip); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, clip); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p, "demo"); err == nil {
		t.Fatal("escaping symlink should fail")
	}
}

func chmodTree(t *testing.T, root string, mode os.FileMode) {
	t.Helper()
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			_ = os.Chmod(path, mode)
		}
		return nil
	})
}

func TestOpenDoesNotRepairProjection(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "src")
	if _, err := s.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.StableClip(), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := Open(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	h.Close()
	got, _ := os.ReadFile(paths.StableClip())
	if string(got) != "dirty" {
		t.Fatalf("reader repaired the projection: %s", got)
	}
}

func TestMissingManifestAndClip(t *testing.T) {
	p := &scene.Project{Dir: t.TempDir(), Record: scene.RecordCfg{Out: "recordings"}}
	if _, err := Open(p, "demo"); err == nil {
		t.Fatal("expected missing take")
	}
}

func TestOpenRejectsNestedEscapingClip(t *testing.T) {
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
	outside := filepath.Join(p.Dir, "escaped")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "x.mp4"), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	gen := paths.generationDir(pub.Generation)
	if err := os.Symlink(outside, filepath.Join(gen, "sub")); err != nil {
		t.Fatal(err)
	}
	man := Manifest{Version: 1, Generation: pub.Generation, Clip: "sub/x.mp4", Facts: factsName}
	if err := writeJSON(paths.Manifest(), man); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p, "demo"); err == nil {
		t.Fatal("nested escaping clip should fail")
	}
}

func TestOpenRejectsSceneDirSymlink(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "ok")
	if _, err := s.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(p.Dir, "takes-outside")
	if err := os.Rename(paths.sceneDir(), outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, paths.sceneDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p, "demo"); err == nil {
		t.Fatal("scene dir symlink leaving record.out should fail")
	}
}

func TestOpenRejectsInvalidGenerationID(t *testing.T) {
	p, paths := testProject(t)
	s, err := Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, s, "ok")
	if _, err := s.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(paths.Manifest(), Manifest{Version: 1, Generation: "not-an-id", Clip: clipName, Facts: factsName}); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p, "demo"); err == nil {
		t.Fatal("invalid generation id should fail")
	}
}
