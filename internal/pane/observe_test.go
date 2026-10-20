package pane

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/facts"
)

func TestObserveNormalizesActualWindowAndPointer(t *testing.T) {
	responses := map[string]string{
		"monitors":  `[{"name":"left","x":-1280,"y":0,"width":2560,"height":1440,"scale":2}]`,
		"clients":   `[{"class":"other","address":"0x1","at":[0,0],"size":[50,50]},{"class":"stage","address":"0x2","at":[-960,180],"size":[640,360]}]`,
		"cursorpos": `{"x":-640,"y":360}`,
	}
	read := func(query string) ([]byte, error) {
		body, ok := responses[query]
		if !ok {
			return nil, fmt.Errorf("unexpected query %s", query)
		}
		return []byte(body), nil
	}
	s := observeDesktop(read, "left", "stage")
	if s.Region == nil || *s.Region != (facts.Region{X: 0.25, Y: 0.25, W: 0.5, H: 0.5}) || s.Pointer == nil || s.Pointer.X != 0.5 || s.Pointer.Y != 0.5 || s.Pointer.Click {
		t.Fatalf("state=%+v", s)
	}
	responses["monitors"] = `[{"name":"left","x":-1280,"y":0,"width":2560,"height":1440,"scale":2},{"name":"right","x":0,"y":0,"width":1920,"height":1080,"scale":1}]`
	if s := observeDesktop(read, "", "stage"); s.Region != nil || s.Pointer != nil {
		t.Fatal("guessed a monitor", s)
	}
	responses["cursorpos"] = `{}`
	responses["clients"] = `[{"class":"stage","address":"0x2"}]`
	if s := observeDesktop(read, "left", "stage"); s.Region != nil || s.Pointer != nil {
		t.Fatal("invented missing coordinates", s)
	}
	responses["monitors"] = `[{"name":"left","width":2560,"height":1440,"scale":2,"transform":1}]`
	responses["cursorpos"] = `{"x":180,"y":320}`
	if s := observeDesktop(read, "left", "stage"); s.Pointer == nil || s.Pointer.X != 0.25 || s.Pointer.Y != 0.25 {
		t.Fatal("rotated output did not swap its logical extents", s)
	}
}

func TestOutputTransformsMapPointerAndRegionToFramebuffer(t *testing.T) {
	want := [][2]float64{{.2, .3}, {.3, .8}, {.8, .7}, {.7, .2}, {.8, .3}, {.3, .2}, {.2, .7}, {.7, .8}}
	for transform, point := range want {
		m := desktopMonitor{Width: 1920, Height: 1080, Scale: 1.5, X: -100, Y: 40, Transform: transform}
		w, h := m.pixels()
		p := m.pointer(m.X+.2*w/m.Scale, m.Y+.3*h/m.Scale, true)
		if p == nil || math.Abs(p.X-point[0])+math.Abs(p.Y-point[1]) > 1e-9 {
			t.Fatalf("transform %d: %+v", transform, p)
		}
		r := m.nativeRegion(&facts.Region{X: .2, Y: .3, W: .1, H: .2})
		if r == nil || !validTestRegion(*r) {
			t.Fatalf("transform %d: %+v", transform, r)
		}
		if transform%2 == 0 && math.Abs(r.W-.1) > 1e-9 || transform%2 == 1 && math.Abs(r.W-.2) > 1e-9 {
			t.Fatalf("extent %d: %+v", transform, r)
		}
	}
}

func validTestRegion(r facts.Region) bool {
	return r.X >= 0 && r.Y >= 0 && r.W > 0 && r.H > 0 && r.X+r.W <= 1 && r.Y+r.H <= 1
}

func TestPaneRegionUsesReportedPixelsAndStatusPosition(t *testing.T) {
	s := desktopObservation{window: &facts.Region{X: 0.25, Y: 0.25, W: 0.5, H: 0.5}, pixelWidth: 2560, pixelHeight: 1440}
	metrics := map[string]any{
		"left": 64, "top": 0, "width": 64, "height": 35, "cellWidth": 10, "cellHeight": 20,
		"columns": 128, "rows": 36, "windowWidth": 128, "windowHeight": 35,
		"attached": 1, "active": 1, "status": "on", "statusPosition": "bottom",
	}
	check := func(want facts.Region) {
		t.Helper()
		b, err := json.Marshal(metrics)
		if err != nil {
			t.Fatal(err)
		}
		got := s.paneRegion(b, [2]int{})
		if got == nil || math.Abs(got.X-want.X)+math.Abs(got.Y-want.Y)+math.Abs(got.W-want.W)+math.Abs(got.H-want.H) > 1e-9 {
			t.Fatalf("region=%+v, want %+v", got, want)
		}
	}
	check(facts.Region{X: 0.5, Y: 0.25, W: 0.25, H: 700.0 / 1440})
	metrics["statusPosition"] = "top"
	check(facts.Region{X: 0.5, Y: 0.25 + 20.0/1440, W: 0.25, H: 700.0 / 1440})
	metrics["status"] = "off"
	metrics["rows"] = 35
	s.window.H = 700.0 / 1440
	check(facts.Region{X: 0.5, Y: 0.25, W: 0.25, H: 700.0 / 1440})
	metrics["left"] = 0
	metrics["width"] = 128
	s.window.X = -0.1
	check(facts.Region{X: 0, Y: 0.25, W: 0.4, H: 700.0 / 1440})
	for _, change := range []map[string]any{
		{"cellWidth": 0}, {"cellHeight": 0}, {"left": -1}, {"width": 129},
		{"attached": 2}, {"active": 0},
		{"zoomed": 1, "paneActive": 0}, {"columns": 127}, {"rows": 36},
		{"cellWidth": 9}, {"statusPosition": "unknown"},
	} {
		candidate := make(map[string]any, len(metrics))
		for key, value := range metrics {
			candidate[key] = value
		}
		for key, value := range change {
			candidate[key] = value
		}
		b, _ := json.Marshal(candidate)
		if got := s.paneRegion(b, [2]int{}); got != nil {
			t.Fatalf("invented geometry for %v: %+v", change, got)
		}
	}
}
