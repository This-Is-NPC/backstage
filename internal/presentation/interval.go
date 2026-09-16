package presentation

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const mixSampleRate = 48000

type PreviewOpts struct {
	From, To, Scale float64
}

type renderOpts struct {
	First, End int
	Scale      float64
}

func intervalFrames(from, to float64, fps int) (first, end int) {
	first = int(math.Ceil(from*float64(fps) - 1e-9))
	end = int(math.Ceil(to*float64(fps) - 1e-9))
	return first, end
}

func finiteNumber(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func validatePreviewOpts(p *Plan, o PreviewOpts) error {
	if !finiteNumber(o.From) || o.From < 0 {
		return fmt.Errorf("from must be a finite number >= 0")
	}
	if !finiteNumber(o.To) || o.To > p.Document.Duration {
		return fmt.Errorf("to must be a finite number at most the presentation duration")
	}
	if o.From >= o.To {
		return fmt.Errorf("from must be less than to")
	}
	if !finiteNumber(o.Scale) || o.Scale <= 0 || o.Scale > 1 {
		return fmt.Errorf("scale must be a finite number in (0, 1]")
	}
	first, end := intervalFrames(o.From, o.To, p.FPS)
	if first >= end {
		return fmt.Errorf("interval has no frames")
	}
	return nil
}

func renderOptsFromPreview(p *Plan, o PreviewOpts) (renderOpts, error) {
	if err := validatePreviewOpts(p, o); err != nil {
		return renderOpts{}, err
	}
	first, end := intervalFrames(o.From, o.To, p.FPS)
	return renderOpts{First: first, End: end, Scale: o.Scale}, nil
}

func fullRenderOpts(p *Plan) renderOpts {
	return renderOpts{First: 0, End: p.Frames, Scale: 1}
}

func decoderSeekTime(start, fps int) string {
	if start <= 0 || fps < 1 {
		return ""
	}
	t := (float64(start) - 0.5) / float64(fps)
	if t < 0 {
		t = 0
	}
	return num(t)
}

var observeCountPackets func(path string)

func countPackets(ctx context.Context, path string) (int, error) {
	if observeCountPackets != nil {
		observeCountPackets(path)
	}
	b, err := command(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0", "-count_packets", "-show_entries", "stream=nb_read_packets", "-of", "csv=p=0", path).Output()
	if err != nil {
		return 0, fmt.Errorf("count packets %s: %w", path, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid packet count %q for %s", strings.TrimSpace(string(b)), path)
	}
	return n, nil
}

func trackVisible(tr CompiledTrack, n, fps int) bool {
	local := float64(n)/float64(fps) - tr.Start
	return local >= 0 && !(local >= tr.Duration && tr.OnEnd == "hide")
}

func trackFrame(tr CompiledTrack, n, fps int) int {
	local := float64(n)/float64(fps) - tr.Start
	index := int(math.Floor(local*float64(fps) + 1e-8))
	if index < 0 {
		return 0
	}
	return index
}

func trackNeeded(tr CompiledTrack, first, end, fps int) bool {
	for n := first; n < end; n++ {
		if trackVisible(tr, n, fps) {
			return true
		}
	}
	return false
}

func atrimFilter(ss, es int64) string {
	return fmt.Sprintf("atrim=start_sample=%d:end_sample=%d", ss, es)
}

func muxAudioFilter(ss, es int64) string {
	return atrimFilter(ss, es) + ",asetpts=PTS-STARTPTS"
}

func audioSamples(first, end, fps int) (ss, es int64) {
	if fps < 1 {
		return 0, 0
	}
	if mixSampleRate%fps == 0 {
		return int64(first) * int64(mixSampleRate) / int64(fps), int64(end) * int64(mixSampleRate) / int64(fps)
	}
	return int64(math.Round(float64(first) * float64(mixSampleRate) / float64(fps))), int64(math.Round(float64(end) * float64(mixSampleRate) / float64(fps)))
}

func intervalDuration(first, end, fps int) float64 {
	return float64(end-first) / float64(fps)
}

func draftCropFilter() string {
	return "crop=trunc(iw/2)*2:trunc(ih/2)*2:0:0"
}
