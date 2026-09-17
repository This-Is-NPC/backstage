package presentation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

type segmentKeyV1 struct {
	V       string            `json:"v"`
	Kind    string            `json:"kind"`
	W       int               `json:"w"`
	H       int               `json:"h"`
	FPS     int               `json:"fps"`
	Scale   string            `json:"scale"`
	First   int               `json:"first"`
	End     int               `json:"end"`
	Encode  []string          `json:"encode"`
	FFmpeg  string            `json:"ffmpeg"`
	Browser string            `json:"browser"`
	Web     map[string]string `json:"web"`
	Event   eventKeyV1        `json:"event"`
	Prev    *eventKeyV1       `json:"prev,omitempty"`
	Overlap *overlapKeyV1     `json:"overlap,omitempty"`
}

type eventKeyV1 struct {
	At         string            `json:"at"`
	Layout     string            `json:"layout"`
	Slots      map[string]string `json:"slots"`
	Scene      string            `json:"scene"`
	Transition transitionKeyV1   `json:"transition"`
	Parameters map[string]any    `json:"parameters"`
	Narration  any               `json:"narration"`
	Text       []textKeyV1       `json:"text"`
	Entry      string            `json:"entry"`
	Tracks     []segmentTrackV1  `json:"tracks"`
}

type transitionKeyV1 struct {
	Effect   string `json:"effect"`
	Duration string `json:"duration"`
}

type overlapKeyV1 struct {
	Effect       string `json:"effect"`
	Duration     string `json:"duration"`
	ProgressFrom string `json:"progress-from"`
	ProgressTo   string `json:"progress-to"`
}

type textKeyV1 struct {
	At   string `json:"at"`
	End  string `json:"end"`
	Text string `json:"text"`
	Slot string `json:"slot"`
}

type segmentTrackV1 struct {
	Key      string `json:"key"`
	From     int    `json:"from"`
	To       int    `json:"to"`
	OnEnd    string `json:"on-end"`
	Duration string `json:"duration"`
}

type segmentCache struct {
	cache   *renderCache
	plan    *Plan
	project *scene.Project
	browser string
	web     map[string]string
}

type chunkJob struct {
	chunkRange
	key string
}

func newSegmentCache(cache *renderCache, p *Plan, browser string) *segmentCache {
	return &segmentCache{
		cache:   cache,
		plan:    p,
		project: p.Project,
		browser: browser,
		web:     embeddedWebHashes(),
	}
}

func (s *segmentCache) dest(key string) string {
	return filepath.Join(s.cache.root, "segments", key+".mp4")
}

func embeddedWebHashes() map[string]string {
	out := map[string]string{}
	for _, name := range []string{"runtime.html", "template.html", "visual.html"} {
		b, err := web.ReadFile("web/" + name)
		if err != nil {
			panic(err)
		}
		sum := sha256.Sum256(b)
		out[name] = hex.EncodeToString(sum[:])
	}
	return out
}

func x264CodecArgs() []string {
	return []string{"-an", "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p"}
}

func x264EncodeTail(p *Plan, encodeThreads int, out string) []string {
	return append(x264CodecArgs(), "-threads", strconv.Itoa(encodeThreads), "-video_track_timescale", strconv.Itoa(p.FPS), out)
}

func x264BaseArgs(p *Plan, opts renderOpts) []string {
	args := []string{"-v", "error", "-y", "-f", "image2pipe", "-framerate", strconv.Itoa(p.FPS), "-i", "-"}
	if opts.Scale != 1 {
		args = append(args, "-vf", draftCropFilter())
	}
	return append(args, x264CodecArgs()...)
}

func segmentEncodeArgs(p *Plan, opts renderOpts) []string {
	return append(x264BaseArgs(p, opts), "-video_track_timescale", strconv.Itoa(p.FPS))
}

func encoderArgs(p *Plan, opts renderOpts, encodeThreads int, out string) []string {
	return append(x264BaseArgs(p, opts), "-threads", strconv.Itoa(encodeThreads), "-video_track_timescale", strconv.Itoa(p.FPS), out)
}

func textSpanKeys(spans []TextSpan, first, end, fps int) []textKeyV1 {
	chunkStart := float64(first) / float64(fps)
	chunkEnd := float64(end) / float64(fps)
	var out []textKeyV1
	for _, s := range spans {
		if s.At < chunkEnd && s.End > chunkStart {
			out = append(out, textKeyV1{At: num(s.At), End: num(s.End), Text: s.Text, Slot: s.Slot})
		}
	}
	return out
}

func overlapProgress(e Event, first, end, fps int) (from, to string) {
	dur := e.Transition.Duration
	if dur <= 0 {
		return num(0), num(0)
	}
	var pf, pt float64
	found := false
	limit := e.At + dur
	for n := first; n < end; n++ {
		t := float64(n) / float64(fps)
		if t >= limit {
			break
		}
		p := (t - e.At) / dur
		if p < 0 {
			p = 0
		}
		if !found {
			pf = p
			found = true
		}
		pt = p
	}
	return num(pf), num(pt)
}

func chunkEvents(p *Plan, ch chunkRange) []int {
	ids := []int{ch.Event}
	if chunkTransition(p, ch) {
		ids = append(ids, ch.Event-1)
	}
	return ids
}

func (s *segmentCache) eventEntryHash(e Event) (string, error) {
	var path string
	switch {
	case e.Scene != "":
		sc := s.plan.Scenes[e.Scene]
		if sc == nil {
			return "", fmt.Errorf("unknown visual scene %q", e.Scene)
		}
		abs, err := s.plan.Project.InputPath(sc.Entry)
		if err != nil {
			return "", err
		}
		path = abs
	case s.plan.Template != "":
		path = s.plan.Template
	default:
		return "", nil
	}
	return s.cache.hashFile(path)
}

func (s *segmentCache) slottedTrackKeys(e Event, first, end int) ([]segmentTrackV1, error) {
	var out []segmentTrackV1
	seen := map[string]bool{}
	for _, slot := range sortedKeys(e.Slots) {
		id := e.Slots[slot]
		if seen[id] {
			continue
		}
		seen[id] = true
		tr, ok := s.plan.Tracks[id]
		if !ok || !trackNeeded(tr, first, end, s.plan.FPS) {
			continue
		}
		src, err := s.cache.hashFile(tr.Media.Path)
		if err != nil {
			return nil, err
		}
		out = append(out, segmentTrackV1{
			Key:      trackCacheKey(src, tr.Segments, s.plan.FPS, ffv1GOP, s.cache.ffmpeg),
			From:     trackFrame(tr, first, s.plan.FPS),
			To:       trackFrame(tr, end-1, s.plan.FPS),
			OnEnd:    tr.OnEnd,
			Duration: num(tr.Duration),
		})
	}
	return out, nil
}

func (s *segmentCache) eventKey(e Event, first, end int) (eventKeyV1, error) {
	entry, err := s.eventEntryHash(e)
	if err != nil {
		return eventKeyV1{}, err
	}
	tracks, err := s.slottedTrackKeys(e, first, end)
	if err != nil {
		return eventKeyV1{}, err
	}
	var narration any
	if e.Scene != "" {
		if sc := s.plan.Scenes[e.Scene]; sc != nil {
			narration = sc.Narration
		}
	}
	return eventKeyV1{
		At:         num(e.At),
		Layout:     e.Layout,
		Slots:      e.Slots,
		Scene:      e.Scene,
		Transition: transitionKeyV1{Effect: e.Transition.Effect, Duration: num(e.Transition.Duration)},
		Parameters: s.plan.Document.Parameters,
		Narration:  narration,
		Text:       textSpanKeys(s.plan.Text, first, end, s.plan.FPS),
		Entry:      entry,
		Tracks:     tracks,
	}, nil
}

func (s *segmentCache) cacheKey(opts renderOpts, ch chunkRange) (string, error) {
	p := s.plan
	e := p.Document.Timeline[ch.Event]
	ev, err := s.eventKey(e, ch.First, ch.End)
	if err != nil {
		return "", err
	}
	key := segmentKeyV1{
		V:       cacheKeyVersion,
		Kind:    "segment",
		W:       p.Width,
		H:       p.Height,
		FPS:     p.FPS,
		Scale:   num(opts.Scale),
		First:   ch.First,
		End:     ch.End,
		Encode:  segmentEncodeArgs(p, opts),
		FFmpeg:  s.cache.ffmpeg,
		Browser: s.browser,
		Web:     s.web,
		Event:   ev,
	}
	if chunkTransition(p, ch) {
		prev, err := s.eventKey(p.Document.Timeline[ch.Event-1], ch.First, ch.End)
		if err != nil {
			return "", err
		}
		from, to := overlapProgress(e, ch.First, ch.End, p.FPS)
		key.Prev = &prev
		key.Overlap = &overlapKeyV1{
			Effect:       e.Transition.Effect,
			Duration:     num(e.Transition.Duration),
			ProgressFrom: from,
			ProgressTo:   to,
		}
	}
	return hashJSON(key), nil
}

func (s *segmentCache) hashFiles(files map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for rel, path := range files {
		if path == "" {
			out[rel] = ""
			continue
		}
		sum, err := s.cache.hashFile(path)
		if err != nil {
			return nil, err
		}
		out[rel] = sum
	}
	return out, nil
}

func (s *segmentCache) entryValid(dest string) bool {
	m, size, ok := readEntryMeta(dest)
	if !ok || m.Size != size || m.Files == nil {
		return false
	}
	for rel, want := range *m.Files {
		abs, err := resolveWorkspaceAsset(s.project, rel)
		if want == "" {
			if err == nil {
				info, e := os.Stat(abs)
				if e == nil && info.Mode().IsRegular() {
					return false
				}
			}
			continue
		}
		if err != nil {
			return false
		}
		got, err := s.cache.hashFile(abs)
		if err != nil || got != want {
			return false
		}
	}
	return true
}

func (s *segmentCache) manifestAbs(dest string) map[string]string {
	out := map[string]string{}
	m, _, ok := readEntryMeta(dest)
	if !ok || m.Files == nil {
		return out
	}
	for rel, sum := range *m.Files {
		if sum == "" {
			out[rel] = ""
			continue
		}
		abs, err := resolveWorkspaceAsset(s.project, rel)
		if err != nil {
			continue
		}
		out[rel] = abs
	}
	return out
}

func recordSegmentHit(progress io.Writer, cache *renderCache, job chunkJob, abs map[string]string, results []chunkResult) {
	cache.note(true, "segments")
	fmt.Fprintf(progress, ">> chunk-%d cached\n", job.Index)
	results[job.Index] = chunkResult{index: job.Index, files: abs}
}
