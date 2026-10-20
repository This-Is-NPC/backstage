package scene

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Composition describes a visual scene without a project-specific renderer.
// All times are seconds on the scene clock, including nested nodes.
type Composition struct {
	Width       int          `json:"width"`
	Height      int          `json:"height"`
	Stylesheets []string     `json:"stylesheets,omitempty"`
	Nodes       []VisualNode `json:"nodes"`
}

type VisualNode struct {
	Tag        string            `json:"tag,omitempty"`
	Text       string            `json:"text,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Style      map[string]string `json:"style,omitempty"`
	Children   []VisualNode      `json:"children,omitempty"`
	Start      float64           `json:"start,omitempty"`
	End        float64           `json:"end,omitempty"`
	Animate    []VisualAnimation `json:"animate,omitempty"`
}

type VisualAnimation struct {
	Property  string           `json:"property"`
	Keyframes []VisualKeyframe `json:"keyframes"`
}

type VisualKeyframe struct {
	At    float64 `json:"at"`
	Value string  `json:"value"`
	Ease  string  `json:"ease,omitempty"`
}

var visualTags = strings.Fields("div span p h1 h2 h3 h4 h5 h6 section article header footer main aside nav ul ol li a strong em b i small label button input table thead tbody tfoot tr th td br hr img mark time svg g path rect circle ellipse line polyline polygon defs clipPath mask linearGradient radialGradient stop text tspan #text")
var visualName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_:-]*$`)
var visualEase = regexp.MustCompile(`^(linear|ease|ease-in|ease-out|ease-in-out|step-start|step-end|cubic-bezier\(\s*-?[0-9.]+\s*,\s*-?[0-9.]+\s*,\s*-?[0-9.]+\s*,\s*-?[0-9.]+\s*\)|steps\([1-9][0-9]*(,\s*(start|end))?\))$`)

func (c *Composition) validate(p *Project, duration float64) error {
	if c.Width <= 0 || c.Height <= 0 || len(c.Nodes) == 0 {
		return fmt.Errorf("composition requires positive width, height and nodes")
	}
	for _, path := range c.Stylesheets {
		if _, err := p.InputPath(path); err != nil {
			return err
		}
	}
	var visit func(VisualNode) error
	visit = func(n VisualNode) error {
		tag := n.Tag
		if tag == "" {
			tag = "div"
		}
		allowed := false
		for _, candidate := range visualTags {
			allowed = allowed || candidate == tag
		}
		if !allowed {
			return fmt.Errorf("unsupported composition tag %q", tag)
		}
		if n.Start < 0 || n.Start >= duration || n.End < 0 || n.End > duration || (n.End != 0 && n.End <= n.Start) {
			return fmt.Errorf("invalid composition node interval")
		}
		if tag == "#text" && (len(n.Attributes)+len(n.Style)+len(n.Children)+len(n.Animate) > 0 || n.Start != 0 || n.End != 0) {
			return fmt.Errorf("text nodes only accept text")
		}
		if n.Text != "" && len(n.Children) > 0 {
			return fmt.Errorf("composition nodes cannot combine text and children")
		}
		for key, value := range n.Attributes {
			lower := strings.ToLower(key)
			if !visualName.MatchString(key) || strings.HasPrefix(lower, "on") || lower == "srcdoc" || lower == "style" {
				return fmt.Errorf("unsupported composition attribute %q", key)
			}
			if lower == "href" || lower == "xlink:href" {
				if value != "" && !strings.HasPrefix(value, "#") {
					return fmt.Errorf("composition links must be fragment references")
				}
			}
			if lower == "src" {
				if tag != "img" {
					return fmt.Errorf("composition src requires img")
				}
				if _, err := p.InputPath(value); err != nil {
					return err
				}
			}
		}
		properties := map[string]bool{}
		for _, animation := range n.Animate {
			if !visualName.MatchString(animation.Property) || properties[animation.Property] || len(animation.Keyframes) < 2 {
				return fmt.Errorf("composition animations require unique properties and at least two keyframes")
			}
			properties[animation.Property] = true
			previous := -1.0
			for _, key := range animation.Keyframes {
				if math.IsNaN(key.At) || math.IsInf(key.At, 0) || key.At < 0 || key.At > duration || key.At <= previous || key.Value == "" || (key.Ease != "" && !visualEase.MatchString(key.Ease)) {
					return fmt.Errorf("invalid composition keyframe for %q", animation.Property)
				}
				previous = key.At
			}
		}
		for _, child := range n.Children {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, node := range c.Nodes {
		if err := visit(node); err != nil {
			return err
		}
	}
	return nil
}
