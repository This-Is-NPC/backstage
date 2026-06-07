package stage

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

const (
	// LayoutClass is the window class of the staged tmux terminal.
	LayoutClass = "backstage.layout"
	// sessionName is the tmux session the layout runs in.
	sessionName = "backstage-stage"
)

var layoutRE = "^(" + strings.ReplaceAll(LayoutClass, ".", `\.`) + ")$"

// Hypr stages a fullscreen tmux layout in a single terminal window and forces
// real fullscreen via Hyprland. Ports build_layout / teardown_layout.
type Hypr struct{}

func tmux(args ...string) error {
	return exec.Command("tmux", args...).Run()
}

func tmuxOut(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	return strings.TrimSpace(string(out)), err
}

func hyprctl(args ...string) error {
	return exec.Command("hyprctl", args...).Run()
}

// Setup builds the tmux session for the layout, opens it in a terminal of class
// LayoutClass, and (when the layout is fullscreen) forces Hyprland fullscreen.
func (h *Hypr) Setup(layout scene.Layout, p *scene.Project) (*scene.Manifest, error) {
	if len(layout.Panes) == 0 {
		return nil, fmt.Errorf("layout has no panes")
	}
	_ = tmux("kill-session", "-t", sessionName)

	prefix := exportPrefix(p.Env)
	m := &scene.Manifest{Panes: map[string]string{}}

	first := layout.Panes[0]
	cwd0, err := p.SafePath(orDot(first.Cwd))
	if err != nil {
		return nil, err
	}
	pid, err := tmuxOut("new-session", "-d", "-P", "-F", "#{pane_id}",
		"-s", sessionName, "-c", cwd0, "-x", "260", "-y", "60")
	if err != nil {
		return nil, fmt.Errorf("tmux new-session: %w", err)
	}
	m.Panes[first.Name] = pid
	m.Order = append(m.Order, first.Name)
	if err := tmux("send-keys", "-t", pid, prefix+"clear; "+orBash(first.Cmd), "Enter"); err != nil {
		return nil, fmt.Errorf("tmux send-keys: %w", err)
	}

	prev := pid
	for _, pane := range layout.Panes[1:] {
		cwd, err := p.SafePath(orDot(pane.Cwd))
		if err != nil {
			return nil, err
		}
		args := []string{"split-window", "-h"}
		if pane.Size != "" {
			args = append(args, "-l", pane.Size)
		}
		args = append(args, "-t", prev, "-c", cwd, "-P", "-F", "#{pane_id}")
		npid, err := tmuxOut(args...)
		if err != nil {
			return nil, fmt.Errorf("tmux split-window: %w", err)
		}
		m.Panes[pane.Name] = npid
		m.Order = append(m.Order, pane.Name)
		if err := tmux("send-keys", "-t", npid, prefix+"clear; "+orBash(pane.Cmd), "Enter"); err != nil {
			return nil, fmt.Errorf("tmux send-keys: %w", err)
		}
		prev = npid
	}
	_ = tmux("select-pane", "-t", pid)

	term := p.Term
	if term == "" {
		term = "ghostty"
	}
	if err := exec.Command(term, "--class="+LayoutClass,
		"-e", "tmux", "attach", "-t", sessionName).Start(); err != nil {
		return nil, fmt.Errorf("spawn layout terminal: %w", err)
	}

	if layout.FullscreenEnabled() {
		forceFullscreen()
	}
	return m, nil
}

// Teardown kills the tmux session and closes the layout window.
func (h *Hypr) Teardown() error {
	_ = tmux("kill-session", "-t", sessionName)
	return hyprctl("dispatch", "closewindow", "class:"+layoutRE)
}

// forceFullscreen waits for the window to map, then repeatedly focuses it and
// toggles fullscreen until Hyprland reports it fullscreen (the rule can miss).
func forceFullscreen() {
	for i := 0; i < 30; i++ {
		if hasClient(LayoutClass) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	for i := 0; i < 12; i++ {
		_ = hyprctl("dispatch", "focuswindow", "class:"+layoutRE)
		time.Sleep(200 * time.Millisecond)
		_ = hyprctl("dispatch", "fullscreen", "0")
		time.Sleep(300 * time.Millisecond)
		if clientFullscreen(LayoutClass) {
			break
		}
	}
}

type hyprClient struct {
	Class      string `json:"class"`
	Fullscreen int    `json:"fullscreen"`
}

func clients() []hyprClient {
	out, err := exec.Command("hyprctl", "clients", "-j").Output()
	if err != nil {
		return nil
	}
	var cs []hyprClient
	_ = json.Unmarshal(out, &cs)
	return cs
}

func hasClient(class string) bool {
	for _, c := range clients() {
		if c.Class == class {
			return true
		}
	}
	return false
}

func clientFullscreen(class string) bool {
	for _, c := range clients() {
		if c.Class == class && c.Fullscreen != 0 {
			return true
		}
	}
	return false
}

// exportPrefix builds a deterministic "export K='V'; ..." prefix (keys sorted)
// so every pane inherits the project env block.
func exportPrefix(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString("export ")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(shquote(env[k]))
		b.WriteString("; ")
	}
	return b.String()
}

func shquote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func orDot(s string) string {
	if s == "" {
		return "."
	}
	return s
}

func orBash(s string) string {
	if s == "" {
		return "bash"
	}
	return s
}
