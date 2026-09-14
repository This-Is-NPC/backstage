package take

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilesMatchComparesSizeThenChunks(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.WriteFile(a, []byte("same-size-aaaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("same-size-bbbb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := filesMatch(a, b); err == nil {
		t.Fatal("same size, different content")
	}
	if err := os.WriteFile(b, []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := filesMatch(a, b); err == nil {
		t.Fatal("different sizes should fail before reading")
	}
	if err := os.WriteFile(b, []byte("same-size-aaaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := filesMatch(a, b); err != nil {
		t.Fatal(err)
	}
}
