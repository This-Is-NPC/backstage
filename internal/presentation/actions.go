package presentation

import (
	"fmt"
	"math"
	"sort"

	"github.com/This-Is-NPC/backstage/internal/facts"
)

// ActionOverlay selects recorded evidence to show on top of a source track.
type ActionOverlay struct {
	Track     string  `json:"track"`
	Step      string  `json:"step,omitempty"`
	Keys      bool    `json:"keys,omitempty"`
	Highlight string  `json:"highlight,omitempty"`
	Cursor    bool    `json:"cursor,omitempty"`
	Region    *Region `json:"region,omitempty"`
	Duration  float64 `json:"duration,omitempty"`
	Sound     *Sound  `json:"sound,omitempty"`
}

type ActionSpan struct {
	Track       string                `json:"track"`
	At          float64               `json:"at"`
	End         float64               `json:"end"`
	Keys        []string              `json:"keys,omitempty"`
	Highlight   string                `json:"highlight,omitempty"`
	Region      *Region               `json:"region,omitempty"`
	Pointer     *facts.Pointer        `json:"pointer,omitempty"`
	Trace       []facts.PointerSample `json:"pointer-trace,omitempty"`
	SourceStart float64               `json:"source-start,omitempty"`
	Rate        float64               `json:"rate,omitempty"`
}

func (p *Plan) compileActions() error {
	for i, overlay := range p.Document.Actions {
		tr, ok := p.Tracks[overlay.Track]
		if !ok || (!overlay.Keys && overlay.Highlight == "" && !overlay.Cursor && overlay.Sound == nil) || !validTime(overlay.Duration) {
			return fmt.Errorf("action overlay %d requires a known track, a visible effect or sound, and nonnegative duration", i)
		}
		if overlay.Highlight != "" && overlay.Highlight != "outline" && overlay.Highlight != "spotlight" {
			return fmt.Errorf("action overlay %d: highlight must be outline or spotlight", i)
		}
		if overlay.Region != nil && !validRegion(*overlay.Region) {
			return fmt.Errorf("action overlay %d: invalid source region", i)
		}
		if overlay.Sound != nil && overlay.Sound.File == "" {
			return fmt.Errorf("action overlay %d: sound requires file", i)
		}
		recorded, err := p.recordedFacts(overlay.Track)
		if err != nil {
			return err
		}
		found := overlay.Step == ""
		for _, step := range recorded.Steps {
			if overlay.Step != "" && overlay.Step != step.ID {
				continue
			}
			found = true
			region := step.Region
			if overlay.Region != nil {
				region = overlay.Region
			}
			if overlay.Step != "" && overlay.Highlight != "" && region == nil {
				return fmt.Errorf("step %q has no recorded region; supply a region", step.ID)
			}
			for _, span := range mapSpan(tr, step.Start, step.End) {
				at, end := span.At, math.Min(span.End, p.Document.Duration)
				if overlay.Duration > 0 {
					end = math.Min(at+overlay.Duration, p.Document.Duration)
				}
				if at >= end {
					continue
				}
				a := ActionSpan{Track: overlay.Track, At: at, End: end}
				if overlay.Keys {
					a.Keys = step.Keys
				}
				if overlay.Highlight != "" && region != nil {
					a.Highlight, a.Region = overlay.Highlight, region
				}
				if overlay.Cursor && len(recorded.PointerTrace) == 0 && step.Pointer != nil {
					a.Pointer = step.Pointer
				}
				p.Actions = append(p.Actions, a)
				if overlay.Sound != nil {
					p.Document.Audio = append(p.Document.Audio, overlay.Sound.audio(fmt.Sprintf("action-sound-%d-%d", i, len(p.Actions)), at))
				}
			}
		}
		if !found {
			return fmt.Errorf("unknown recorded step %q", overlay.Step)
		}
		if overlay.Cursor && len(recorded.PointerTrace) > 0 {
			start, end := 0.0, tr.Media.Duration
			if overlay.Step != "" {
				for _, step := range recorded.Steps {
					if step.ID == overlay.Step {
						start, end = step.Start, step.End
						break
					}
				}
			}
			for _, span := range mapSpan(tr, start, end) {
				if span.At >= p.Document.Duration {
					continue
				}
				trace := recorded.PointerTrace
				lo := sort.Search(len(trace), func(i int) bool { return trace[i].At >= span.SourceStart })
				hi := sort.Search(len(trace), func(i int) bool { return trace[i].At > span.SourceEnd })
				lo, hi = max(0, lo-1), min(len(trace), hi+1)
				if hi > lo {
					p.Actions = append(p.Actions, ActionSpan{Track: overlay.Track, At: span.At, End: min(span.End, p.Document.Duration), Trace: trace[lo:hi], SourceStart: span.SourceStart, Rate: span.Rate})
				}
			}
		}
	}
	return nil
}

func (p *Plan) actionKey(t float64) string {
	if len(p.Actions) == 0 {
		return ""
	}
	var spans []ActionSpan
	for _, a := range p.Actions {
		if t >= a.At && t < a.End {
			spans = append(spans, a)
		}
	}
	return hashJSON(spans)
}

func (p *Plan) actionsMoving(t float64) bool {
	for _, a := range p.Actions {
		if t < a.At || t >= a.End {
			continue
		}
		if len(a.Trace) > 0 {
			return true
		}
		if a.Pointer != nil && a.Pointer.Click && t < a.At+0.35 {
			return true
		}
		if a.Region != nil || a.Pointer != nil {
			for i, e := range p.Document.Timeline {
				if e.At > t {
					break
				}
				next := p.Document.Duration
				if i+1 < len(p.Document.Timeline) {
					next = p.Document.Timeline[i+1].At
				}
				if t >= next {
					continue
				}
				c := p.eventCamera(i, a.Track)
				if c.region(t) != c.region(math.Max(e.At, t-1/float64(p.FPS))) {
					return true
				}
			}
		}
	}
	return false
}
