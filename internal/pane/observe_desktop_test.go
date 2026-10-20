package pane

import (
	"encoding/json"
	"fmt"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

// This opt-in check opens one temporary window and uses an isolated tmux server.
func TestObservedPaneMatchesDesktopPixels(t *testing.T) {
	if os.Getenv("BACKSTAGE_DESKTOP_TEST") != "1" {
		t.Skip("set BACKSTAGE_DESKTOP_TEST=1 to check real terminal pixel geometry")
	}
	var monitors []struct {
		Name          string
		Width, Height int
		Focused       bool
		Transform     int
	}
	b, err := exec.Command("hyprctl", "-j", "monitors").Output()
	if err != nil || json.Unmarshal(b, &monitors) != nil {
		t.Fatalf("monitor query: %s, %v", b, err)
	}
	monitor := -1
	for i, m := range monitors {
		if m.Focused && m.Transform == 0 {
			monitor = i
		}
	}
	if monitor < 0 {
		t.Fatal("a focused, unrotated monitor is required")
	}
	dir := t.TempDir()
	socket := filepath.Join(dir, "tmux")
	t.Setenv("TMUX", socket+",0,0")
	tmux := func(args ...string) string {
		t.Helper()
		b, err := exec.Command("tmux", append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	tmux("-f", "/dev/null", "new-session", "-d", "-s", "pixels", "printf '\033[48;2;190;20;20m'; head -c 65536 /dev/zero | tr '\\000' ' '; sleep 60")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	id := tmux("split-window", "-h", "-P", "-F", "#{pane_id}", "-t", "pixels", "printf '\033[48;2;20;20;190m'; head -c 65536 /dev/zero | tr '\\000' ' '; sleep 60")
	class := fmt.Sprintf("backstage.pixels-%d", time.Now().UnixNano())
	terminal := exec.Command("ghostty", "--class="+class, "--window-padding-x=0", "--window-padding-y=0",
		"--window-padding-balance=false", "--window-decoration=none", "--scrollbar=never", "--background-opacity=1",
		"-e", "tmux", "-S", socket, "attach", "-t", "pixels")
	terminal.Env = append(os.Environ(), "TMUX=")
	if err := terminal.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.Process.Kill(); _ = terminal.Wait() })
	driver := &Tmux{Manifest: &scene.Manifest{Panes: map[string]string{"right": id}, ContentOrigin: &[2]int{}}, Monitor: monitors[monitor].Name, WindowClass: class}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state := driver.Observe("right")
		if state.Region == nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		path := filepath.Join(dir, "screen.png")
		if b, err := exec.Command("grim", "-o", driver.Monitor, path).CombinedOutput(); err != nil {
			t.Fatalf("screenshot: %v: %s", err, b)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		picture, err := png.Decode(f)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		r := state.Region
		x, y := int(math.Round(r.X*float64(picture.Bounds().Dx()))), int(math.Round((r.Y+r.H/2)*float64(picture.Bounds().Dy())))
		firstBlue := -1
		for col := max(0, x-50); col < min(picture.Bounds().Dx(), x+50); col++ {
			red, green, blue, _ := picture.At(col, y).RGBA()
			if blue > 150*257 && red < 80*257 && green < 80*257 {
				firstBlue = col
				break
			}
		}
		if firstBlue >= 0 && math.Abs(float64(firstBlue-x)) <= 1 {
			t.Logf("pane starts at pixel %d; observed %d, region=%+v", firstBlue, x, r)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("observed region did not match the colored pane; tmux metrics: %s", tmux("display-message", "-p", "-t", id, "#{pane_left} #{pane_width} #{window_cell_width} #{client_width} #{client_height} #{window_width} #{window_height} #{status} #{status-position}"))
}
