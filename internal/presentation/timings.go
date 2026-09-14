package presentation

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"
)

const (
	histBuckets  = 1000
	histOverflow = 1000
)

// framePhase is one composed-frame stage: total, mean, max, p95, and count.
type framePhase struct {
	Seconds     float64 `json:"seconds"`
	MeanSeconds float64 `json:"mean-seconds"`
	MaxSeconds  float64 `json:"max-seconds"`
	P95Seconds  float64 `json:"p95-seconds"`
	Frames      int     `json:"frames"`
}

type namedSeconds struct {
	ID      string  `json:"id"`
	Seconds float64 `json:"seconds"`
}

type namedBytes struct {
	ID    string `json:"id"`
	Bytes int64  `json:"bytes"`
}

// renderTimings is the presentation sidecar object. It is not facts.Timings.
type renderTimings struct {
	RendererStartSeconds float64            `json:"renderer-start-seconds"`
	PrepareTrackSeconds  map[string]float64 `json:"prepare-track-seconds,omitempty"`
	AudioSeconds         *float64           `json:"audio-seconds,omitempty"`
	AudioPartSeconds     []namedSeconds     `json:"audio-part-seconds,omitempty"`
	Decode               framePhase         `json:"decode"`
	Transfer             framePhase         `json:"transfer"`
	Draw                 framePhase         `json:"draw"`
	Screenshot           framePhase         `json:"screenshot"`
	EncodeWrite          framePhase         `json:"encode-write"`
	EncodeSeconds        float64            `json:"encode-seconds"`
	MuxSeconds           *float64           `json:"mux-seconds,omitempty"`
	MetadataSeconds      float64            `json:"metadata-seconds"`
	TotalSeconds         float64            `json:"total-seconds"`
	TrackBytes           map[string]int64   `json:"track-bytes,omitempty"`
	AudioPartBytes       []namedBytes       `json:"audio-part-bytes,omitempty"`
	MixWAVBytes          *int64             `json:"mix-wav-bytes,omitempty"`
	VideoMP4Bytes        *int64             `json:"video-mp4-bytes,omitempty"`
	FinalMP4Bytes        *int64             `json:"final-mp4-bytes,omitempty"`
	DecodedPNGBytes      int64              `json:"decoded-png-bytes"`
	ScreenshotBytes      int64              `json:"screenshot-bytes"`
}

// msHist is 1000 one-millisecond buckets plus overflow. It does not store a duration per frame.
type msHist struct {
	counts [histBuckets + 1]uint64
	sum    time.Duration
	max    time.Duration
	n      uint64
}

func roundSec(d time.Duration) float64 {
	return math.Round(d.Seconds()*1000) / 1000
}

func (h *msHist) add(d time.Duration) {
	if d < 0 {
		d = 0
	}
	h.sum += d
	h.n++
	if d > h.max {
		h.max = d
	}
	ms := int(d / time.Millisecond)
	if ms >= histBuckets {
		h.counts[histOverflow]++
		return
	}
	h.counts[ms]++
}

func (h *msHist) p95() time.Duration {
	if h.n == 0 {
		return 0
	}
	need := uint64(math.Ceil(0.95 * float64(h.n)))
	if need == 0 {
		need = 1
	}
	var acc uint64
	for i := 0; i < histBuckets; i++ {
		acc += h.counts[i]
		if acc >= need {
			return time.Duration(i+1) * time.Millisecond
		}
	}
	return h.max
}

func (h *msHist) snapshot() framePhase {
	if h.n == 0 {
		return framePhase{}
	}
	mean := h.sum.Seconds() / float64(h.n)
	return framePhase{
		Seconds:     roundSec(h.sum),
		MeanSeconds: math.Round(mean*1000) / 1000,
		MaxSeconds:  roundSec(h.max),
		P95Seconds:  roundSec(h.p95()),
		Frames:      int(h.n),
	}
}

func statSize(path string) *int64 {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	n := info.Size()
	return &n
}

func collectWorkBytes(timings *renderTimings, work string, p *Plan, trackPaths map[string]string) {
	if len(trackPaths) > 0 {
		sizes := map[string]int64{}
		for id, path := range trackPaths {
			if n := statSize(path); n != nil {
				sizes[id] = *n
			}
		}
		if len(sizes) > 0 {
			timings.TrackBytes = sizes
		}
	}
	var parts []namedBytes
	for i, a := range p.AudioParts {
		if n := statSize(filepath.Join(work, fmt.Sprintf("audio-%d.wav", i))); n != nil {
			parts = append(parts, namedBytes{ID: a.ID, Bytes: *n})
		}
	}
	if len(parts) > 0 {
		timings.AudioPartBytes = parts
	}
	timings.MixWAVBytes = statSize(filepath.Join(work, "mix.wav"))
	timings.VideoMP4Bytes = statSize(filepath.Join(work, "video.mp4"))
	timings.FinalMP4Bytes = statSize(filepath.Join(work, "final.mp4"))
}

func (t renderTimings) progressLine(frames int) string {
	audio, mux, prep := 0.0, 0.0, 0.0
	if t.AudioSeconds != nil {
		audio = *t.AudioSeconds
	}
	if t.MuxSeconds != nil {
		mux = *t.MuxSeconds
	}
	for _, s := range t.PrepareTrackSeconds {
		prep += s
	}
	return fmt.Sprintf(">> render timings: total=%.3f renderer-start=%.3f prepare=%.3f audio=%.3f encode=%.3f mux=%.3f frames=%d decode-p95=%.3f transfer-p95=%.3f draw-p95=%.3f screenshot-p95=%.3f encode-write-p95=%.3f\n",
		t.TotalSeconds, t.RendererStartSeconds, prep, audio, t.EncodeSeconds, mux, frames,
		t.Decode.P95Seconds, t.Transfer.P95Seconds, t.Draw.P95Seconds, t.Screenshot.P95Seconds, t.EncodeWrite.P95Seconds)
}
