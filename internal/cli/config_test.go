package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigShowCommand(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	if err := os.Mkdir(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "backstage.json"), []byte(`{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"term": "ghostty"
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "backstage.json"), []byte(`{"extends": "../backstage.json"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	old := projectFlag
	projectFlag = leaf
	t.Cleanup(func() { projectFlag = old })

	cmd := configCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"show"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"workspace: " + ws,
		"project: " + leaf,
		"extends: ../backstage.json",
		"term = \"ghostty\"  [backstage.json]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q\n%s", want, got)
		}
	}
}
