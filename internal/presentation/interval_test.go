package presentation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg required")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe required")
	}
}

func previewPlan(duration float64, fps int) *Plan {
	return &Plan{Document: Document{Duration: duration}, FPS: fps, Frames: int(math.Ceil(duration*float64(fps) - 1e-9))}
}

func TestPreviewOptsRefuseNaNAndInf(t *testing.T) {
	p := previewPlan(8, 12)
	cases := []PreviewOpts{
		{From: math.NaN(), To: 2, Scale: 1},
		{From: 0, To: math.NaN(), Scale: 1},
		{From: 0, To: 2, Scale: math.NaN()},
		{From: math.Inf(1), To: 2, Scale: 1},
		{From: 0, To: math.Inf(-1), Scale: 1},
		{From: 0, To: 2, Scale: math.Inf(1)},
	}
	for i, o := range cases {
		if err := validatePreviewOpts(p, o); err == nil {
			t.Fatalf("case %d accepted non-finite %#v", i, o)
		}
	}
}

func TestPreviewOptsRefuseEmptyInterval(t *testing.T) {
	p := previewPlan(8, 12)
	if err := validatePreviewOpts(p, PreviewOpts{From: 1.01, To: 1.05, Scale: 1}); err == nil {
		t.Fatal("accepted interval with no frames")
	}
	first, end := intervalFrames(1.01, 1.05, 12)
	if first != end {
		t.Fatalf("first=%d end=%d", first, end)
	}
}

func TestPreviewOptsRefuseBounds(t *testing.T) {
	p := previewPlan(8, 12)
	for _, o := range []PreviewOpts{
		{From: -0.1, To: 1, Scale: 1},
		{From: 0, To: 8.1, Scale: 1},
		{From: 2, To: 2, Scale: 1},
		{From: 3, To: 2, Scale: 1},
		{From: 0, To: 0, Scale: 1},
		{From: 0, To: 2, Scale: 0},
		{From: 0, To: 2, Scale: 1.1},
	} {
		if err := validatePreviewOpts(p, o); err == nil {
			t.Fatalf("accepted %#v", o)
		}
	}
	if err := validatePreviewOpts(p, PreviewOpts{From: 0, To: 8, Scale: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestPreviewToDefaultUsesChanged(t *testing.T) {
	p := previewPlan(8, 12)
	o := PreviewOpts{From: 0, To: 0, Scale: 1}
	if err := validatePreviewOpts(p, o); err == nil {
		t.Fatal("explicit to=0 must stay refused")
	}
	o.To = p.Document.Duration
	if err := validatePreviewOpts(p, o); err != nil {
		t.Fatal(err)
	}
}

func TestDecoderSeekTimeHalfFrame(t *testing.T) {
	if decoderSeekTime(0, 12) != "" {
		t.Fatalf("S=0: %q", decoderSeekTime(0, 12))
	}
	for _, c := range []struct{ s, fps int }{{1, 12}, {350, 12}, {719, 12}, {1, 30}, {29, 30}} {
		got := decoderSeekTime(c.s, c.fps)
		want := num((float64(c.s) - 0.5) / float64(c.fps))
		if got != want {
			t.Fatalf("S=%d fps=%d: %q want %q", c.s, c.fps, got, want)
		}
		if got == num(float64(c.s)/float64(c.fps)) {
			t.Fatalf("S=%d fps=%d used S/fps without half-frame margin", c.s, c.fps)
		}
	}
}

func TestAudioSamplesExclusiveIntegerMath(t *testing.T) {
	ss, es := audioSamples(12, 24, 12)
	if ss != 48000 || es != 96000 {
		t.Fatalf("12fps %d %d", ss, es)
	}
	ss, es = audioSamples(1, 3, 30)
	if ss != 1600 || es != 4800 {
		t.Fatalf("30fps %d %d", ss, es)
	}
	ss, es = audioSamples(1, 3, 7)
	if ss != int64(math.Round(48000.0/7)) || es != int64(math.Round(3*48000.0/7)) {
		t.Fatalf("7fps %d %d", ss, es)
	}
	if atrimFilter(ss, es) != fmt.Sprintf("atrim=start_sample=%d:end_sample=%d", ss, es) {
		t.Fatal(atrimFilter(ss, es))
	}
	if strings.Contains(atrimFilter(10, 20), "end_sample=19") {
		t.Fatal("end_sample is exclusive")
	}
}

func TestProgressLineIncludesDecoderStart(t *testing.T) {
	line := renderTimings{DecoderStartSeconds: 0.044, TotalSeconds: 1, Workers: 2}.progressLine(24)
	if !strings.Contains(line, ">> render timings:") || !strings.Contains(line, "decoder-start=0.044") || !strings.Contains(line, "frames=24") {
		t.Fatal(line)
	}
	if !strings.Contains(line, "workers=2") || !strings.Contains(line, "concat=0.000") {
		t.Fatal(line)
	}
	if strings.Contains(line, ">> decoder-start") {
		t.Fatal(line)
	}
}

func writePatternMKV(t *testing.T, fps int, duration float64) string {
	t.Helper()
	requireFFmpeg(t)
	path := filepath.Join(t.TempDir(), "pattern.mkv")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", fmt.Sprintf("testsrc=size=160x90:rate=%d:duration=%s", fps, num(duration)), "-an", "-vf", fmt.Sprintf("fps=%d", fps), "-c:v", "ffv1", "-g", "1", path); err != nil {
		t.Fatal(err)
	}
	return path
}

func decodeIndexes(t *testing.T, path string, start, fps int, indexes []int) [][]byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d, err := newDecoder(ctx, path, start, fps)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	var out [][]byte
	for _, i := range indexes {
		b, err := d.get(i)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, append([]byte(nil), b...))
	}
	return out
}

func TestDecoderSeekMatchesFromZero(t *testing.T) {
	requireFFmpeg(t)
	ctx := context.Background()
	for _, fps := range []int{12, 30} {
		path := writePatternMKV(t, fps, 2)
		packets, err := countPackets(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		zero := decodeIndexes(t, path, 0, fps, []int{0, 1, packets / 2, packets - 1})
		d0, err := newDecoder(ctx, path, 0, fps)
		if err != nil {
			t.Fatal(err)
		}
		if containsArg(d0.args, "-ss") {
			t.Fatalf("S=0 kept -ss: %v", d0.args)
		}
		d0.close()
		cases := []int{1, packets / 2, packets - 1}
		for _, s := range cases {
			d, err := newDecoder(ctx, path, s, fps)
			if err != nil {
				t.Fatal(err)
			}
			if d.start != s {
				t.Fatalf("decoder.start=%d want %d", d.start, s)
			}
			if s > 0 && !ssBeforeInput(d.args) {
				t.Fatalf("S=%d fps=%d -ss not before -i: %v", s, fps, d.args)
			}
			if s > 0 && d.args[indexOf(d.args, "-ss")+1] != decoderSeekTime(s, fps) {
				t.Fatalf("S=%d seek %v", s, d.args)
			}
			got, err := d.get(s)
			d.close()
			if err != nil {
				t.Fatal(err)
			}
			ref := decodeIndexes(t, path, 0, fps, []int{s})[0]
			if err = samePixels(got, ref); err != nil {
				t.Fatalf("fps=%d S=%d: %v", fps, s, err)
			}
		}
		if err = samePixels(zero[len(zero)-1], decodeIndexes(t, path, packets-1, fps, []int{packets - 1})[0]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDecoderTrackCases(t *testing.T) {
	requireFFmpeg(t)
	fps := 10
	path := writePatternMKV(t, fps, 1)
	later := CompiledTrack{Track: Track{Start: 0.8, OnEnd: "freeze"}, Duration: 1}
	s := trackFrame(later, 5, fps)
	if s != 0 {
		t.Fatalf("track that starts after from must open at 0, got %d", s)
	}
	if err := samePixels(decodeIndexes(t, path, s, fps, []int{0})[0], decodeIndexes(t, path, 0, fps, []int{0})[0]); err != nil {
		t.Fatal(err)
	}
	hide := CompiledTrack{Track: Track{Start: 0, OnEnd: "hide"}, Duration: 0.2}
	if trackNeeded(hide, 5, 8, fps) {
		t.Fatal("hide track outside interval should not open")
	}
	ctx := context.Background()
	tail := writePatternMKV(t, fps, 0.25)
	packets, err := countPackets(ctx, tail)
	if err != nil {
		t.Fatal(err)
	}
	if packets < 2 {
		t.Fatalf("packets=%d", packets)
	}
	d, err := openTrackDecoder(ctx, tail, packets+2, fps)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := d.get(packets + 2)
	d.close()
	if err != nil {
		t.Fatal(err)
	}
	fromZero := decodeIndexes(t, tail, 0, fps, []int{packets - 1})[0]
	if err = samePixels(frozen, fromZero); err != nil {
		t.Fatal(err)
	}
}

func TestAudioTrimMatchesMixSlice(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	mix := filepath.Join(dir, "mix.wav")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=1", "-ac", "2", "-c:a", "pcm_s16le", mix); err != nil {
		t.Fatal(err)
	}
	first, end, fps := 3, 9, 12
	ss, es := audioSamples(first, end, fps)
	cut := filepath.Join(dir, "cut.wav")
	if err := run(ctx, "ffmpeg", "-v", "error", "-y", "-i", mix, "-af", muxAudioFilter(ss, es), "-c:a", "pcm_s16le", cut); err != nil {
		t.Fatal(err)
	}
	mixPCM := rawPCM(t, mix)
	cutPCM := rawPCM(t, cut)
	frame := 4
	if len(cutPCM)/frame != int(es-ss) {
		t.Fatalf("samples %d want %d", len(cutPCM)/frame, es-ss)
	}
	want := mixPCM[int(ss)*frame : int(es)*frame]
	if !bytes.Equal(cutPCM, want) {
		t.Fatal("cut samples differ from mix slice")
	}
}

func TestIntervalExtractNeverSeeksH264(t *testing.T) {
	args := intervalSelectArgs(24, 48)
	for i, a := range args {
		if a == "-ss" {
			t.Fatalf("H.264 interval extract used -ss: %v", args)
		}
		if i+1 < len(args) && a == "-vf" && !strings.Contains(args[i+1], "between(n,24,47)") {
			t.Fatal(args)
		}
	}
}

func rawPCM(t *testing.T, path string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := command(ctx, "ffmpeg", "-v", "error", "-i", path, "-vn", "-f", "s16le", "-ac", "2", "-ar", "48000", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("pcm %s: %v: %s", path, err, stderr.String())
	}
	return b
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

func ssBeforeInput(args []string) bool {
	ss, in := indexOf(args, "-ss"), indexOf(args, "-i")
	return ss >= 0 && in > ss
}

func intervalSelectArgs(first, end int) []string {
	return []string{"-vf", fmt.Sprintf("select='between(n,%d,%d)'", first, end-1), "-fps_mode", "passthrough"}
}

func TestOpenTrackDecoderCountsOnlyPastEnd(t *testing.T) {
	requireFFmpeg(t)
	ctx := context.Background()
	path := writePatternMKV(t, 10, 0.25)
	packets, err := countPackets(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if packets < 2 {
		t.Fatalf("packets=%d", packets)
	}
	fromZero := decodeIndexes(t, path, 0, 10, []int{1, packets - 1})
	t.Cleanup(func() { observeCountPackets = nil })
	var n int
	observeCountPackets = func(string) { n++ }
	d, err := openTrackDecoder(ctx, path, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	midPNG, err := d.get(1)
	d.close()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("mid counted %d", n)
	}
	if err = samePixels(midPNG, fromZero[0]); err != nil {
		t.Fatal(err)
	}
	n = 0
	d, err = openTrackDecoder(ctx, path, packets-1, 10)
	if err != nil {
		t.Fatal(err)
	}
	last, err := d.get(packets - 1)
	d.close()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("last frame counted %d", n)
	}
	if err = samePixels(last, fromZero[1]); err != nil {
		t.Fatal(err)
	}
	n = 0
	d, err = openTrackDecoder(ctx, path, packets+3, 10)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := d.get(packets + 3)
	d.close()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("past end counted %d want 1", n)
	}
	if err = samePixels(frozen, fromZero[1]); err != nil {
		t.Fatal(err)
	}
}

func TestOpenTrackDecoderZeroSkipsPeek(t *testing.T) {
	requireFFmpeg(t)
	ctx := context.Background()
	path := writePatternMKV(t, 10, 0.25)
	d, err := openTrackDecoder(ctx, path, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	if d.frame != -1 || d.last != nil {
		t.Fatalf("S=0 peeked: frame=%d last=%v", d.frame, d.last != nil)
	}
}

func writeFFmpegShim(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nSHIM_DIR=" + strconv.Quote(dir) + "\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func openDecoderGuarded(t *testing.T, path string, start, fps int) (*decoder, error) {
	t.Helper()
	type out struct {
		d   *decoder
		err error
	}
	ch := make(chan out, 1)
	go func() {
		d, err := openTrackDecoder(context.Background(), path, start, fps)
		ch <- out{d, err}
	}()
	select {
	case r := <-ch:
		return r.d, r.err
	case <-time.After(5 * time.Second):
		t.Fatal("openTrackDecoder hung")
		return nil, nil
	}
}

func assertShimChildrenGone(t *testing.T, dir string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "child.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid < 1 {
		t.Fatalf("child.pid %q", b)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		err = syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("child %d still running", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestOpenTrackDecoderRejectsNonPNG(t *testing.T) {
	dir := writeFFmpegShim(t, "sleep 100 &\necho $! > \"$SHIM_DIR/child.pid\"\nprintf 'XXXXNOTPNG'\nwait")
	t.Cleanup(func() { observeCountPackets = nil })
	var n int
	observeCountPackets = func(string) { n++ }
	_, err := openDecoderGuarded(t, filepath.Join(dir, "in.mkv"), 1, 10)
	if err == nil || err.Error() != "invalid PNG stream" {
		t.Fatalf("err=%q", err)
	}
	if n != 0 {
		t.Fatalf("countPackets=%d", n)
	}
	assertShimChildrenGone(t, dir)
}

func TestOpenTrackDecoderReportsFFmpegExit(t *testing.T) {
	dir := writeFFmpegShim(t, "echo ffmpeg-fail >&2\nexit 7")
	t.Cleanup(func() { observeCountPackets = nil })
	var n int
	observeCountPackets = func(string) { n++ }
	_, err := openDecoderGuarded(t, filepath.Join(dir, "in.mkv"), 1, 10)
	if err == nil || !strings.Contains(err.Error(), "decode:") || !strings.Contains(err.Error(), "ffmpeg-fail") {
		t.Fatalf("err=%v", err)
	}
	if n != 0 {
		t.Fatalf("countPackets=%d", n)
	}
}

func TestOpenTrackDecoderRejectsTruncatedPNG(t *testing.T) {
	dir := writeFFmpegShim(t, `printf '\211PNG\r\n\032\nxxxx'`)
	t.Cleanup(func() { observeCountPackets = nil })
	var n int
	observeCountPackets = func(string) { n++ }
	_, err := openDecoderGuarded(t, filepath.Join(dir, "in.mkv"), 1, 10)
	if err == nil {
		t.Fatal("accepted truncated PNG")
	}
	if strings.Contains(err.Error(), "packets=") {
		t.Fatalf("counted packets: %v", err)
	}
	if n != 0 {
		t.Fatalf("countPackets=%d", n)
	}
}

func TestOpenTrackDecoderCountErrorIncludesStderr(t *testing.T) {
	requireFFmpeg(t)
	path := writePatternMKV(t, 10, 1)
	writeFFmpegShim(t, "echo decode-stderr >&2\nexit 0")
	t.Cleanup(func() { observeCountPackets = nil })
	_, err := openDecoderGuarded(t, path, 6, 10)
	if err == nil {
		t.Fatal("expected count error")
	}
	if !strings.Contains(err.Error(), "packets=") || !strings.Contains(err.Error(), "start=6") {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(err.Error(), "decode-stderr") {
		t.Fatalf("missing stderr: %v", err)
	}
}

func TestOpenTrackDecoderCorruptTailKeepsStderr(t *testing.T) {
	requireFFmpeg(t)
	good, path := writeCorruptTailMKV(t)
	ctx := context.Background()
	packets, err := countPackets(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if packets != 10 {
		t.Fatalf("packets=%d want 10", packets)
	}
	ref := decodeIndexes(t, good, 0, 10, []int{4})[0]
	d0, err := newDecoder(ctx, path, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		b, e := d0.get(i)
		if e != nil {
			d0.close()
			t.Fatalf("full decode i=%d: %v", i, e)
		}
		if i >= 5 && !bytes.Equal(b, ref) {
			d0.close()
			t.Fatalf("frozen i=%d is not good frame 4", i)
		}
	}
	d0.close()
	_, err = openTrackDecoder(ctx, path, 6, 10)
	if err == nil {
		t.Fatal("preview past last decodable succeeded")
	}
	if !strings.HasPrefix(err.Error(), "decode:") || !strings.Contains(err.Error(), "Invalid data found") {
		t.Fatalf("err=%v", err)
	}
}

func writeCorruptTailMKV(t *testing.T) (string, string) {
	t.Helper()
	requireFFmpeg(t)
	src := filepath.Join(t.TempDir(), "good.mkv")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=10:duration=1", "-an", "-c:v", "ffv1", "-g", "1", src); err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_packets", "-show_entries", "packet=pos,size", "-of", "json", src).Output()
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		Packets []struct {
			Pos  string `json:"pos"`
			Size string `json:"size"`
		} `json:"packets"`
	}
	if err = json.Unmarshal(raw, &info); err != nil {
		t.Fatal(err)
	}
	if len(info.Packets) != 10 {
		t.Fatalf("packets=%d", len(info.Packets))
	}
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	out := append([]byte(nil), body...)
	for i, p := range info.Packets {
		if i < 5 {
			continue
		}
		pos, size := atoi(t, p.Pos), atoi(t, p.Size)
		for j := pos + 40; j < pos+size && j < len(out); j++ {
			out[j] ^= 0x5A
		}
	}
	path := filepath.Join(t.TempDir(), "tail.mkv")
	if err = os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	return src, path
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCodedPatternDecodes(t *testing.T) {
	requireFFmpeg(t)
	path := filepath.Join(t.TempDir(), "coded.mp4")
	writeCodedMP4(t, path, 160, 90, 12, 1)
	for i, png := range decodeVideoPNGs(t, path, 12, 8) {
		got, err := readFrameCode(png)
		if err != nil {
			t.Fatal(err)
		}
		if got != i {
			t.Fatalf("frame %d code %d", i, got)
		}
	}
}

const frameCodeBits = 8

func writeCodedMP4(t *testing.T, path string, w, h, fps int, duration float64) {
	t.Helper()
	requireFFmpeg(t)
	block := w / frameCodeBits
	vf := fmt.Sprintf("geq=lum='if(bitand(N,pow(2,floor(X/%d))),255,0)':cb=128:cr=128", block)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", fmt.Sprintf("color=c=black:s=%dx%d:r=%d:d=%s", w, h, fps, num(duration)), "-vf", vf, "-pix_fmt", "yuv420p", path); err != nil {
		t.Fatal(err)
	}
}

func readFrameCode(b []byte) (int, error) {
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	block := w / frameCodeBits
	if block < 4 {
		return 0, fmt.Errorf("narrow %d", w)
	}
	code := 0
	for k := 0; k < frameCodeBits; k++ {
		x := img.Bounds().Min.X + k*block + block/2
		y := img.Bounds().Min.Y + h/2
		r, g, bl, _ := img.At(x, y).RGBA()
		if (int(r>>8)+int(g>>8)+int(bl>>8))/3 > 128 {
			code |= 1 << k
		}
	}
	return code, nil
}

func decodeVideoPNGs(t *testing.T, path string, fps, n int) [][]byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d, err := newDecoder(ctx, path, 0, fps)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	out := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		b, err := d.get(i)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, append([]byte(nil), b...))
	}
	return out
}

func TestPreviewTitleMentionsIntervalAndScale(t *testing.T) {
	p := previewPlan(8, 12)
	if previewTitle(p, PreviewOpts{From: 0, To: 8, Scale: 1}) != "Backstage preview" {
		t.Fatal(previewTitle(p, PreviewOpts{From: 0, To: 8, Scale: 1}))
	}
	got := previewTitle(p, PreviewOpts{From: 4, To: 6, Scale: 0.5})
	if !strings.Contains(got, "4–6 s") || !strings.Contains(got, "×0.5") {
		t.Fatal(got)
	}
}
