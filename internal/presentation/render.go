package presentation

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
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
	scale       float64
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

func chromiumAllocatorOptions(browser string) []chromedp.ExecAllocatorOption {
	return append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browser), chromedp.Flag("disable-dev-shm-usage", true), chromedp.Flag("force-color-profile", "srgb"), chromedp.Flag("disable-partial-raster", true))
}

func NewRenderer(ctx context.Context, p *Plan) (*Renderer, error) {
	browser, err := dependencies()
	if err != nil {
		return nil, err
	}
	r := &Renderer{plan: p, files: map[string]string{}, scale: 1}
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
	alloc, stop := chromedp.NewExecAllocator(ctx, chromiumAllocatorOptions(browser)...)
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

func (r *Renderer) loadedFiles() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]string{}
	for rel, path := range r.files {
		out[rel] = path
	}
	return out
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

func (r *Renderer) captureScreenshot() ([]byte, error) {
	observeMu.Lock()
	optimize := screenshotOptimize
	obs := screenshotObserver
	if obs != nil {
		obs(optimize)
	}
	observeMu.Unlock()
	var shot []byte
	err := chromedp.Run(r.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		params := page.CaptureScreenshot().WithFromSurface(true).WithFormat(page.CaptureScreenshotFormatPng).WithOptimizeForSpeed(optimize)
		if r.scale > 0 && r.scale != 1 {
			params = params.WithClip(&page.Viewport{X: 0, Y: 0, Width: float64(r.plan.Width), Height: float64(r.plan.Height), Scale: r.scale})
		}
		var err error
		shot, err = params.Do(ctx)
		return err
	}))
	return shot, err
}

func (r *Renderer) frame(t float64, frames map[string][]byte) ([]byte, error) {
	if err := r.eval("draw", map[string]any{"time": t, "images": encodeImages(frames)}); err != nil {
		return nil, err
	}
	return r.captureScreenshot()
}

// frameTimed calls drawTimed. Measured draw includes image load, host fonts, and a compositor paint.
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
	shot, err := r.captureScreenshot()
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
