package stage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestObservedGhosttyStageDeclaresItsCellOrigin(t *testing.T) {
	for _, tc := range []struct {
		name, terminal string
		observations   bool
		precise        bool
	}{
		{"ordinary", "ghostty", false, false},
		{"observed", "ghostty", true, true},
		{"other-terminal", "other", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			argsPath := filepath.Join(dir, "arguments")
			t.Setenv("BACKSTAGE_TEST_TERMINAL_ARGS", argsPath)
			for name, script := range map[string]string{
				"tmux":      "#!/bin/sh\nif [ \"$1\" = new-session ]; then echo '%1'; fi\n",
				tc.terminal: "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$BACKSTAGE_TEST_TERMINAL_ARGS\"\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			fullscreen := false
			p := &scene.Project{Dir: dir, Workspace: dir, Term: filepath.Join(dir, tc.terminal), Record: scene.RecordCfg{Observations: tc.observations}}
			m, err := (&Hypr{}).Setup(scene.Layout{Fullscreen: &fullscreen, Panes: []scene.Pane{{Name: "editor"}}}, p)
			if err != nil {
				t.Fatal(err)
			}
			if (m.ContentOrigin != nil) != tc.precise {
				t.Fatalf("content origin=%v, precise=%v", m.ContentOrigin, tc.precise)
			}
			var args []byte
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				args, err = os.ReadFile(argsPath)
				if err == nil && len(args) != 0 {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if err != nil || len(args) == 0 {
				t.Fatalf("terminal arguments: %s, %v", args, err)
			}
			if strings.Contains(string(args), "--window-padding-x=0") != tc.precise ||
				strings.Contains(string(args), "--window-decoration=none") != tc.precise ||
				strings.Contains(string(args), "--scrollbar=never") != tc.precise {
				t.Fatalf("terminal arguments=%s", args)
			}
		})
	}
}

func TestExportPrefix(t *testing.T) {
	if got := exportPrefix(nil); got != "" {
		t.Errorf("empty env should give empty prefix, got %q", got)
	}
	// keys sorted -> deterministic
	got := exportPrefix(map[string]string{"B": "2", "A": "1"})
	want := "export A='1'; export B='2'; "
	if got != want {
		t.Errorf("exportPrefix = %q, want %q", got, want)
	}
	// value with a space and quote is shell-safe
	got = exportPrefix(map[string]string{"P": "/a b/c"})
	if got != "export P='/a b/c'; " {
		t.Errorf("exportPrefix quoting = %q", got)
	}
}

func TestOrHelpers(t *testing.T) {
	if orDot("") != "." || orDot("x") != "x" {
		t.Error("orDot")
	}
	if orBash("") != "bash" || orBash("okt tui") != "okt tui" {
		t.Error("orBash")
	}
}

var _ Stager = (*Hypr)(nil)
