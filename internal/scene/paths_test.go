package scene

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfinedPathSameBaseAndBoundary(t *testing.T) {
	dir := t.TempDir()
	got, err := confinedPath(dir, dir, "recordings", "demo.mp4")
	if err != nil || filepath.Dir(got) != filepath.Join(dir, "recordings") {
		t.Fatalf("valid = %s, %v", got, err)
	}
	for _, bad := range []string{"../x", "/tmp/x"} {
		if _, err := confinedPath(dir, dir, bad); err == nil {
			t.Fatalf("confinedPath(%q) should reject escape", bad)
		}
	}
	outside := t.TempDir()
	link := filepath.Join(dir, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := confinedPath(dir, dir, "linked", "out.mp4"); err == nil {
		t.Fatal("should reject symlink escapes")
	}
	if _, err := confinedPath(dir, dir, "linked", "newdir", "out.mp4"); err == nil {
		t.Fatal("should reject symlink ancestor escapes with missing child directories")
	}
}

func TestConfinedPathSeparateBaseAndBoundary(t *testing.T) {
	workspace := t.TempDir()
	leaf := filepath.Join(workspace, "leaf")
	shared := filepath.Join(workspace, "shared")
	if err := os.Mkdir(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "hook.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	evalLeaf, err := filepath.EvalSymlinks(leaf)
	if err != nil {
		t.Fatal(err)
	}
	got, err := confinedPath(leaf, workspace, "local.txt")
	if err != nil || filepath.Dir(got) != evalLeaf {
		t.Fatalf("leaf-relative = %s, %v", got, err)
	}
	got, err = confinedPath(leaf, workspace, "..", "shared", "hook.sh")
	if err != nil {
		t.Fatalf("workspace-relative parent should be allowed: %v", err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(shared, "hook.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("workspace-relative = %s, want %s", got, want)
	}

	if _, err := confinedPath(leaf, workspace, "..", "..", "outside"); err == nil {
		t.Fatal("should reject a path that leaves the boundary")
	}
	if _, err := confinedPath(leaf, workspace, "/tmp/x"); err == nil {
		t.Fatal("should reject an absolute path")
	}

	outside := t.TempDir()
	link := filepath.Join(leaf, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := confinedPath(leaf, workspace, "linked", "out.mp4"); err == nil {
		t.Fatal("should reject a symlink that leaves the boundary")
	}
	if _, err := confinedPath(leaf, workspace, "linked", "newdir", "out.mp4"); err == nil {
		t.Fatal("should reject a symlink ancestor that leaves the boundary")
	}
}
