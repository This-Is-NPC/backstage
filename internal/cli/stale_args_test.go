package cli

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestStaleParse(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	scene := filepath.Join(dir, "scene.json")
	writeFile(t, scene, `{"name":"solo","layout":"solo","steps":[{"action":"wait"}]}`)

	for _, verb := range []struct {
		name string
		cmd  func() *cobra.Command
	}{
		{"play", playCmd},
		{"rehearse", rehearseCmd},
	} {
		t.Run(verb.name+"/stale-dir-json", func(t *testing.T) {
			got, err := parseStale(t, verb.cmd, "--stale", dir, "--json")
			if err != nil {
				t.Fatal(err)
			}
			if got != dir {
				t.Fatalf("dir %q, want %q", got, dir)
			}
		})
		t.Run(verb.name+"/stale-json-dir", func(t *testing.T) {
			got, err := parseStale(t, verb.cmd, "--stale", "--json", dir)
			if err != nil {
				t.Fatal(err)
			}
			if got != dir {
				t.Fatalf("dir %q, want %q", got, dir)
			}
		})
		t.Run(verb.name+"/stale-default-dot", func(t *testing.T) {
			got, err := parseStale(t, verb.cmd, "--stale")
			if err != nil {
				t.Fatal(err)
			}
			if got != "." {
				t.Fatalf("dir %q, want .", got)
			}
		})
		t.Run(verb.name+"/stale-two-dirs", func(t *testing.T) {
			_, err := parseStale(t, verb.cmd, "--stale", dir, other)
			if err == nil {
				t.Fatal("two dirs must fail")
			}
			if !strings.Contains(err.Error(), "accepts at most 1 arg") {
				t.Fatalf("want at most 1 arg, got %v", err)
			}
		})
		t.Run(verb.name+"/scene-then-stale-is-file", func(t *testing.T) {
			_, err := parseStale(t, verb.cmd, scene, "--stale")
			if err == nil {
				t.Fatal("file as stale dir must fail")
			}
			if !strings.Contains(err.Error(), "workspace directory") || !strings.Contains(err.Error(), scene) {
				t.Fatalf("want a directory error naming the file, got %v", err)
			}
		})
		t.Run(verb.name+"/no-args-no-stale", func(t *testing.T) {
			_, err := parseStale(t, verb.cmd)
			if err == nil {
				t.Fatal("missing scene must fail")
			}
			if !strings.Contains(err.Error(), "accepts 1 arg") {
				t.Fatalf("want exactly 1 arg, got %v", err)
			}
		})
	}
}

func TestStaleHelpIsSwitch(t *testing.T) {
	for _, cmd := range []*cobra.Command{playCmd(), rehearseCmd()} {
		if cmd.Flags().Lookup("stale").Value.Type() != "bool" {
			t.Fatalf("%s --stale type %s, want bool", cmd.Name(), cmd.Flags().Lookup("stale").Value.Type())
		}
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetArgs([]string{"--help"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		help := buf.String()
		if strings.Contains(help, `string[="."]`) {
			t.Fatalf("%s help still shows a string --stale:\n%s", cmd.Name(), help)
		}
		if !strings.Contains(help, "--stale [DIR]") {
			t.Fatalf("%s help missing --stale [DIR]:\n%s", cmd.Name(), help)
		}
	}
}

func parseStale(t *testing.T, makeCmd func() *cobra.Command, args ...string) (string, error) {
	t.Helper()
	cmd := makeCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	var got string
	cmd.RunE = func(_ *cobra.Command, a []string) error {
		dir, err := staleWorkspace(a)
		got = dir
		return err
	}
	cmd.SetArgs(args)
	err := cmd.Execute()
	return got, err
}
