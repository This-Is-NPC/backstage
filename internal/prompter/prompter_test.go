package prompter

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTypewriterOutput(t *testing.T) {
	var b strings.Builder
	Typewriter(&b, "faça já", 1e9) // huge cps -> no real wait
	got := b.String()
	if !strings.HasPrefix(got, "faça já") {
		t.Errorf("output missing/garbled text: %q", got)
	}
	if !strings.HasSuffix(got, caret) {
		t.Errorf("output missing trailing caret: %q", got)
	}
}

func TestTypeDurationGrows(t *testing.T) {
	short := TypeDuration("hi", 32)
	long := TypeDuration("hi there, this is a longer line\nwith a newline", 32)
	if long <= short {
		t.Errorf("longer text should take longer: short=%v long=%v", short, long)
	}
	if short <= 0 {
		t.Errorf("duration should be positive, got %v", short)
	}
}

func TestTypeDurationDefaultsInvalidCPS(t *testing.T) {
	if got, want := TypeDuration("hi", 0), TypeDuration("hi", 32); got != want {
		t.Errorf("invalid cps should default: got %v want %v", got, want)
	}
}

func TestShquote(t *testing.T) {
	if got := shquote("/a b/c"); got != "'/a b/c'" {
		t.Errorf("shquote spaces = %q", got)
	}
	if got := shquote("it's"); got != `'it'\''s'` {
		t.Errorf("shquote apostrophe = %q", got)
	}
}

func TestHeaderCommand(t *testing.T) {
	if got := headerCommand("prompt", "none"); got != "" {
		t.Errorf("none header = %q, want empty", got)
	}
	if got := headerCommand("prompt", "minimal"); !strings.Contains(got, "prompt") || strings.Contains(got, "──") {
		t.Errorf("minimal header unexpected: %q", got)
	}
	if got := headerCommand("prompt", "default"); !strings.Contains(got, "prompt") || !strings.Contains(got, "──") {
		t.Errorf("default header unexpected: %q", got)
	}
	if got := headerCommand(`\033[31m`, "minimal"); strings.Contains(got, "%b") {
		t.Errorf("header command must not use %%b for configured text: %q", got)
	}
}

func TestHeaderCommandRendersConfiguredHeaderLiterally(t *testing.T) {
	out, err := exec.Command("bash", "-lc", headerCommand(`\033[31m`, "minimal")).Output()
	if err != nil {
		t.Fatalf("header command: %v", err)
	}
	if got, want := string(out), "\n  \\033[31m\n\n"; got != want {
		t.Fatalf("escaped header output = %q, want %q", got, want)
	}

	out, err = exec.Command("bash", "-lc", headerCommand("bad\x1b[31m", "minimal")).Output()
	if err != nil {
		t.Fatalf("header command with control byte: %v", err)
	}
	if strings.Contains(string(out), "\x1b") {
		t.Fatalf("configured header emitted terminal escape byte: %q", out)
	}
	if !strings.Contains(string(out), `bad\x1b[31m`) {
		t.Fatalf("configured header control byte was not rendered visibly: %q", out)
	}
}

func TestClassREFor(t *testing.T) {
	if got := classREFor("backstage.demo"); got != `^(backstage\.demo)$` {
		t.Errorf("classREFor = %q", got)
	}
	if got := classREFor(""); got != `^(backstage\.popup)$` {
		t.Errorf("default classREFor = %q", got)
	}
}

func TestHyprPopupTracksAndCleansTerminalProcess(t *testing.T) {
	dir := t.TempDir()
	hyprLog := filepath.Join(dir, "hypr.log")
	termPIDs := filepath.Join(dir, "term-pids")
	termClosed := filepath.Join(dir, "term-closed")
	installPrompterStub(t, dir, "hyprctl", `#!/bin/sh
if [ "$1" = "clients" ]; then
  printf '[{"class": "backstage.popup"}]'
  exit 0
fi
printf '%s\n' "$*" >> "$HYPR_LOG"
exit 0
`)
	installPrompterStub(t, dir, "termstub", `#!/bin/sh
printf '%s\n' "$$" >> "$TERM_PIDS"
trap 'printf "%s\n" "$$" >> "$TERM_CLOSED"; exit 0' TERM INT HUP
while :; do sleep 1; done
`)
	t.Setenv("HYPR_LOG", hyprLog)
	t.Setenv("TERM_PIDS", termPIDs)
	t.Setenv("TERM_CLOSED", termClosed)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	oldGrace := popupTerminateGrace
	popupTerminateGrace = 200 * time.Millisecond
	defer func() { popupTerminateGrace = oldGrace }()

	h := &Hypr{Self: "/bin/true"}
	defer h.Close()
	opts := Opts{Term: "termstub", Width: 640, Height: 240}
	if err := h.Show("first", opts); err != nil {
		t.Fatalf("first Show: %v", err)
	}
	waitForPrompterLines(t, termPIDs, 1)
	h.mu.Lock()
	firstTmp := h.tmp
	setpgid := h.cmd != nil && h.cmd.SysProcAttr != nil && h.cmd.SysProcAttr.Setpgid
	h.mu.Unlock()
	if firstTmp == "" {
		t.Fatal("Show should retain the popup temp file")
	}
	if !setpgid {
		t.Fatal("Show should start the terminal in its own process group")
	}

	if err := h.Show("second", opts); err != nil {
		t.Fatalf("second Show: %v", err)
	}
	waitForPrompterLines(t, termPIDs, 2)
	waitForPrompterLines(t, termClosed, 1)
	if _, err := os.Stat(firstTmp); !os.IsNotExist(err) {
		t.Fatalf("previous popup temp file still exists: %v", err)
	}

	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitForPrompterLines(t, termClosed, 2)
	h.mu.Lock()
	stateCleared := h.tmp == "" && h.class == "" && h.cmd == nil && h.waitCh == nil
	h.mu.Unlock()
	if !stateCleared {
		t.Fatal("Close should clear tracked popup state")
	}
	b, err := os.ReadFile(hyprLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `dispatch closewindow class:^(backstage\.popup)$`) {
		t.Fatalf("hyprctl closewindow by class not recorded in log:\n%s", b)
	}
}

func TestTitleSafety(t *testing.T) {
	if err := ValidateTitle("instruction.md"); err != nil {
		t.Fatalf("normal title rejected: %v", err)
	}
	for _, title := range []string{"line\nbreak", "bad\x1b]0;evil\x07", "c1\u009d"} {
		if err := ValidateTitle(title); err == nil {
			t.Fatalf("ValidateTitle(%q) should reject controls", title)
		}
	}
	if got, want := SafeTitle("bad\x1b]0;evil\x07"), "bad]0;evil"; got != want {
		t.Fatalf("SafeTitle = %q, want %q", got, want)
	}
}

var _ Prompter = (*Hypr)(nil)

func installPrompterStub(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func waitForPrompterLines(t *testing.T, path string, want int) []string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if b, err := os.ReadFile(path); err == nil {
			lines := strings.Fields(string(b))
			if len(lines) >= want {
				return lines
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %d lines in %s", want, path)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
