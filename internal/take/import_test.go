package take

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestImportPublishesCopiedPair(t *testing.T) {
	p, paths := testProject(t)
	clip, facts := writeImportPair(t, t.TempDir(), "raw")
	s, err := Import(p, "demo", clip, facts)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := s.Publish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := filesMatch(clip, pub.Clip); err != nil {
		t.Fatal(err)
	}
	if err := filesMatch(facts, pub.Facts); err != nil {
		t.Fatal(err)
	}
	if err := filesMatch(clip, paths.StableClip()); err != nil {
		t.Fatal(err)
	}
	assertNotSameFile(t, clip, pub.Clip)
}

func TestImportNeverHardlinksOrRenamesSource(t *testing.T) {
	p, _ := testProject(t)
	srcDir := otherFSDir(t)
	clip, facts := writeImportPair(t, srcDir, "xfs")
	clipInfo, err := os.Stat(clip)
	if err != nil {
		t.Fatal(err)
	}
	old := cloneFile
	cloneFile = copyOnly
	t.Cleanup(func() { cloneFile = old })
	s, err := Import(p, "demo", clip, facts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(clip); err != nil {
		t.Fatalf("source clip was moved: %v", err)
	}
	got, err := os.Stat(clip)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(clipInfo, got) {
		t.Fatal("source clip inode changed")
	}
	assertNotSameFile(t, clip, s.Clip())
	if err := filesMatch(clip, s.Clip()); err != nil {
		t.Fatal(err)
	}
	if err := s.Discard(); err != nil {
		t.Fatal(err)
	}
}

func TestImportInterruptDuringClipCopyDiscards(t *testing.T) {
	p, paths := testProject(t)
	clip, facts := writeImportPair(t, t.TempDir(), "mid")
	var sess *Session
	old := cloneFile
	cloneFile = func(src string, dst *os.File) error {
		if _, err := dst.Write([]byte("partial")); err != nil {
			return err
		}
		if sess != nil {
			sess.Interrupt()
		}
		return context.Canceled
	}
	t.Cleanup(func() { cloneFile = old })
	_, err := ImportContext(context.Background(), p, "demo", clip, facts, func(in *Session) {
		sess = in
	})
	if err == nil {
		t.Fatal("partial copy succeeded")
	}
	assertNoAttempt(t, paths)
	assertNoPendingOrCreating(t, paths)
}

func TestImportInterruptBetweenCopiesDiscards(t *testing.T) {
	p, paths := testProject(t)
	clip, facts := writeImportPair(t, t.TempDir(), "gap")
	_, err := ImportContext(context.Background(), p, "demo", clip, facts, func(in *Session) {
		in.hook = func(name string) error {
			if name == afterImportClip {
				in.Interrupt()
			}
			return nil
		}
	})
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("import: %v", err)
	}
	assertNoAttempt(t, paths)
	assertNoPendingOrCreating(t, paths)
}

func TestImportInterruptAfterCopyKeepsAttempt(t *testing.T) {
	p, paths := testProject(t)
	clip, facts := writeImportPair(t, t.TempDir(), "int")
	var sess *Session
	s, err := ImportContext(context.Background(), p, "demo", clip, facts, func(in *Session) {
		sess = in
		in.hook = func(name string) error {
			if name == afterImportCopy {
				in.Interrupt()
			}
			return nil
		}
	})
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("import: %v", err)
	}
	if s == nil || !s.Finished() {
		t.Fatal("interrupted import returned no finished session")
	}
	if sess == nil || !sess.Finished() {
		t.Fatal("bind session was not finished")
	}
	assertAttempt(t, paths, s.ID)
	assertNoPendingOrCreating(t, paths)
}

func TestImportInterruptAfterLeaseKeepsAttempt(t *testing.T) {
	p, paths := testProject(t)
	clip, facts := writeImportPair(t, t.TempDir(), "lease")
	_, err := ImportContext(context.Background(), p, "demo", clip, facts, func(in *Session) {
		in.hook = func(name string) error {
			if name == afterImportLease {
				in.Interrupt()
			}
			return nil
		}
	})
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("import: %v", err)
	}
	assertAttempt(t, paths, "")
	assertNoPendingOrCreating(t, paths)
}

func TestPublishInterruptAfterImportLeavesValidState(t *testing.T) {
	p, paths := testProject(t)
	clip, facts := writeImportPair(t, t.TempDir(), "pub")
	s, err := Import(p, "demo", clip, facts)
	if err != nil {
		t.Fatal(err)
	}
	s.hook = func(name string) error {
		if name == beforeRenameGeneration {
			s.Interrupt()
			return context.Canceled
		}
		return nil
	}
	_, err = s.Publish(context.Background())
	if err == nil {
		t.Fatal("publish succeeded after interrupt")
	}
	assertAttempt(t, paths, s.ID)
	assertNoPendingOrCreating(t, paths)
}

func writeImportPair(t *testing.T, dir, body string) (clip, facts string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	clip = filepath.Join(dir, "work.mp4")
	facts = filepath.Join(dir, "work.facts.json")
	if err := os.WriteFile(clip, []byte(body+"-clip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(facts, []byte(body+"-facts"), 0o644); err != nil {
		t.Fatal(err)
	}
	return clip, facts
}

func otherFSDir(t *testing.T) string {
	t.Helper()
	if st, err := os.Stat("/dev/shm"); err == nil && st.IsDir() {
		dir, err := os.MkdirTemp("/dev/shm", "backstage-import-*")
		if err == nil {
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			return dir
		}
	}
	return t.TempDir()
}

func copyOnly(src string, dst *os.File) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if _, err := dst.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := dst.Truncate(0); err != nil {
		return err
	}
	_, err = io.Copy(dst, in)
	return err
}

func assertNotSameFile(t *testing.T, a, b string) {
	t.Helper()
	sa, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(sa, sb) {
		t.Fatalf("%s and %s share an inode (hardlink)", a, b)
	}
}

func assertNoAttempt(t *testing.T, paths Paths) {
	t.Helper()
	entries, err := os.ReadDir(paths.attemptsDir())
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unexpected attempts: %v", names(entries))
	}
}

func names(entries []os.DirEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name()
	}
	return out
}

func assertAttempt(t *testing.T, paths Paths, id string) {
	t.Helper()
	entries, err := os.ReadDir(paths.attemptsDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no attempt")
	}
	if id != "" {
		if _, err := os.Stat(filepath.Join(paths.attemptDir(id), clipName)); err != nil {
			t.Fatalf("attempt %s: %v", id, err)
		}
	}
}

func assertNoPendingOrCreating(t *testing.T, paths Paths) {
	t.Helper()
	entries, err := os.ReadDir(paths.sceneDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, pendingPrefix) || strings.HasPrefix(name, creatingPrefix) {
			t.Fatalf("left %s", name)
		}
	}
}

func TestImportRejectsEmptySource(t *testing.T) {
	p := &scene.Project{Dir: t.TempDir(), Record: scene.RecordCfg{Out: "recordings"}}
	clip := filepath.Join(t.TempDir(), "empty.mp4")
	if err := os.WriteFile(clip, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	facts := filepath.Join(t.TempDir(), "empty.facts.json")
	if err := os.WriteFile(facts, []byte(`{"result":"ok"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(p, "demo", clip, facts); err == nil {
		t.Fatal("empty clip accepted")
	}
}
