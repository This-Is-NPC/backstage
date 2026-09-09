package presentation

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func audioFixture(t *testing.T) *Plan {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "voice.wav")
	if err := run(context.Background(), "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000:duration=2", path); err != nil {
		t.Fatal(err)
	}
	return &Plan{Project: &scene.Project{Dir: dir}, Document: Document{Duration: 5, Sources: map[string]Source{"source": {Scene: "recording"}}}, Inputs: map[string]string{}, Tracks: map[string]CompiledTrack{"video": {Track: Track{Source: "source", Start: 0, Segments: []Segment{{From: 0, To: 4, Rate: 2}}}}}, Scenes: map[string]*scene.Scene{"recording": {Narration: scene.Narration{Cues: []scene.NarrationCue{{ID: "cue", Start: 1, End: 3, Text: "test", Audio: "voice.wav"}}}}}}
}
func TestAudioPolicyIsPerUse(t *testing.T) {
	p := audioFixture(t)
	p.Document.Audio = []Audio{{ID: "follow", Track: "video", Cue: "cue", Mode: "follow-video"}, {ID: "normal", Scene: "recording", Cue: "cue", Mode: "independent", At: 2}}
	if err := p.compileAudio(); err != nil {
		t.Fatal(err)
	}
	a, b := p.AudioParts[0], p.AudioParts[1]
	if a.At != 0.5 || a.Rate != 2 || math.Abs(a.Duration-1) > 0.001 || b.At != 2 || b.Rate != 1 || math.Abs(b.Duration-2) > 0.001 {
		t.Fatalf("%+v %+v", a, b)
	}
	mixed, err := p.mix(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := probe(mixed)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(m.Duration-5) > 0.001 {
		t.Fatal(m.Duration)
	}
}
func TestIndependentAudioNeedsExplicitEndCut(t *testing.T) {
	p := audioFixture(t)
	p.Document.Audio = []Audio{{ID: "voice", Scene: "recording", Cue: "cue", At: 4}}
	if err := p.compileAudio(); err == nil {
		t.Fatal("silently truncated speech")
	}
	p.Document.Audio[0].TrimEnd = true
	if err := p.compileAudio(); err != nil {
		t.Fatal(err)
	}
	if p.AudioParts[0].Duration != 1 {
		t.Fatal(p.AudioParts)
	}
}
func TestRepeatedSegmentsRepeatSourceCaptions(t *testing.T) {
	p := audioFixture(t)
	tr := p.Tracks["video"]
	tr.Segments = append(tr.Segments, Segment{From: 1, To: 3, Rate: 1})
	p.Tracks["video"] = tr
	p.Document.Captions = []Caption{{Track: "video", Cue: "cue", Clock: "source", Slot: "subtitle"}}
	if err := p.compileCaptions(); err != nil {
		t.Fatal(err)
	}
	if len(p.Text) != 2 || p.Text[0].At != 0.5 || p.Text[1].At != 2 {
		t.Fatal(p.Text)
	}
}

func TestLoopedMusicHasBoundedDuration(t *testing.T) {
	p := audioFixture(t)
	p.Document.Duration = 1.5
	p.Document.Audio = []Audio{{ID: "music", File: "voice.wav", From: 0, To: 0.2, Loop: true, Volume: func() *float64 { v := 0.5; return &v }()}}
	if err := p.compileAudio(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path, err := p.mix(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 400000 {
		t.Fatalf("loop exceeded bounded PCM size: %d", info.Size())
	}
	m, err := probe(path)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(m.Duration-1.5) > 0.001 {
		t.Fatal(m.Duration)
	}
}

func TestSceneAudioAssetCanBeReusedIndependently(t *testing.T) {
	p := audioFixture(t)
	p.Scenes["recording"].Audio = map[string]scene.SceneAudio{"ambience": {File: "voice.wav"}}
	p.Document.Audio = []Audio{{ID: "scene-sound", Scene: "recording", Asset: "ambience", At: 1, Volume: func() *float64 { v := 0.2; return &v }()}}
	if err := p.compileAudio(); err != nil {
		t.Fatal(err)
	}
	if len(p.AudioParts) != 1 || p.AudioParts[0].At != 1 || p.AudioParts[0].Volume != 0.2 {
		t.Fatal(p.AudioParts)
	}
}
