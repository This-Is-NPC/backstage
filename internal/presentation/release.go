package presentation

import (
	"fmt"
	"math"
)

// Release expands the recurring statement/title/demo/CTA structure into the
// ordinary timeline, sharing its graphics, validation, caches and compositor.
type Release struct {
	Statements []string      `json:"statements,omitempty"`
	Title      Card          `json:"title"`
	Demos      []ReleaseDemo `json:"demos"`
	CTA        Card          `json:"cta"`
}

type ReleaseDemo struct {
	Track    string  `json:"track"`
	Duration float64 `json:"duration"`
	Title    string  `json:"title,omitempty"`
	Subtitle string  `json:"subtitle,omitempty"`
}

func (p *Plan) compileRelease() error {
	r := p.Document.Release
	if r == nil {
		return nil
	}
	if len(p.Document.Timeline) > 0 {
		return fmt.Errorf("release replaces timeline; provide only one")
	}
	if len(r.Demos) == 0 {
		return fmt.Errorf("release requires at least one demo")
	}
	if (r.Title.Type != "" && r.Title.Type != "title") || (r.CTA.Type != "" && r.CTA.Type != "cta") {
		return fmt.Errorf("release requires title and CTA cards")
	}
	t := p.Document.theme()
	at := 0.0
	transition := Transition{Effect: "fade", Duration: 8 / float64(p.FPS)}
	appendEvent := func(e Event, duration float64) {
		e.At = at
		if len(p.Document.Timeline) > 0 {
			e.Transition = transition
		}
		p.Document.Timeline = append(p.Document.Timeline, e)
		at += duration
	}
	cardDuration := func(c Card) float64 {
		entry := c.Entrance.Duration
		if c.Entrance.Effect != "none" && entry == 0 {
			entry = 0.35
		}
		return math.Ceil((t.readingTime(c.text(), c.Type == "cta")+max(entry, transition.Duration))*float64(p.FPS)) / float64(p.FPS)
	}
	for _, text := range r.Statements {
		c := Card{Type: "statement", Title: text, Strike: true}
		// The strike finishes after the entrance, before the reading interval.
		appendEvent(Event{Card: &c}, cardDuration(c)+0.35)
	}
	title := r.Title
	title.Type = "title"
	appendEvent(Event{Card: &title}, cardDuration(title))
	seen := map[string]bool{}
	for i, demo := range r.Demos {
		tr, ok := p.Tracks[demo.Track]
		if !ok || !finite(demo.Duration) || demo.Duration <= 0 || demo.Duration > p.Document.Duration {
			return fmt.Errorf("release demo %d requires an available track and positive duration", i)
		}
		if seen[demo.Track] {
			return fmt.Errorf("release reuses track %q; declare a separate track instance", demo.Track)
		}
		seen[demo.Track] = true
		tr.Start = at
		p.Tracks[demo.Track] = tr
		if demo.Duration < transition.Duration {
			return fmt.Errorf("release demo %d is shorter than its transition", i)
		}
		e := Event{Layout: "release", Slots: map[string]string{"center": demo.Track}}
		if demo.Title != "" {
			e.Card = &Card{Type: "lower-third", Title: demo.Title, Subtitle: demo.Subtitle}
		}
		appendEvent(e, demo.Duration)
	}
	cta := r.CTA
	cta.Type = "cta"
	remaining := p.Document.Duration - at
	if remaining+1e-9 < cardDuration(cta) {
		return fmt.Errorf("release needs at least %.2f seconds; duration is %.2f", at+cardDuration(cta), p.Document.Duration)
	}
	appendEvent(Event{Card: &cta}, remaining)
	return nil
}

// Reframe keeps source coordinates and timing while changing the output canvas.
func (p *Plan) Reframe(aspect string) error {
	if aspect == "" {
		return nil
	}
	long, short := max(p.Width, p.Height), min(p.Width, p.Height)
	even := func(n float64) int { return max(2, int(math.Round(n/2))*2) }
	switch aspect {
	case "16:9":
		p.Width, p.Height = long, even(float64(long)*9/16)
	case "9:16":
		p.Width, p.Height = even(float64(long)*9/16), long
	case "1:1":
		p.Width, p.Height = short, short
	default:
		return fmt.Errorf("aspect must be 16:9, 9:16 or 1:1")
	}
	p.Document.Render.W, p.Document.Render.H = p.Width, p.Height
	return nil
}
