package presentation

import (
	"fmt"
	"regexp"
	"strings"
)

// Theme sets typography, brand colors and minimum reading times.
type Theme struct {
	Font           string  `json:"font,omitempty"`
	Background     string  `json:"background,omitempty"`
	Foreground     string  `json:"foreground,omitempty"`
	Accent         string  `json:"accent,omitempty"`
	MinFont        float64 `json:"min-font,omitempty"`
	TitleSize      float64 `json:"title-size,omitempty"`
	TextSize       float64 `json:"text-size,omitempty"`
	CaptionSize    float64 `json:"caption-size,omitempty"`
	SecondsPerWord float64 `json:"seconds-per-word,omitempty"`
	CTADuration    float64 `json:"cta-duration,omitempty"`
	Ease           string  `json:"ease,omitempty"`
}

type Motion struct {
	Effect   string  `json:"effect,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	Ease     string  `json:"ease,omitempty"`
}

// Card is an editorial overlay on an event, or a full-screen event by itself.
type Card struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Text     string `json:"text,omitempty"`
	URL      string `json:"url,omitempty"`
	Logo     string `json:"logo,omitempty"`
	Strike   bool   `json:"strike,omitempty"`
	Entrance Motion `json:"entrance,omitempty"`
}

func (d Document) theme() Theme {
	t := Theme{}
	if d.Theme != nil {
		t = *d.Theme
	}
	if t.Font == "" {
		t.Font = "system-ui, sans-serif"
	}
	if t.Background == "" {
		t.Background = "#11111a"
	}
	if t.Foreground == "" {
		t.Foreground = "#e4e8f7"
	}
	if t.Accent == "" {
		t.Accent = "#f7768e"
	}
	if t.MinFont == 0 {
		t.MinFont = 26
	}
	if t.TitleSize == 0 {
		t.TitleSize = 72
	}
	if t.TextSize == 0 {
		t.TextSize = 36
	}
	if t.CaptionSize == 0 {
		t.CaptionSize = 32
	}
	if t.SecondsPerWord == 0 {
		t.SecondsPerWord = 0.3
	}
	if t.CTADuration == 0 {
		t.CTADuration = 3.5
	}
	if t.Ease == "" {
		t.Ease = "ease-out"
	}
	return t
}

func validEase(e string) bool {
	return e == "" || e == "linear" || e == "ease-out" || e == "ease-in-out"
}

func (m *Motion) validate(fallback string, duration float64) error {
	if m.Effect == "" {
		m.Effect = fallback
	}
	switch m.Effect {
	case "none", "rise", "fade", "type", "scale":
	default:
		return fmt.Errorf("entrance effect must be none, rise, fade, type or scale")
	}
	if m.Duration == 0 && m.Effect != "none" {
		m.Duration = 0.35
	}
	if !validTime(m.Duration) || m.Duration > duration || !validEase(m.Ease) {
		return fmt.Errorf("entrance needs a valid easing and duration within its event")
	}
	return nil
}

var hexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

func (p *Plan) validateGraphics() error {
	t := p.Document.theme()
	for _, size := range []float64{t.MinFont, t.TitleSize, t.TextSize, t.CaptionSize} {
		if !finite(size) || size <= 0 || size < t.MinFont || size > 512 {
			return fmt.Errorf("theme fonts must be between min-font and 512 px")
		}
	}
	if !hexColor.MatchString(t.Background) || !hexColor.MatchString(t.Foreground) || !hexColor.MatchString(t.Accent) {
		return fmt.Errorf("theme colors must be hexadecimal CSS colors")
	}
	if !validEase(t.Ease) || !finite(t.SecondsPerWord) || t.SecondsPerWord <= 0 || !finite(t.CTADuration) || t.CTADuration <= 0 {
		return fmt.Errorf("theme needs valid easing and positive reading times")
	}
	for i := range p.Document.Timeline {
		e := &p.Document.Timeline[i]
		if e.Card == nil {
			continue
		}
		c := e.Card
		switch c.Type {
		case "title", "lower-third", "badge", "statement", "cta":
		default:
			return fmt.Errorf("event %d: unknown card type %q", i, c.Type)
		}
		if strings.TrimSpace(c.Title) == "" {
			return fmt.Errorf("event %d: card requires a title", i)
		}
		if c.Strike && c.Type != "statement" {
			return fmt.Errorf("event %d: strike requires a statement card", i)
		}
		end := p.Document.Duration
		if i+1 < len(p.Document.Timeline) {
			end = p.Document.Timeline[i+1].At
		}
		if err := c.Entrance.validate("rise", end-e.At); err != nil {
			return fmt.Errorf("event %d: %w", i, err)
		}
		if err := t.readable(c.text(), end-e.At-max(c.Entrance.Duration, e.Transition.Duration), c.Type == "cta"); err != nil {
			return fmt.Errorf("event %d: %w", i, err)
		}
		if c.Logo != "" {
			if _, err := p.input(c.Logo); err != nil {
				return fmt.Errorf("card logo: %w", err)
			}
		}
	}
	return nil
}

func (t Theme) readable(text string, seconds float64, cta bool) error {
	minimum := t.readingTime(text, cta)
	if seconds+1e-9 < minimum {
		return fmt.Errorf("text needs at least %.2f readable seconds; got %.2f", minimum, seconds)
	}
	return nil
}

func (c Card) text() string { return strings.Join([]string{c.Title, c.Subtitle, c.Text, c.URL}, " ") }

func (t Theme) readingTime(text string, cta bool) float64 {
	minimum := float64(len(strings.Fields(text))) * t.SecondsPerWord
	if cta {
		minimum = max(minimum, t.CTADuration)
	}
	return minimum
}

func (p *Plan) graphicsMoving(t float64) bool {
	if p.actionsMoving(t) {
		return true
	}
	i := 0
	for i+1 < len(p.Document.Timeline) && p.Document.Timeline[i+1].At <= t {
		i++
	}
	if len(p.Document.Timeline) > 0 {
		for j := i; j >= max(0, i-1); j-- {
			if j < i && t >= p.Document.Timeline[i].At+p.Document.Timeline[i].Transition.Duration {
				break
			}
			e := p.Document.Timeline[j]
			if c := e.Card; c != nil && ((c.Entrance.Effect != "none" && t < e.At+c.Entrance.Duration) || (c.Strike && t < e.At+c.Entrance.Duration+0.35)) {
				return true
			}
		}
	}
	for _, c := range p.Text {
		if c.Entrance.Effect != "" && c.Entrance.Effect != "none" && t >= c.At && t < min(c.End, c.At+c.Entrance.Duration) {
			return true
		}
	}
	return false
}
