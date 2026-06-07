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

// substitute replaces the supported placeholders in cmd.
func substitute(cmd string, v Vars) string {
	return strings.NewReplacer(
		"{{out}}", v.Out,
		"{{w}}", strconv.Itoa(v.W),
		"{{h}}", strconv.Itoa(v.H),
		"{{fps}}", strconv.Itoa(v.FPS),
		"{{from}}", v.From,
		"{{to}}", v.To,
	).Replace(cmd)
}

// Render substitutes placeholders, runs the command via the shell (so users can
// use pipes/redirection), and verifies a non-empty mp4 landed at v.Out.
func Render(cmd string, v Vars, env []string, dir string) error {
	final := substitute(cmd, v)
	c := exec.Command("sh", "-c", final)
	c.Dir = dir
	c.Env = env
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("transition command failed: %w", err)
	}
	fi, err := os.Stat(v.Out)
	if err != nil {
		return fmt.Errorf("transition wrote no output at %s: %w", v.Out, err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("transition output is empty: %s", v.Out)
	}
	return nil
}
