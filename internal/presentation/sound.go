package presentation

import (
	"fmt"
	"strings"
)

type Sound struct {
	File   string   `json:"file"`
	Volume *float64 `json:"volume,omitempty"`
}

func (s Sound) audio(id string, at float64) Audio {
	return Audio{ID: id, File: s.File, Volume: s.Volume, Role: "effect", At: at, TrimEnd: true}
}

// Event sounds share the ordinary audio pipeline and its cache.
func (p *Plan) eventSounds() error {
	for i, e := range p.Document.Timeline {
		if e.Sound == nil {
			continue
		}
		if e.Sound.File == "" {
			return fmt.Errorf("event %d: sound requires file", i)
		}
		p.Document.Audio = append(p.Document.Audio, e.Sound.audio(fmt.Sprintf("event-sound-%d", i), e.At))
	}
	return nil
}

// duckMix splits the narration bus once per ducked input, preserving the voice
// itself in the final mix. The compressor follows actual samples, not cue text.
func duckMix(parts []AudioPart, filters []string) ([]string, []string) {
	var voice, ordinary, ducked []string
	for i, a := range parts {
		label := fmt.Sprintf("[a%d]", i)
		if a.Narration {
			voice = append(voice, label)
		} else if a.Duck {
			ducked = append(ducked, label)
		} else {
			ordinary = append(ordinary, label)
		}
	}
	if len(voice) == 0 || len(ducked) == 0 {
		labels := make([]string, len(parts))
		for i := range parts {
			labels[i] = fmt.Sprintf("[a%d]", i)
		}
		return filters, labels
	}
	outputs := "[voice-main]"
	for i := range ducked {
		outputs += fmt.Sprintf("[sidechain%d]", i)
	}
	filters = append(filters, fmt.Sprintf("%samix=inputs=%d:normalize=0,apad,asetnsamples=n=1024:p=1,asplit=%d%s", strings.Join(voice, ""), len(voice), len(ducked)+1, outputs))
	ordinary = append(ordinary, "[voice-main]")
	for i, label := range ducked {
		out := fmt.Sprintf("[ducked%d]", i)
		filters = append(filters, fmt.Sprintf("%s[sidechain%d]sidechaincompress=threshold=0.02:ratio=8:attack=20:release=250%s", label, i, out))
		ordinary = append(ordinary, out)
	}
	return filters, ordinary
}
