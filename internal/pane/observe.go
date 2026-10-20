package pane

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"time"

	"github.com/This-Is-NPC/backstage/internal/facts"
)

// Observer is optional: missing desktop metadata must not break a recording.
type Observer interface {
	Observe(target string) facts.VisualState
}

func (t *Tmux) Observe(target string) facts.VisualState {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	state := observeDesktop(func(query string) ([]byte, error) {
		return exec.CommandContext(ctx, "hyprctl", "-j", query).Output()
	}, t.Monitor, t.WindowClass)
	if t.Manifest != nil && t.Manifest.ContentOrigin != nil && state.window != nil {
		format := `{"left":#{pane_left},"top":#{pane_top},"width":#{pane_width},"height":#{pane_height},"cellWidth":#{window_cell_width},"cellHeight":#{window_cell_height},"columns":#{client_width},"rows":#{client_height},"windowWidth":#{window_width},"windowHeight":#{window_height},"attached":#{session_attached},"status":"#{status}","statusPosition":"#{status-position}","active":#{window_active},"zoomed":#{window_zoomed_flag},"paneActive":#{pane_active}}`
		if b, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", t.pane(target), format).Output(); err == nil {
			if region := state.paneRegion(b, *t.Manifest.ContentOrigin); region != nil {
				state.Region = region
			}
		}
	}
	return state.VisualState
}

func (g *Guest) Observe(_ string) facts.VisualState {
	ctx := g.Box.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	box := *g.Box
	box.Context = ctx
	state := observeDesktop(func(query string) ([]byte, error) {
		out, err := box.InSession("hyprctl -j " + query)
		return []byte(out), err
	}, "", "")
	if g.NativePixels {
		state.Region = state.monitor.nativeRegion(state.Region)
		if p := state.Pointer; p != nil {
			p.X, p.Y = state.monitor.nativePoint(p.X, p.Y)
		}
	}
	return state.VisualState
}

type desktopObservation struct {
	facts.VisualState
	window                  *facts.Region
	pixelWidth, pixelHeight float64
	monitor                 desktopMonitor
}

type desktopMonitor struct {
	Name          string `json:"name"`
	X, Y          float64
	Width, Height float64
	Scale         float64
	Transform     int
}

func (m desktopMonitor) pixels() (float64, float64) {
	if m.Scale <= 0 || m.Width <= 0 || m.Height <= 0 || m.Transform < 0 || m.Transform > 7 {
		return 0, 0
	}
	if m.Transform%2 == 1 {
		return m.Height, m.Width
	}
	return m.Width, m.Height
}

// nativePoint inverts the Wayland output transform for raw framebuffer capture.
func (m desktopMonitor) nativePoint(x, y float64) (float64, float64) {
	if m.Transform >= 4 {
		x = 1 - x
	}
	switch m.Transform % 4 {
	case 1:
		x, y = y, 1-x
	case 2:
		x, y = 1-x, 1-y
	case 3:
		x, y = 1-y, x
	}
	return x, y
}

func (m desktopMonitor) nativeRegion(r *facts.Region) *facts.Region {
	if r == nil {
		return nil
	}
	x1, y1 := m.nativePoint(r.X, r.Y)
	x2, y2 := m.nativePoint(r.X+r.W, r.Y+r.H)
	return &facts.Region{X: min(x1, x2), Y: min(y1, y2), W: max(x1, x2) - min(x1, x2), H: max(y1, y2) - min(y1, y2)}
}

func (m desktopMonitor) pointer(x, y float64, native bool) *facts.Pointer {
	w, h := m.pixels()
	if w == 0 || h == 0 {
		return nil
	}
	x, y = (x-m.X)*m.Scale/w, (y-m.Y)*m.Scale/h
	if x < 0 || y < 0 || x > 1 || y > 1 {
		return nil
	}
	if native {
		x, y = m.nativePoint(x, y)
	}
	return &facts.Pointer{X: x, Y: y}
}

func observeDesktop(read func(string) ([]byte, error), monitor, windowClass string) desktopObservation {
	var monitors []desktopMonitor
	b, err := read("monitors")
	if err != nil || json.Unmarshal(b, &monitors) != nil {
		return desktopObservation{}
	}
	index := -1
	for i, m := range monitors {
		if (monitor != "" && m.Name == monitor) || (monitor == "" && len(monitors) == 1) {
			index = i
			break
		}
	}
	if index < 0 {
		return desktopObservation{}
	}
	m := monitors[index]
	pw, ph := m.pixels()
	if pw == 0 || ph == 0 {
		return desktopObservation{}
	}
	w, h := pw/m.Scale, ph/m.Scale
	state := desktopObservation{pixelWidth: pw, pixelHeight: ph, monitor: m}
	type client struct {
		At      []float64 `json:"at"`
		Size    []float64 `json:"size"`
		Address string    `json:"address"`
		Class   string    `json:"class"`
	}
	var window client
	if windowClass == "" {
		if b, err := read("activewindow"); err == nil {
			if json.Unmarshal(b, &window) != nil {
				window = client{}
			}
		}
	} else if b, err := read("clients"); err == nil {
		var clients []client
		if json.Unmarshal(b, &clients) == nil {
			for _, candidate := range clients {
				if candidate.Class == windowClass {
					window = candidate
					break
				}
			}
		}
	}
	if window.Address != "" && len(window.At) == 2 && len(window.Size) == 2 {
		x, y := (window.At[0]-m.X)/w, (window.At[1]-m.Y)/h
		state.window = &facts.Region{X: x, Y: y, W: window.Size[0] / w, H: window.Size[1] / h}
		state.Region = clipRegion(*state.window)
	}
	var pointer struct{ X, Y *float64 }
	if b, err := read("cursorpos"); err == nil && json.Unmarshal(b, &pointer) == nil && pointer.X != nil && pointer.Y != nil {
		state.Pointer = m.pointer(*pointer.X, *pointer.Y, false)
	}
	return state
}

func (s desktopObservation) paneRegion(data []byte, origin [2]int) *facts.Region {
	var p struct {
		Left, Top, Width, Height, CellWidth, CellHeight int
		Columns, Rows, WindowWidth, WindowHeight        int
		Attached                                        int
		Active, Zoomed, PaneActive                      int
		Status, StatusPosition                          string
	}
	if json.Unmarshal(data, &p) != nil {
		return nil
	}
	status, err := strconv.Atoi(p.Status)
	switch p.Status {
	case "on":
		status, err = 1, nil
	case "off":
		status, err = 0, nil
	}
	if err != nil || s.window == nil || s.pixelWidth <= 0 || s.pixelHeight <= 0 ||
		p.CellWidth <= 0 || p.CellHeight <= 0 || p.Width <= 0 || p.Height <= 0 || p.Left < 0 || p.Top < 0 ||
		p.Attached != 1 || p.Active != 1 || (p.Zoomed != 0 && p.PaneActive != 1) ||
		p.Columns != p.WindowWidth || p.Rows != p.WindowHeight+status ||
		p.Left+p.Width > p.WindowWidth || p.Top+p.Height > p.WindowHeight ||
		status < 0 || status > 5 || (p.StatusPosition != "top" && p.StatusPosition != "bottom") ||
		origin[0] < 0 || origin[1] < 0 {
		return nil
	}
	// A zero-padding terminal can leave less than one cell at the right and
	// bottom edges. A larger mismatch indicates unaccounted chrome or scaling.
	remainingX := s.window.W*s.pixelWidth - float64(origin[0]+p.Columns*p.CellWidth)
	remainingY := s.window.H*s.pixelHeight - float64(origin[1]+p.Rows*p.CellHeight)
	if remainingX < -1 || remainingY < -1 || remainingX >= float64(p.CellWidth)+1 || remainingY >= float64(p.CellHeight)+1 {
		return nil
	}
	if p.StatusPosition == "top" {
		p.Top += status
	}
	return clipRegion(facts.Region{
		X: s.window.X + float64(origin[0]+p.Left*p.CellWidth)/s.pixelWidth,
		Y: s.window.Y + float64(origin[1]+p.Top*p.CellHeight)/s.pixelHeight,
		W: float64(p.Width*p.CellWidth) / s.pixelWidth,
		H: float64(p.Height*p.CellHeight) / s.pixelHeight,
	})
}

func clipRegion(region facts.Region) *facts.Region {
	right, bottom := min(1, region.X+region.W), min(1, region.Y+region.H)
	region.X, region.Y = max(0, region.X), max(0, region.Y)
	region.W, region.H = right-region.X, bottom-region.Y
	if region.W <= 0 || region.H <= 0 {
		return nil
	}
	return &region
}
