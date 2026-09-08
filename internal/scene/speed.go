package scene

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// SceneRef is one scene in a production, and how to present it.
//
// A production may name a scene as a bare string or as an object. The string
// form is the ordinary case and stays the ordinary case; the object form
// carries what the *presentation* decides, which is never what the recording
// decided.
//
// Speed lives here and not on the scene for that reason. A take of fifty real
// minutes is the evidence. Whether it is published at ten times or at six is a
// question somebody answers afterwards, more than once, without recording
// anything again -- and a clip written to disk already fast has thrown that
// away.
type SceneRef struct {
	Scene string    `json:"scene"`
	Speed []Segment `json:"speed,omitempty"`
}

// Segment is a stretch of a clip played at one rate.
//
// `Until` is where the stretch ends, measured in the *source* clip. The last
// segment leaves it empty and runs to the end. Segments are read in order and
// each one begins where the last ended, so a production never states a start:
// two numbers that have to agree are two numbers that will one day disagree.
type Segment struct {
	Until string  `json:"until,omitempty"`
	Rate  float64 `json:"rate"`
	// Badge is drawn over this stretch while it plays, so nobody mistakes a
	// fast clip for a fast machine.
	Badge string `json:"badge,omitempty"`
}

// UnmarshalJSON accepts a bare scene name or an object.
func (r *SceneRef) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, `"`) {
		return json.Unmarshal(data, &r.Scene)
	}
	type plain SceneRef
	var spelled plain
	if err := json.Unmarshal(data, &spelled); err != nil {
		return err
	}
	*r = SceneRef(spelled)
	if r.Scene == "" {
		return fmt.Errorf("a production names a scene with no `scene`")
	}
	return nil
}

// MarshalJSON writes the short form when there is nothing else to say.
func (r SceneRef) MarshalJSON() ([]byte, error) {
	if len(r.Speed) == 0 {
		return json.Marshal(r.Scene)
	}
	type plain SceneRef
	return json.Marshal(plain(r))
}

// Seconds reads `48:00`, `1:02:30` or `90`.
//
// Written out rather than taken from a duration parser, because `1:00` in a
// production means a minute and `1m` would be a second way to write the same
// thing. One spelling for one meaning.
func Seconds(at string) (float64, error) {
	at = strings.TrimSpace(at)
	if at == "" {
		return 0, fmt.Errorf("no time given")
	}
	parts := strings.Split(at, ":")
	if len(parts) > 3 {
		return 0, fmt.Errorf("%q is not a time; write seconds, mm:ss or hh:mm:ss", at)
	}
	total := 0.0
	for _, part := range parts {
		value, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil || value < 0 {
			return 0, fmt.Errorf("%q is not a time; write seconds, mm:ss or hh:mm:ss", at)
		}
		total = total*60 + value
	}
	return total, nil
}

// Cuts turns the segments into the boundaries of a clip that is `length`
// seconds long.
//
// Each cut carries the piece's start, its end and its rate. The last segment
// runs to the end whatever it says, because a production that had to know the
// length of its own take would be a production that breaks when the take gets
// a second longer.
type Cut struct {
	From, To, Rate float64
	Badge          string
}

// Cuts validates the segments and returns them as boundaries.
func Cuts(segments []Segment, length float64) ([]Cut, error) {
	if len(segments) == 0 {
		return []Cut{{From: 0, To: length, Rate: 1}}, nil
	}
	cuts := make([]Cut, 0, len(segments))
	at := 0.0
	for index, segment := range segments {
		rate := segment.Rate
		if rate <= 0 {
			return nil, fmt.Errorf("segment %d has rate %v; a rate is a positive number",
				index+1, segment.Rate)
		}
		last := index == len(segments)-1
		to := length
		if !last {
			if segment.Until == "" {
				return nil, fmt.Errorf("segment %d has no `until`; only the last one may",
					index+1)
			}
			seconds, err := Seconds(segment.Until)
			if err != nil {
				return nil, fmt.Errorf("segment %d: %w", index+1, err)
			}
			to = seconds
		} else if segment.Until != "" {
			// Said and ignored is worse than refused: somebody wrote a number
			// meaning it to be obeyed.
			return nil, fmt.Errorf("the last segment has `until %s`; it runs to the end "+
				"of the clip and must not say where that is", segment.Until)
		}
		if to <= at {
			return nil, fmt.Errorf("segment %d ends at %.0fs, which is not after %.0fs",
				index+1, to, at)
		}
		if to > length {
			return nil, fmt.Errorf("segment %d ends at %.0fs and the clip is %.0fs long",
				index+1, to, length)
		}
		cuts = append(cuts, Cut{From: at, To: to, Rate: rate, Badge: segment.Badge})
		at = to
	}
	return cuts, nil
}
