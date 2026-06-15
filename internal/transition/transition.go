// Package transition renders a transition clip by running a full user-defined
// command. The command must write an mp4 to {{out}}; Backstage substitutes a few
// placeholders and otherwise stays out of the way (any tool, any params).
package transition

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Vars are the values substituted into a transition command.
type Vars struct {
	Out  string // {{out}} — where the command must write the mp4 (required)
	From string // {{from}} — scene before this transition
	To   string // {{to}} — scene after this transition
	W    int    // {{w}}
	H    int    // {{h}}
	FPS  int    // {{fps}}
}

// Substitute replaces the supported placeholders in a command or argument. Values
// are inserted verbatim: offline transition commands are trusted project shell
// commands, and the config author owns quoting where shell word boundaries matter.
func Substitute(cmd string, v Vars) string {
	return strings.NewReplacer(
		"{{out}}", v.Out,
		"{{w}}", strconv.Itoa(v.W),
		"{{h}}", strconv.Itoa(v.H),
		"{{fps}}", strconv.Itoa(v.FPS),
		"{{from}}", v.From,
		"{{to}}", v.To,
	).Replace(cmd)
}

// SubstituteArgs applies Substitute to every argument.
func SubstituteArgs(args []string, v Vars) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = Substitute(a, v)
	}
	return out
}

// SubstituteLiveArgs substitutes placeholders for a live prop's args. Unlike an
// offline command, a live prop does NOT own the clip file — the recorder writes
// {{out}}. So {{out}} is intentionally left empty here: handing the recorder's
// output path to a live prop would let it clobber the in-progress recording.
// Live props may still use {{w}}/{{h}}/{{fps}}/{{from}}/{{to}}.
func SubstituteLiveArgs(args []string, v Vars) []string {
	lv := v
	lv.Out = ""
	return SubstituteArgs(args, lv)
}

// BuildCommand substitutes placeholders and returns the shell command that renders
// a transition clip. Callers that need signal/process lifecycle control can start
// and wait it themselves, then call VerifyOutput.
func BuildCommand(cmd string, v Vars, env []string, dir string) *exec.Cmd {
	final := Substitute(cmd, v)
	c := exec.Command("sh", "-c", final)
	c.Dir = dir
	c.Env = env
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c
}

// Render substitutes placeholders, runs the command via the shell (so users can
// use pipes/redirection), and verifies a non-empty mp4 landed at v.Out.
func Render(cmd string, v Vars, env []string, dir string) error {
	c := BuildCommand(cmd, v, env, dir)
	if err := c.Run(); err != nil {
		return fmt.Errorf("transition command failed: %w", err)
	}
	return VerifyOutput(v.Out)
}

// VerifyOutput checks that a transition command wrote a non-empty mp4.
func VerifyOutput(out string) error {
	fi, err := os.Stat(out)
	if err != nil {
		return fmt.Errorf("transition wrote no output at %s: %w", out, err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("transition output is empty: %s", out)
	}
	return nil
}
