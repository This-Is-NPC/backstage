package presentation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

// TimeReference anchors an event to one source step or narration cue.
// Offset is measured in presentation seconds, after track retiming.
type TimeReference struct {
	Step   string  `json:"step,omitempty"`
	Cue    string  `json:"cue,omitempty"`
	Track  string  `json:"track,omitempty"`
	Scene  string  `json:"scene,omitempty"`
	Edge   string  `json:"edge,omitempty"`
	Offset float64 `json:"offset,omitempty"`
}

// decodeTimed keeps the numeric Go model while accepting declarative anchors.
// Decoding through an alias preserves strict unknown-field validation.
func decodeTimed(data []byte, into any, ref **TimeReference) error {
	*ref = nil
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	at := bytes.TrimSpace(fields["at"])
	if len(at) > 0 && at[0] == '{' {
		var r TimeReference
		if err := decodeJSON(bytes.NewReader(at), &r); err != nil {
			return fmt.Errorf("at: %w", err)
		}
		if (r.Step == "") == (r.Cue == "") || !finite(r.Offset) || (r.Edge != "" && r.Edge != "start" && r.Edge != "end") {
			return fmt.Errorf("at requires exactly one step or cue, a finite offset, and edge start or end")
		}
		*ref = &r
		fields["at"] = json.RawMessage("0")
		var err error
		data, err = json.Marshal(fields)
		if err != nil {
			return err
		}
	}
	return decodeJSON(bytes.NewReader(data), into)
}

func (e *Event) UnmarshalJSON(b []byte) error {
	type plain Event
	return decodeTimed(b, (*plain)(e), &e.atRef)
}
func (a *Audio) UnmarshalJSON(b []byte) error {
	type plain Audio
	return decodeTimed(b, (*plain)(a), &a.atRef)
}
func (c *Caption) UnmarshalJSON(b []byte) error {
	type plain Caption
	return decodeTimed(b, (*plain)(c), &c.atRef)
}

func (p *Plan) resolveTime(r *TimeReference) (float64, error) {
	track := r.Track
	if track == "" {
		for _, id := range sortedKeys(p.Tracks) {
			src := p.Document.Sources[p.Tracks[id].Source]
			if r.Scene != "" && src.Scene != r.Scene {
				continue
			}
			if track != "" {
				return 0, fmt.Errorf("ambiguous time reference: specify track")
			}
			track = id
		}
	}
	tr, ok := p.Tracks[track]
	if !ok {
		return 0, fmt.Errorf("time reference requires a source track")
	}
	sourceTime := 0.0
	if r.Cue != "" {
		cue, err := p.cue(r.Scene, track, r.Cue)
		if err != nil {
			return 0, err
		}
		sourceTime = cue.Start
		if r.Edge == "end" {
			sourceTime = cue.End
		}
	} else {
		steps, err := p.recordedSteps(track)
		if err != nil {
			return 0, fmt.Errorf("step %q requires recorded step timings: %w", r.Step, err)
		}
		found := false
		for _, step := range steps {
			if step.ID != r.Step {
				continue
			}
			found = true
			sourceTime = step.Start
			if r.Edge == "end" {
				sourceTime = step.End
			}
		}
		if !found {
			return 0, fmt.Errorf("unknown recorded step %q", r.Step)
		}
	}
	pos := tr.Start
	resolved := math.NaN()
	for _, s := range tr.Segments {
		if sourceTime >= s.From && (sourceTime < s.To || (r.Edge == "end" && sourceTime == s.To)) {
			candidate := pos + (sourceTime-s.From)/s.Rate
			if !math.IsNaN(resolved) && math.Abs(candidate-resolved) > 1e-9 {
				return 0, fmt.Errorf("time reference appears in repeated segments; use a separate track")
			}
			resolved = candidate
		}
		pos += (s.To - s.From) / s.Rate
	}
	resolved += r.Offset
	if !validTime(resolved) || resolved >= p.Document.Duration {
		return 0, fmt.Errorf("time reference is outside the selected presentation interval")
	}
	return resolved, nil
}

func (p *Plan) resolveTimes() error {
	resolve := func(at *float64, r *TimeReference) error {
		if r == nil {
			return nil
		}
		v, err := p.resolveTime(r)
		if err == nil {
			*at = v
		}
		return err
	}
	for i := range p.Document.Timeline {
		e := &p.Document.Timeline[i]
		if err := resolve(&e.At, e.atRef); err != nil {
			return fmt.Errorf("timeline event %d: %w", i, err)
		}
	}
	for i := range p.Document.Audio {
		a := &p.Document.Audio[i]
		if err := resolve(&a.At, a.atRef); err != nil {
			return fmt.Errorf("audio %q: %w", a.ID, err)
		}
	}
	for i := range p.Document.Captions {
		c := &p.Document.Captions[i]
		if err := resolve(&c.At, c.atRef); err != nil {
			return fmt.Errorf("caption %d: %w", i, err)
		}
	}
	return nil
}
