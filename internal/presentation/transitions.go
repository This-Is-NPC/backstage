package presentation

import "fmt"

func (t Transition) validate() error {
	switch t.Effect {
	case "", "fade", "morph", "zoom", "blur":
		if t.Direction != "" {
			return fmt.Errorf("transition %q does not accept a direction", t.Effect)
		}
	case "slide", "push", "wipe":
		switch t.Direction {
		case "", "left", "right", "up", "down":
		default:
			return fmt.Errorf("transition direction must be left, right, up or down")
		}
	default:
		return fmt.Errorf("unknown transition %q", t.Effect)
	}
	return nil
}

// xfadeName covers effects whose pixel motion matches the HTML compositor.
// Slide, zoom, blur and morph stay on the frame path.
func (t Transition) xfadeName() string {
	if t.Effect != "push" && t.Effect != "wipe" {
		return ""
	}
	prefix := "slide"
	if t.Effect == "wipe" {
		prefix = "wipe"
	}
	direction := t.Direction
	if direction == "" {
		direction = "left"
	}
	return prefix + direction
}
