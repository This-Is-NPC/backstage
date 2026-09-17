package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/presentation"
)

func TestCachePruneCommandJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", home)
	root, err := presentation.DefaultRenderCacheRoot()
	if err != nil {
		t.Fatal(err)
	}
	tracks := filepath.Join(root, "tracks")
	if err = os.MkdirAll(tracks, 0o700); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("ab", 32)
	if err = os.WriteFile(filepath.Join(tracks, key+".mkv"), []byte("entry"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(tracks, key+".lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(tracks, key+".meta.json"), []byte(`{"last-used":1,"size":5}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := cachePruneCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--max-size", "1", "--json"})
	if err = cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var rep presentation.CachePruneReport
	if err = json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatal(err, out.String())
	}
	if len(rep.Removed) != 1 {
		t.Fatalf("%+v", rep)
	}
	if _, err = os.Stat(filepath.Join(tracks, key+".lock")); err != nil {
		t.Fatal("prune deleted the lock")
	}
}
