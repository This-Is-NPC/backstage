package scene

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSceneRefReadsBothSpellings(t *testing.T) {
	var short SceneRef
	if err := json.Unmarshal([]byte(`"06-the-panel"`), &short); err != nil {
		t.Fatal(err)
	}
	if short.Scene != "06-the-panel" || len(short.Speed) != 0 {
		t.Errorf("short form = %+v", short)
	}

	var long SceneRef
	body := `{"scene":"07-a-real-hour","speed":[{"until":"1:00","rate":1},{"rate":10,"badge":"10x"}]}`
	if err := json.Unmarshal([]byte(body), &long); err != nil {
		t.Fatal(err)
	}
	if long.Scene != "07-a-real-hour" || len(long.Speed) != 2 {
		t.Fatalf("long form = %+v", long)
	}
	if long.Speed[1].Badge != "10x" {
		t.Errorf("badge = %q", long.Speed[1].Badge)
	}
}

func TestSceneRefRefusesAnObjectWithNoScene(t *testing.T) {
	var ref SceneRef
	err := json.Unmarshal([]byte(`{"speed":[{"rate":10}]}`), &ref)
	if err == nil {
		t.Fatal("a production named a speed and no scene, and it was accepted")
	}
	if !strings.Contains(err.Error(), "scene") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}
}

func TestSceneRefWritesTheShortFormWhenItCan(t *testing.T) {
	body, err := json.Marshal(SceneRef{Scene: "06-the-panel"})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `"06-the-panel"` {
		t.Errorf("marshalled %s, want the bare name", body)
	}
}

func TestSecondsReadsTheThreeSpellings(t *testing.T) {
	for _, c := range []struct {
		at   string
		want float64
	}{
		{"90", 90}, {"1:30", 90}, {"48:00", 2880}, {"1:02:30", 3750},
	} {
		got, err := Seconds(c.at)
		if err != nil || got != c.want {
			t.Errorf("Seconds(%q) = %v, %v; want %v", c.at, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "soon", "1:2:3:4", "-5", "1:xx"} {
		if _, err := Seconds(bad); err == nil {
			t.Errorf("Seconds(%q) was accepted", bad)
		}
	}
}

func TestCutsWithNoSegmentsIsTheWholeClipAtRealTime(t *testing.T) {
	cuts, err := Cuts(nil, 600)
	if err != nil {
		t.Fatal(err)
	}
	if len(cuts) != 1 || cuts[0].Rate != 1 || cuts[0].To != 600 {
		t.Errorf("cuts = %+v", cuts)
	}
}

// The last segment runs to the end whatever the clip turns out to be. A
// production that had to know the length of its own take would break the day
// the take got a second longer.
func TestCutsLetTheLastSegmentRunToTheEnd(t *testing.T) {
	segments := []Segment{
		{Until: "1:00", Rate: 1},
		{Until: "48:00", Rate: 10, Badge: "10x"},
		{Rate: 1},
	}
	cuts, err := Cuts(segments, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if len(cuts) != 3 {
		t.Fatalf("cuts = %+v", cuts)
	}
	if cuts[0].From != 0 || cuts[0].To != 60 {
		t.Errorf("first cut = %+v", cuts[0])
	}
	if cuts[1].From != 60 || cuts[1].To != 2880 || cuts[1].Rate != 10 {
		t.Errorf("second cut = %+v", cuts[1])
	}
	if cuts[2].From != 2880 || cuts[2].To != 3000 {
		t.Errorf("last cut = %+v", cuts[2])
	}
	// The pieces are continuous. A gap is footage nobody sees and an overlap is
	// footage seen twice, and neither would look like a fault in the file.
	for i := 1; i < len(cuts); i++ {
		if cuts[i].From != cuts[i-1].To {
			t.Errorf("cut %d starts at %.0f and cut %d ended at %.0f",
				i+1, cuts[i].From, i, cuts[i-1].To)
		}
	}
}

func TestCutsRefuseWhatWouldSilentlyLoseFootage(t *testing.T) {
	for _, c := range []struct {
		why      string
		segments []Segment
		length   float64
	}{
		{"a rate of zero", []Segment{{Rate: 0}}, 600},
		{"a negative rate", []Segment{{Rate: -2}}, 600},
		{"a middle segment with no until", []Segment{{Rate: 1}, {Rate: 10}}, 600},
		{"an until on the last segment", []Segment{{Until: "1:00", Rate: 1}, {Until: "9:00", Rate: 10}}, 600},
		{"a boundary that goes backwards", []Segment{{Until: "5:00", Rate: 1}, {Until: "1:00", Rate: 10}, {Rate: 1}}, 600},
	} {
		if _, err := Cuts(c.segments, c.length); err == nil {
			t.Errorf("%s was accepted", c.why)
		}
	}
}

// A boundary past the end of the clip is refused by the guard for boundaries
// that go backwards, because the segment after it then starts later than it
// ends. The dedicated guard survives for its sentence, and the sentence is the
// whole of its value: "segment 1 ends at 1200s and the clip is 600s long" tells
// somebody what to change, and "segment 2 ends at 600s, which is not after
// 1200s" tells them to work it out.
//
// So this case asserts the words, not the refusal. Without the guard the words
// change and nobody is any wiser from the file.
func TestCutsSayWhichSegmentRanPastTheEnd(t *testing.T) {
	_, err := Cuts([]Segment{{Until: "20:00", Rate: 1}, {Rate: 10}}, 600)
	if err == nil {
		t.Fatal("a segment past the end of the clip was accepted")
	}
	for _, want := range []string{"segment 1", "the clip is"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}
