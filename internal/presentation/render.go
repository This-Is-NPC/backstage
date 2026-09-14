package presentation

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

//go:embed web/*
var web embed.FS

type Renderer struct {
	plan        *Plan
	ctx         context.Context
	cancel      context.CancelFunc
	stopBrowser context.CancelFunc
	server      *http.Server
	files       map[string]string
	mu          sync.Mutex
	url         string
}

func dependencies() (string, error) {
	for _, n := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(n); err != nil {
			return "", fmt.Errorf("install %s to render presentations", n)
		}
	}
	for _, n := range []string{"chromium", "chromium-browser", "google-chrome"} {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("install Chromium to render presentations")
}
func NewRenderer(ctx context.Context, p *Plan) (*Renderer, error) {
	browser, err := dependencies()
	if err != nil {
		return nil, err
	}
	r := &Renderer{plan: p, files: map[string]string{}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	token := make([]byte, 24)
	if _, err = rand.Read(token); err != nil {
		_ = listener.Close()
		return nil, err
	}
	prefix := "/" + hex.EncodeToString(token) + "/"
	r.url = "http://" + listener.Addr().String() + prefix
	mux := http.NewServeMux()
	mux.HandleFunc(prefix, servePresentation(p.Project, prefix, r.files, &r.mu))
	r.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = r.server.Serve(listener) }()
	fail := func(e error) (*Renderer, error) { r.Close(); return nil, e }
	events, err := timelineEvents(p, r.url)
	if err != nil {
		return fail(err)
	}
	alloc, stop := chromedp.NewExecAllocator(ctx, append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browser), chromedp.Flag("disable-dev-shm-usage", true), chromedp.Flag("force-color-profile", "srgb"))...)
	r.stopBrowser = stop
	r.ctx, r.cancel = chromedp.NewContext(alloc)
	if err = chromedp.Run(r.ctx, emulation.SetDeviceMetricsOverride(int64(p.Width), int64(p.Height), 1, false), chromedp.Navigate(r.url+"builtin/runtime.html")); err != nil {
		return fail(err)
	}
	config := map[string]any{"events": events, "parameters": p.Document.Parameters, "text": p.Text, "duration": p.Document.Duration}
	if err = r.eval("initialize", config); err != nil {
		return fail(err)
	}
	return r, nil
}
func (r *Renderer) eval(fn string, value any) error {
	_, err := r.evalNumber(fn, value, false)
	return err
}

func (r *Renderer) evalNumber(fn string, value any, read bool) (float64, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	expr := "window." + fn + "(" + string(b) + ")"
	await := func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }
	if !read {
		return 0, chromedp.Run(ctx, chromedp.Evaluate(expr, nil, await))
	}
	var ms float64
	err = chromedp.Run(ctx, chromedp.Evaluate(expr, &ms, await))
	return ms, err
}
func (r *Renderer) Close() {
	if r.cancel != nil {
		r.cancel()
	}
	if r.stopBrowser != nil {
		r.stopBrowser()
	}
	if r.server != nil {
		_ = r.server.Close()
	}
}

type frameTimes struct {
	transfer, draw, screenshot time.Duration
}

func encodeImages(frames map[string][]byte) map[string]string {
	images := map[string]string{}
	for id, b := range frames {
		images[id] = base64.StdEncoding.EncodeToString(b)
	}
	return images
}

func captureScreenshot(ctx context.Context) ([]byte, error) {
	optimize := screenshotOptimize
	if screenshotObserver != nil {
		screenshotObserver(optimize)
	}
	var shot []byte
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		shot, err = page.CaptureScreenshot().WithFromSurface(true).WithFormat(page.CaptureScreenshotFormatPng).WithOptimizeForSpeed(optimize).Do(ctx)
		return err
	}))
	return shot, err
}

func (r *Renderer) frame(t float64, frames map[string][]byte) ([]byte, error) {
	if err := r.eval("draw", map[string]any{"time": t, "images": encodeImages(frames)}); err != nil {
		return nil, err
	}
	return captureScreenshot(r.ctx)
}

// frameTimed calls drawTimed. Measured draw includes image load and the final requestAnimationFrame.
func (r *Renderer) frameTimed(t float64, frames map[string][]byte) ([]byte, frameTimes, error) {
	var st frameTimes
	t0 := time.Now()
	images := encodeImages(frames)
	t1 := time.Now()
	ms, err := r.evalNumber("drawTimed", map[string]any{"time": t, "images": images}, true)
	if err != nil {
		return nil, st, err
	}
	t2 := time.Now()
	jsDraw := time.Duration(ms * float64(time.Millisecond))
	st.draw = jsDraw
	overhead := t2.Sub(t1) - jsDraw
	if overhead < 0 {
		overhead = 0
	}
	st.transfer = t1.Sub(t0) + overhead
	shotAt := time.Now()
	shot, err := captureScreenshot(r.ctx)
	st.screenshot = time.Since(shotAt)
	return shot, st, err
}

// Check exercises template initialization and a frame at every transition boundary.
func Check(ctx context.Context, p *Plan) error {
	r, err := NewRenderer(ctx, p)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, e := range p.Document.Timeline {
		if _, err = r.frame(e.At, map[string][]byte{}); err != nil {
			return err
		}
	}
	return nil
}

func Render(ctx context.Context, p *Plan, out string, progress io.Writer) (err error) {
	started := time.Now()
	if progress == nil {
		progress = io.Discard
	}
	if _, err = dependencies(); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	work, err := os.MkdirTemp(filepath.Dir(out), ".backstage-render-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	var timings renderTimings
	fmt.Fprintln(progress, ">> validate HTML templates")
	rendererAt := time.Now()
	r, err := NewRenderer(ctx, p)
	if err != nil {
		return err
	}
	timings.RendererStartSeconds = roundSec(time.Since(rendererAt))
	defer r.Close()
	decoders := map[string]*decoder{}
	defer func() {
		for _, d := range decoders {
			d.close()
		}
	}()
	cache, err := openRenderCache(ctx, progress)
	if err != nil {
		return err
	}
	defer cache.Close()
	trackPaths, prepare, err := prepareTracks(ctx, p, work, progress, cache)
	if err != nil {
		return err
	}
	if len(prepare) > 0 {
		timings.PrepareTrackSeconds = prepare
	}
	for _, id := range sortedKeys(p.Tracks) {
		d, e := newDecoder(ctx, trackPaths[id])
		if e != nil {
			return e
		}
		decoders[id] = d
	}
	fmt.Fprintln(progress, ">> prepare audio")
	mixAt := time.Now()
	audio, parts, err := p.mix(ctx, work, cache)
	if err != nil {
		return err
	}
	if audio != "" {
		sec := roundSec(time.Since(mixAt))
		timings.AudioSeconds = &sec
		if len(parts) > 0 {
			timings.AudioPartSeconds = parts
		}
	}
	video := filepath.Join(work, "video.mp4")
	_, _, encodeThreads := resolveRenderThreads(planThreadCfg(p), len(p.Tracks))
	encArgs := []string{"-v", "error", "-y", "-f", "image2pipe", "-framerate", strconv.Itoa(p.FPS), "-i", "-", "-an", "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p", "-threads", strconv.Itoa(encodeThreads), video}
	noteRender(renderNote{EncodeThreads: encodeThreads, EncoderArgs: append([]string(nil), encArgs...)})
	encoder := command(ctx, "ffmpeg", encArgs...)
	var stderr bytes.Buffer
	encoder.Stderr = &stderr
	pipe, err := encoder.StdinPipe()
	if err != nil {
		return err
	}
	// encode-seconds is Start to Wait and overlaps the Chromium frame loop.
	encodeAt := time.Now()
	if err = encoder.Start(); err != nil {
		return err
	}
	done := false
	defer func() {
		_ = pipe.Close()
		if !done {
			_ = encoder.Process.Kill()
			_ = encoder.Wait()
		}
	}()
	var decodeH, transferH, drawH, shotH, writeH msHist
	var decodedPNG, screenshotBytes int64
	for n := 0; n < p.Frames; n++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		t := float64(n) / float64(p.FPS)
		frames := map[string][]byte{}
		decodeAt := time.Now()
		for _, id := range sortedKeys(p.Tracks) {
			tr := p.Tracks[id]
			local := t - tr.Start
			if local < 0 || (local >= tr.Duration && tr.OnEnd == "hide") {
				continue
			}
			index := int(math.Floor(local*float64(p.FPS) + 1e-8))
			b, e := decoders[id].get(index)
			if e != nil {
				return e
			}
			frames[id] = b
			decodedPNG += int64(len(b))
		}
		decodeH.add(time.Since(decodeAt))
		shot, st, e := r.frameTimed(t, frames)
		if e != nil {
			return fmt.Errorf("frame %d: %w", n, e)
		}
		transferH.add(st.transfer)
		drawH.add(st.draw)
		shotH.add(st.screenshot)
		screenshotBytes += int64(len(shot))
		writeAt := time.Now()
		if _, e = pipe.Write(shot); e != nil {
			return e
		}
		writeH.add(time.Since(writeAt))
		if n%p.FPS == 0 {
			fmt.Fprintf(progress, ">> render %d/%d frames\n", n, p.Frames)
		}
	}
	_ = pipe.Close()
	err = encoder.Wait()
	done = true
	if err != nil {
		return fmt.Errorf("encode: %w: %s", err, stderr.String())
	}
	timings.EncodeSeconds = roundSec(time.Since(encodeAt))
	final := video
	if audio != "" {
		final = filepath.Join(work, "final.mp4")
		muxAt := time.Now()
		if err = run(ctx, "ffmpeg", "-v", "error", "-y", "-i", video, "-i", audio, "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-t", num(p.Document.Duration), "-movflags", "+faststart", final); err != nil {
			return err
		}
		sec := roundSec(time.Since(muxAt))
		timings.MuxSeconds = &sec
	}
	timings.Decode = decodeH.snapshot()
	timings.Transfer = transferH.snapshot()
	timings.Draw = drawH.snapshot()
	timings.Screenshot = shotH.snapshot()
	timings.EncodeWrite = writeH.snapshot()
	timings.DecodedPNGBytes = decodedPNG
	timings.ScreenshotBytes = screenshotBytes
	timings.CacheHits = cache.hits
	timings.CacheMisses = cache.misses
	collectWorkBytes(&timings, work, p, trackPaths)
	if err = r.metadata(work, &timings, started); err != nil {
		return err
	}
	fmt.Fprint(progress, timings.progressLine(p.Frames))
	if err = os.Rename(final, out); err != nil {
		return err
	}
	return os.Rename(filepath.Join(work, "facts.json"), strings.TrimSuffix(out, filepath.Ext(out))+".facts.json")
}
func (r *Renderer) metadata(dir string, timings *renderTimings, started time.Time) error {
	metaAt := time.Now()
	inputs := map[string]string{}
	for rel, path := range r.plan.Inputs {
		inputs[rel] = path
	}
	r.mu.Lock()
	for rel, path := range r.files {
		inputs[rel] = path
	}
	r.mu.Unlock()
	hashes := map[string]string{}
	for rel, path := range inputs {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		_ = f.Close()
		if err != nil {
			return err
		}
		hashes[rel] = hex.EncodeToString(h.Sum(nil))
	}
	for _, name := range []string{"runtime.html", "template.html", "visual.html"} {
		b, err := web.ReadFile("web/" + name)
		if err != nil {
			return err
		}
		h := sha256.Sum256(b)
		hashes["builtin:"+name] = hex.EncodeToString(h[:])
	}
	versions := map[string]string{}
	browser, _ := dependencies()
	for _, name := range []string{browser, "ffmpeg", "ffprobe"} {
		flag := "-version"
		if name == browser {
			flag = "--version"
		}
		b, err := command(r.ctx, name, flag).Output()
		if err != nil {
			return err
		}
		versions[filepath.Base(name)] = strings.SplitN(string(b), "\n", 2)[0]
	}
	timings.MetadataSeconds = roundSec(time.Since(metaAt))
	timings.TotalSeconds = roundSec(time.Since(started))
	data := map[string]any{"version": 1, "presentation": r.plan.Name, "configuration": r.plan.Document, "render": map[string]int{"w": r.plan.Width, "h": r.plan.Height, "fps": r.plan.FPS, "frames": r.plan.Frames}, "inputs": hashes, "tools": versions, "timings": timings}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "facts.json"), b, 0o644)
}
