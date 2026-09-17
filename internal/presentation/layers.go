package presentation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const padTransparent = "black@0"

type probeCaption struct {
	At   float64 `json:"at"`
	End  float64 `json:"end"`
	Slot string  `json:"slot"`
	Text string  `json:"text"`
}

type probeGeo struct {
	Track        string  `json:"track"`
	Slot         string  `json:"slot"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
	W            float64 `json:"w"`
	H            float64 `json:"h"`
	RTL          float64 `json:"rtl"`
	RTR          float64 `json:"rtr"`
	RBR          float64 `json:"rbr"`
	RBL          float64 `json:"rbl"`
	RadiusPx     bool    `json:"radiusPx"`
	BL           float64 `json:"bl"`
	BT           float64 `json:"bt"`
	BR           float64 `json:"br"`
	BB           float64 `json:"bb"`
	Fit          string  `json:"fit"`
	BorderLeft   string  `json:"borderLeft"`
	BorderRight  string  `json:"borderRight"`
	BorderTop    string  `json:"borderTop"`
	BorderBottom string  `json:"borderBottom"`
	Background   string  `json:"background"`
	Shadow       string  `json:"shadow"`
}

type probeFrame struct {
	Event            int            `json:"event"`
	Geometry         []probeGeo     `json:"geometry"`
	AnimationsActive bool           `json:"animationsActive"`
	DynamicElements  []string       `json:"dynamicElements"`
	SMIL             bool           `json:"smil"`
	Canvas           bool           `json:"canvas"`
	Video            bool           `json:"video"`
	DOMHash          string         `json:"domHash"`
	Captions         []probeCaption `json:"captions"`
}

type slotBox struct {
	Track, Slot, Fit string
	X, Y, W, H       int
	CX, CY, CW, CH   int
	InnerRadius      float64
}

type layerDecision struct {
	Layered bool
	Reason  string
}

func currentLayerMode() string {
	observeMu.Lock()
	m := layerMode
	observeMu.Unlock()
	return m
}

func skipLayerProbe(p *Plan, opts renderOpts, ch chunkRange) bool {
	return chunkTransition(p, ch) || opts.Scale != 1
}

func snapEdges(x, span float64) (origin, size int) {
	x0 := int(math.Floor(x + 0.5))
	x1 := int(math.Floor(x + span + 0.5))
	return x0, x1 - x0
}

func snapSlot(g probeGeo) slotBox {
	x, w := snapEdges(g.X, g.W)
	y, h := snapEdges(g.Y, g.H)
	cx, cw := snapEdges(g.X+g.BL, g.W-g.BL-g.BR)
	cy, ch := snapEdges(g.Y+g.BT, g.H-g.BT-g.BB)
	fit := g.Fit
	if fit == "" {
		fit = "contain"
	}
	inner := g.RTL - g.BL
	if inner < 0 {
		inner = 0
	}
	return slotBox{
		Track: g.Track, Slot: g.Slot, Fit: fit,
		X: x, Y: y, W: w, H: h,
		CX: cx, CY: cy, CW: cw, CH: ch,
		InnerRadius: inner,
	}
}

func nearFloat(a, b float64) bool {
	return math.Abs(a-b) < 1e-3
}

func geoEqual(a, b probeGeo) bool {
	return a.Track == b.Track && a.Slot == b.Slot && a.Fit == b.Fit &&
		a.BorderLeft == b.BorderLeft && a.BorderRight == b.BorderRight &&
		a.BorderTop == b.BorderTop && a.BorderBottom == b.BorderBottom &&
		a.Background == b.Background && a.Shadow == b.Shadow &&
		a.RadiusPx == b.RadiusPx &&
		nearFloat(a.X, b.X) && nearFloat(a.Y, b.Y) && nearFloat(a.W, b.W) && nearFloat(a.H, b.H) &&
		nearFloat(a.RTL, b.RTL) && nearFloat(a.RTR, b.RTR) &&
		nearFloat(a.RBR, b.RBR) && nearFloat(a.RBL, b.RBL) &&
		nearFloat(a.BL, b.BL) && nearFloat(a.BT, b.BT) &&
		nearFloat(a.BR, b.BR) && nearFloat(a.BB, b.BB)
}

func geoSymmetric(g probeGeo) bool {
	return nearFloat(g.BL, g.BR) && nearFloat(g.BL, g.BT) && nearFloat(g.BL, g.BB) &&
		nearFloat(g.RTL, g.RTR) && nearFloat(g.RTL, g.RBR) && nearFloat(g.RTL, g.RBL)
}

func layeredFit(fit string) bool {
	switch fit {
	case "", "contain", "cover", "fill":
		return true
	}
	return false
}

func captionKey(cs []probeCaption) string {
	type row struct {
		At, End    float64
		Slot, Text string
	}
	rows := make([]row, len(cs))
	for i, c := range cs {
		rows[i] = row(c)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].At != rows[j].At {
			return rows[i].At < rows[j].At
		}
		if rows[i].Slot != rows[j].Slot {
			return rows[i].Slot < rows[j].Slot
		}
		return rows[i].Text < rows[j].Text
	})
	b, _ := json.Marshal(rows)
	return string(b)
}

func visibleCaptions(text []TextSpan, t float64) []probeCaption {
	var out []probeCaption
	for _, c := range text {
		if t >= c.At && t < c.End {
			out = append(out, probeCaption{At: c.At, End: c.End, Slot: c.Slot, Text: c.Text})
		}
	}
	return out
}

func chunkCaptionsStable(p *Plan, ch chunkRange) (string, bool) {
	key := captionKey(visibleCaptions(p.Text, float64(ch.First)/float64(p.FPS)))
	for n := ch.First + 1; n < ch.End; n++ {
		if captionKey(visibleCaptions(p.Text, float64(n)/float64(p.FPS))) != key {
			return key, false
		}
	}
	return key, true
}

func cloneGeo(g []probeGeo) []probeGeo {
	out := make([]probeGeo, len(g))
	copy(out, g)
	return out
}

func geosEqual(a, b []probeGeo) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !geoEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func tracksVisibleAll(p *Plan, ch chunkRange) layerDecision {
	ev := p.Document.Timeline[ch.Event]
	for n := ch.First; n < ch.End; n++ {
		for _, id := range ev.Slots {
			tr, ok := p.Tracks[id]
			if !ok {
				return layerDecision{Reason: "missing track " + id}
			}
			if !trackVisible(tr, n, p.FPS) {
				return layerDecision{Reason: "track not visible"}
			}
		}
	}
	return layerDecision{Layered: true}
}

func geometryEligible(g probeGeo) layerDecision {
	if !layeredFit(g.Fit) {
		return layerDecision{Reason: "fit"}
	}
	if !g.RadiusPx {
		return layerDecision{Reason: "radius"}
	}
	if !geoSymmetric(g) {
		return layerDecision{Reason: "border"}
	}
	box := snapSlot(g)
	if box.CW < 1 || box.CH < 1 || box.W < 1 || box.H < 1 {
		return layerDecision{Reason: "empty"}
	}
	return layerDecision{Layered: true}
}

func chunkEligible(p *Plan, ch chunkRange, probes []probeFrame) layerDecision {
	if len(probes) != ch.End-ch.First {
		return layerDecision{Reason: "probe count"}
	}
	if d := tracksVisibleAll(p, ch); !d.Layered {
		return d
	}
	ev := p.Document.Timeline[ch.Event]
	first := probes[0]
	if first.DOMHash == "" {
		return layerDecision{Reason: "dom hash"}
	}
	caps := captionKey(first.Captions)
	if len(first.Geometry) != len(ev.Slots) {
		return layerDecision{Reason: "geometry count"}
	}
	for _, pr := range probes {
		if pr.DOMHash != first.DOMHash {
			return layerDecision{Reason: "dom hash"}
		}
		if pr.Event != ch.Event {
			return layerDecision{Reason: "event"}
		}
		if pr.AnimationsActive {
			return layerDecision{Reason: "css animation"}
		}
		if pr.SMIL {
			return layerDecision{Reason: "smil"}
		}
		if pr.Canvas {
			return layerDecision{Reason: "canvas"}
		}
		if pr.Video {
			return layerDecision{Reason: "video"}
		}
		if len(pr.DynamicElements) > 0 {
			return layerDecision{Reason: "dynamic image"}
		}
		if captionKey(pr.Captions) != caps {
			return layerDecision{Reason: "captions"}
		}
		if len(pr.Geometry) != len(first.Geometry) {
			return layerDecision{Reason: "geometry count"}
		}
		for i, g := range pr.Geometry {
			if d := geometryEligible(g); !d.Layered {
				return d
			}
			if !geoEqual(g, first.Geometry[i]) {
				return layerDecision{Reason: "geometry"}
			}
		}
	}
	return layerDecision{Layered: true}
}

func staticChunkEligible(p *Plan, ch chunkRange, first, last probeFrame) (layerDecision, string) {
	if d := tracksVisibleAll(p, ch); !d.Layered {
		return d, ""
	}
	caps, ok := chunkCaptionsStable(p, ch)
	if !ok {
		return layerDecision{Reason: "captions"}, ""
	}
	ev := p.Document.Timeline[ch.Event]
	if first.Event != ch.Event || last.Event != ch.Event {
		return layerDecision{Reason: "event"}, caps
	}
	if len(first.Geometry) != len(ev.Slots) {
		return layerDecision{Reason: "geometry count"}, caps
	}
	for _, g := range first.Geometry {
		if d := geometryEligible(g); !d.Layered {
			return d, caps
		}
	}
	return layerDecision{Layered: true}, caps
}

func staticGuard(first, last probeFrame) (ok bool, reason string) {
	if first.AnimationsActive || last.AnimationsActive {
		return false, "css animation"
	}
	if first.SMIL || last.SMIL {
		return false, "smil"
	}
	if !geosEqual(first.Geometry, last.Geometry) {
		return false, "geometry"
	}
	return true, ""
}

func staticSubject(e Event) string {
	if e.Layout != "" {
		return "template " + e.Layout
	}
	return "scene " + e.Scene
}

func containPad(srcW, srcH, cw, ch int) (w, h, x, y int) {
	if srcW < 1 || srcH < 1 || cw < 1 || ch < 1 {
		return cw, ch, 0, 0
	}
	s := math.Min(float64(cw)/float64(srcW), float64(ch)/float64(srcH))
	nw, nh := float64(srcW)*s, float64(srcH)*s
	w = min(cw, int(math.Ceil(nw-1e-9)))
	h = min(ch, int(math.Ceil(nh-1e-9)))
	x = int(math.Round((float64(cw) - nw) / 2))
	y = int(math.Round((float64(ch) - nh) / 2))
	x = max(0, min(x, cw-w))
	y = max(0, min(y, ch-h))
	return w, h, x, y
}

func coverPad(srcW, srcH, cw, ch int) (w, h, x, y int) {
	if srcW < 1 || srcH < 1 || cw < 1 || ch < 1 {
		return cw, ch, 0, 0
	}
	s := math.Max(float64(cw)/float64(srcW), float64(ch)/float64(srcH))
	w = int(math.Round(float64(srcW) * s))
	h = int(math.Round(float64(srcH) * s))
	if w < cw {
		w = cw
	}
	if h < ch {
		h = ch
	}
	x = (w - cw) / 2
	y = (h - ch) / 2
	return w, h, x, y
}

func scaleFilter(destW, destH int) string {
	if destW < 1 {
		destW = 1
	}
	if destH < 1 {
		destH = 1
	}
	return fmt.Sprintf("scale=%d:%d:flags=area", destW, destH)
}

func imageRect(box slotBox, srcW, srcH int) (x, y, w, h int) {
	switch box.Fit {
	case "cover":
		return 0, 0, box.CW, box.CH
	case "fill":
		return 0, 0, box.CW, box.CH
	default:
		w, h, x, y = containPad(srcW, srcH, box.CW, box.CH)
		return x, y, w, h
	}
}

func slotScaleFilter(box slotBox, srcW, srcH int) string {
	switch box.Fit {
	case "cover":
		tw, th, cx, cy := coverPad(srcW, srcH, box.CW, box.CH)
		return fmt.Sprintf("%s,crop=%d:%d:%d:%d,format=rgba", scaleFilter(tw, th), box.CW, box.CH, cx, cy)
	case "fill":
		return scaleFilter(box.CW, box.CH) + ",format=rgba"
	default:
		w, h, x, y := containPad(srcW, srcH, box.CW, box.CH)
		return fmt.Sprintf("%s,pad=%d:%d:%d:%d:color=%s,format=rgba", scaleFilter(w, h), box.CW, box.CH, x, y, padTransparent)
	}
}

func contentMask(cw, ch int, radius float64, imgX, imgY, imgW, imgH int) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, cw, ch))
	if cw < 1 || ch < 1 {
		return img
	}
	if radius < 0 {
		radius = 0
	}
	maxR := math.Min(float64(cw), float64(ch)) / 2
	if radius > maxR {
		radius = maxR
	}
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			rr := roundRectCoverage(float64(x), float64(y), float64(cw), float64(ch), radius)
			ir := axisRectCoverage(float64(x), float64(y), imgX, imgY, imgW, imgH)
			img.SetGray(x, y, color.Gray{Y: uint8(math.Round(rr * ir * 255))})
		}
	}
	return img
}

func roundRectCoverage(px, py, w, h, r float64) float64 {
	if r <= 0 {
		if px >= 0 && py >= 0 && px < w && py < h {
			return 1
		}
		return 0
	}
	const steps = 8
	delta := 1.0 / float64(steps)
	var n float64
	for iy := 0; iy < steps; iy++ {
		for ix := 0; ix < steps; ix++ {
			if insideRoundRect(px+(float64(ix)+0.5)*delta, py+(float64(iy)+0.5)*delta, w, h, r) {
				n++
			}
		}
	}
	return n / float64(steps*steps)
}

func insideRoundRect(x, y, w, h, r float64) bool {
	if x < 0 || y < 0 || x >= w || y >= h {
		return false
	}
	if x >= r && x < w-r || y >= r && y < h-r {
		return true
	}
	cx, cy := r, r
	switch {
	case x >= w-r && y < r:
		cx = w - r
	case x < r && y >= h-r:
		cy = h - r
	case x >= w-r && y >= h-r:
		cx, cy = w-r, h-r
	}
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

func axisRectCoverage(px, py float64, rx, ry, rw, rh int) float64 {
	if rw <= 0 || rh <= 0 {
		return 0
	}
	const steps = 8
	delta := 1.0 / float64(steps)
	var n float64
	x0, y0, x1, y1 := float64(rx), float64(ry), float64(rx+rw), float64(ry+rh)
	for iy := 0; iy < steps; iy++ {
		for ix := 0; ix < steps; ix++ {
			sx := px + (float64(ix)+0.5)*delta
			sy := py + (float64(iy)+0.5)*delta
			if sx >= x0 && sx < x1 && sy >= y0 && sy < y1 {
				n++
			}
		}
	}
	return n / float64(steps*steps)
}

func encodeGrayPNG(img *image.Gray) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type trackMeta struct {
	mu      sync.Mutex
	size    map[string][2]int
	packets map[string]int
}

func (m *trackMeta) sizeOf(ctx context.Context, path string) (w, h int, err error) {
	if m == nil {
		return ffprobeSize(ctx, path)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.size[path]; ok {
		return s[0], s[1], nil
	}
	w, h, err = ffprobeSize(ctx, path)
	if err != nil {
		return 0, 0, err
	}
	if m.size == nil {
		m.size = map[string][2]int{}
	}
	m.size[path] = [2]int{w, h}
	return w, h, nil
}

func (m *trackMeta) packetsOf(ctx context.Context, path string) (n int, err error) {
	if m == nil {
		return countPackets(ctx, path)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.packets[path]; ok {
		return p, nil
	}
	n, err = countPackets(ctx, path)
	if err != nil {
		return 0, err
	}
	if m.packets == nil {
		m.packets = map[string]int{}
	}
	m.packets[path] = n
	return n, nil
}

func ffprobeSize(ctx context.Context, path string) (w, h int, err error) {
	b, err := command(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=p=0", path).Output()
	if err != nil {
		return 0, 0, fmt.Errorf("size %s: %w", path, err)
	}
	parts := strings.Split(strings.TrimSpace(string(b)), ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("size %s: %s", path, b)
	}
	w, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	h, err = strconv.Atoi(parts[1])
	return w, h, err
}

func estimatedTrackFrames(tr CompiledTrack, fps int) int {
	n := int(math.Floor(tr.Duration*float64(fps) + 1e-8))
	if n < 1 {
		return 1
	}
	return n
}

func clampTrackTrim(start, last, nTrack int) (int, int) {
	if nTrack < 1 {
		nTrack = 1
	}
	if start < 0 {
		start = 0
	}
	if last < start {
		last = start
	}
	if start >= nTrack {
		start = nTrack - 1
	}
	if last >= nTrack {
		last = nTrack - 1
	}
	return start, last + 1
}

func resolveTrackTrim(ctx context.Context, path string, tr CompiledTrack, ch chunkRange, fps int, meta *trackMeta) (start, endEx int, err error) {
	nOut := ch.End - ch.First
	if nOut < 1 {
		return 0, 0, nil
	}
	start = trackFrame(tr, ch.First, fps)
	last := trackFrame(tr, ch.End-1, fps)
	nEst := estimatedTrackFrames(tr, fps)
	nTrack := nEst
	if start >= nEst || last >= nEst {
		n, e := meta.packetsOf(ctx, path)
		if e != nil {
			return 0, 0, e
		}
		nTrack = n
	}
	s, e := clampTrackTrim(start, last, nTrack)
	return s, e, nil
}

func layerGraph(boxes []slotBox, srcSizes [][2]int, haves []int, fps int, outLabel string) string {
	if fps < 1 {
		fps = 1
	}
	nTracks := len(boxes)
	var b strings.Builder
	fmt.Fprintf(&b, "[0:v]format=rgba[below];")
	cur := "[below]"
	for i, box := range boxes {
		have := haves[i]
		trimPad := fmt.Sprintf("[t%d]", i)
		fmt.Fprintf(&b, "[%d:v]trim=start_frame=0:end_frame=%d,setpts=N/%d/TB%s;", 1+i, have, fps, trimPad)
		scaled := fmt.Sprintf("[s%d]", i)
		fmt.Fprintf(&b, "%s%s%s;", trimPad, slotScaleFilter(box, srcSizes[i][0], srcSizes[i][1]), scaled)
		fmt.Fprintf(&b, "[%d:v]format=gray[g%d];", 1+nTracks+i, i)
		fmt.Fprintf(&b, "%s[g%d]alphamerge[m%d];", scaled, i, i)
		next := fmt.Sprintf("[o%d]", i)
		fmt.Fprintf(&b, "%s[m%d]overlay=%d:%d:eof_action=repeat:format=rgb,format=rgba%s;", cur, i, box.CX, box.CY, next)
		cur = next
	}
	fmt.Fprintf(&b, "[%d:v]format=rgba[above];%s[above]overlay=0:0:eof_action=repeat:format=rgb,format=rgba[%s]", 1+2*nTracks, cur, outLabel)
	return b.String()
}

func compositeChunk(ctx context.Context, p *Plan, ch chunkRange, trackPaths map[string]string, boxes []slotBox, below, above []byte, encodeThreads int, work, out string, meta *trackMeta, observe func(n int, png []byte) error) error {
	nOut := ch.End - ch.First
	dir := filepath.Join(work, fmt.Sprintf("layer-%d", ch.Index))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	write := func(name string, b []byte) error { return os.WriteFile(filepath.Join(dir, name), b, 0o644) }
	if err := write("below.png", below); err != nil {
		return err
	}
	if err := write("above.png", above); err != nil {
		return err
	}
	fr := strconv.Itoa(p.FPS)
	args := []string{"-v", "error", "-y", "-loop", "1", "-framerate", fr, "-i", filepath.Join(dir, "below.png")}
	sizes := make([][2]int, len(boxes))
	haves := make([]int, len(boxes))
	var maskFiles []string
	for i, box := range boxes {
		path := trackPaths[box.Track]
		tr, ok := p.Tracks[box.Track]
		if !ok {
			return fmt.Errorf("track %s missing", box.Track)
		}
		start, endEx, err := resolveTrackTrim(ctx, path, tr, ch, p.FPS, meta)
		if err != nil {
			return err
		}
		haves[i] = endEx - start
		if seek := decoderSeekTime(start, p.FPS); seek != "" {
			args = append(args, "-ss", seek)
		}
		args = append(args, "-i", path)
		w, h, err := meta.sizeOf(ctx, path)
		if err != nil {
			return err
		}
		sizes[i] = [2]int{w, h}
		ix, iy, iw, ih := imageRect(box, w, h)
		mask, err := encodeGrayPNG(contentMask(box.CW, box.CH, box.InnerRadius, ix, iy, iw, ih))
		if err != nil {
			return err
		}
		name := fmt.Sprintf("m%d.png", i)
		if err = write(name, mask); err != nil {
			return err
		}
		maskFiles = append(maskFiles, filepath.Join(dir, name))
	}
	for _, path := range maskFiles {
		args = append(args, "-loop", "1", "-framerate", fr, "-i", path)
	}
	args = append(args, "-loop", "1", "-framerate", fr, "-i", filepath.Join(dir, "above.png"))
	graph := layerGraph(boxes, sizes, haves, p.FPS, "out")
	if observe != nil {
		graph += ";[out]split=2[enc][png]"
	}
	args = append(args, "-filter_complex", graph)
	if observe != nil {
		args = append(args, "-map", "[enc]", "-frames:v", strconv.Itoa(nOut))
		args = append(args, x264EncodeTail(p, encodeThreads, out)...)
		args = append(args, "-map", "[png]", "-frames:v", strconv.Itoa(nOut), "-f", "image2pipe", "-c:v", "png", "-")
	} else {
		args = append(args, "-map", "[out]", "-frames:v", strconv.Itoa(nOut))
		args = append(args, x264EncodeTail(p, encodeThreads, out)...)
	}
	noteRender(renderNote{EncodeThreads: encodeThreads, EncoderArgs: append([]string(nil), args...)})
	cmd := command(ctx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if observe == nil {
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("encode: %w: %s", err, stderr.String())
		}
		return nil
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	for i := 0; i < nOut; i++ {
		b, err := readPNG(stdout)
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("composed frame %d: %w: %s", i, err, stderr.String())
		}
		if err = observe(ch.First+i, b); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
	}
	_, _ = io.Copy(io.Discard, stdout)
	if err = cmd.Wait(); err != nil {
		return fmt.Errorf("encode: %w: %s", err, stderr.String())
	}
	return nil
}
