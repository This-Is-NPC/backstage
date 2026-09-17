package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/take"
)

func TestTakesPruneDryRun(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "backstage.json"), []byte(`{"layouts":{"solo":{"panes":[{"name":"t"}]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	old := projectFlag
	projectFlag = dir
	t.Cleanup(func() { projectFlag = old })

	cmd := takesPruneCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--dry-run", "--older-than", "7d", "--max-size", "10M"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing to prune") {
		t.Fatalf("prune: %s", out.String())
	}
}

func TestTakesPruneMovesAbandonedClip(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "backstage.json"), []byte(`{"layouts":{"solo":{"panes":[{"name":"t"}]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &scene.Project{Dir: dir, Record: scene.RecordCfg{Out: "recordings"}}
	s, err := take.Begin(p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Clip(), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Discard(); err != nil {
		t.Fatal(err)
	}
	// Recreate an abandoned pending with a clip and no live lease.
	pending := filepath.Join(dir, "recordings", ".takes", "demo", ".pending-"+s.ID)
	if err := os.MkdirAll(pending, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending, "clip.mp4"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending, "lease"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	old := projectFlag
	projectFlag = dir
	t.Cleanup(func() { projectFlag = old })

	dry := takesPruneCmd()
	var dryOut bytes.Buffer
	dry.SetOut(&dryOut)
	dry.SetArgs([]string{"--dry-run"})
	if err := dry.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dryOut.String(), "moved") {
		t.Fatalf("dry-run: %s", dryOut.String())
	}

	real := takesPruneCmd()
	var realOut bytes.Buffer
	real.SetOut(&realOut)
	real.SetArgs([]string{})
	if err := real.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(realOut.String(), "moved") {
		t.Fatalf("prune: %s", realOut.String())
	}
	attempts, err := filepath.Glob(filepath.Join(dir, "recordings", ".takes", "demo", "attempts", "*", "clip.mp4"))
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts: %v %v", attempts, err)
	}
}
