package presentation

import (
	"context"
	"encoding/binary"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestMusicDucksOnlyDuringNarration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dir := t.TempDir()
	makeTone := func(name, filter string) string {
		t.Helper()
		path := filepath.Join(dir, name+".wav")
		if err := run(ctx, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", filter, path); err != nil {
			t.Fatal(err)
		}
		return path
	}
	music := makeTone("music", "sine=frequency=220:duration=3:sample_rate=48000")
	voice := makeTone("voice", "sine=frequency=2200:duration=1:sample_rate=48000")
	p := &Plan{Document: Document{Duration: 3}, AudioParts: []AudioPart{{ID: "music", Path: music, To: 3, Duration: 3, Rate: 1, Volume: 1, Duck: true}, {ID: "voice", Path: voice, At: 1, To: 1, Duration: 1, Rate: 1, Volume: 2, Narration: true}}}
	mixed, _, err := p.mixDirect(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", mixed, "-af", "bandpass=f=220:w=20", "-ac", "1", "-f", "f32le", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	rms := func(from, to float64) float64 {
		a, b := int(from*48000), int(to*48000)
		sum := 0.0
		for i := a; i < b; i++ {
			v := float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[i*4:])))
			sum += v * v
		}
		return math.Sqrt(sum / float64(b-a))
	}
	before, during, after := rms(0.5, 0.8), rms(1.5, 1.8), rms(2.7, 2.9)
	if during > before*0.5 || after < before*0.8 {
		t.Fatalf("music RMS before=%v during=%v after=%v", before, during, after)
	}
	info, err := probe(mixed)
	if err != nil || math.Abs(info.Duration-3) > 0.001 {
		t.Fatalf("mix duration=%v err=%v", info.Duration, err)
	}
	second, _, err := p.mixDirect(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", mixed, "-f", "md5", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	b, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", second, "-f", "md5", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("ducked mix is not reproducible")
	}
}
