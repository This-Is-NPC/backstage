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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
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
	mux.HandleFunc(prefix, func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; frame-src 'self'; connect-src 'none'; media-src 'none'; object-src 'none'; base-uri 'self'")
		w.Header().Set("Cache-Control", "no-store")
		rel := strings.TrimPrefix(req.URL.Path, prefix)
		if strings.HasPrefix(rel, "builtin/") {
			name := strings.TrimPrefix(rel, "builtin/")
			if name != "runtime.html" && name != "template.html" && name != "visual.html" {
				http.NotFound(w, req)
				return
			}
			b, e := web.ReadFile("web/" + name)
			if e != nil {
				http.NotFound(w, req)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(b)
			return
		}
		if !strings.HasPrefix(rel, "asset/") {
			http.NotFound(w, req)
			return
		}
		rel = strings.TrimPrefix(rel, "asset/")
		switch strings.ToLower(filepath.Ext(rel)) {
		case ".html", ".css", ".js", ".svg", ".png", ".jpg", ".jpeg", ".webp", ".woff", ".woff2", ".ttf", ".otf":
		default:
			http.NotFound(w, req)
			return
		}
		path, e := p.Project.SafePath(rel)
		if e != nil {
			http.NotFound(w, req)
			return
		}
		info, e := os.Stat(path)
		if e != nil || !info.Mode().IsRegular() {
			http.NotFound(w, req)
			return
		}
		r.mu.Lock()
		r.files[rel] = path
		r.mu.Unlock()
		http.ServeFile(w, req, path)
	})
	r.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = r.server.Serve(listener) }()
	alloc, stop := chromedp.NewExecAllocator(ctx, append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browser), chromedp.Flag("disable-dev-shm-usage", true), chromedp.Flag("force-color-profile", "srgb"))...)
	r.stopBrowser = stop
	r.ctx, r.cancel = chromedp.NewContext(alloc)
	fail := func(e error) (*Renderer, error) { r.Close(); return nil, e }
	if err = chromedp.Run(r.ctx, emulation.SetDeviceMetricsOverride(int64(p.Width), int64(p.Height), 1, false), chromedp.Navigate(r.url+"builtin/runtime.html")); err != nil {
		return fail(err)
	}
	events := make([]map[string]any, 0, len(p.Document.Timeline))
	for _, e := range p.Document.Timeline {
		u := r.url + "builtin/template.html"
		if p.Template != "" {
			rel, _ := filepath.Rel(p.Project.Dir, p.Template)
			u = r.assetURL(rel)
		}
		var narration any
		if e.Scene != "" {
			s := p.Scenes[e.Scene]
			u = r.assetURL(s.Entry)
			narration = s.Narration
		}
		events = append(events, map[string]any{"at": e.At, "layout": e.Layout, "slots": e.Slots, "transition": e.Transition, "url": u, "narration": narration})
	}
	config := map[string]any{"events": events, "parameters": p.Document.Parameters, "text": p.Text, "duration": p.Document.Duration}
	if err = r.eval("initialize", config); err != nil {
		return fail(err)
	}
	return r, nil
}
func (r *Renderer) assetURL(rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return r.url + "asset/" + strings.Join(parts, "/")
}
func (r *Renderer) eval(fn string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	return chromedp.Run(ctx, chromedp.Evaluate("window."+fn+"("+string(b)+")", nil, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }))
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
func (r *Renderer) frame(t float64, frames map[string][]byte) ([]byte, error) {
	images := map[string]string{}
	for id, b := range frames {
		images[id] = base64.StdEncoding.EncodeToString(b)
	}
	if err := r.eval("draw", map[string]any{"time": t, "images": images}); err != nil {
		return nil, err
	}
	var shot []byte
	err := chromedp.Run(r.ctx, chromedp.CaptureScreenshot(&shot))
	return shot, err
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
	fmt.Fprintln(progress, ">> validate HTML templates")
	r, err := NewRenderer(ctx, p)
	if err != nil {
		return err
	}
	defer r.Close()
	decoders := map[string]*decoder{}
	defer func() {
		for _, d := range decoders {
			d.close()
		}
	}()
	for i, id := range sortedKeys(p.Tracks) {
		fmt.Fprintf(progress, ">> prepare track %s\n", id)
		path, e := prepareTrack(ctx, p.Tracks[id], work, fmt.Sprintf("track-%d", i), p.FPS)
		if e != nil {
			return e
		}
		d, e := newDecoder(ctx, path)
		if e != nil {
			return e
		}
		decoders[id] = d
	}
	fmt.Fprintln(progress, ">> prepare audio")
	audio, err := p.mix(ctx, work)
	if err != nil {
		return err
	}
	video := filepath.Join(work, "video.mp4")
	encoder := command(ctx, "ffmpeg", "-v", "error", "-y", "-f", "image2pipe", "-framerate", strconv.Itoa(p.FPS), "-i", "-", "-an", "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p", "-threads", "2", video)
	var stderr bytes.Buffer
	encoder.Stderr = &stderr
	pipe, err := encoder.StdinPipe()
	if err != nil {
		return err
	}
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
	for n := 0; n < p.Frames; n++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		t := float64(n) / float64(p.FPS)
		frames := map[string][]byte{}
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
		}
		shot, e := r.frame(t, frames)
		if e != nil {
			return fmt.Errorf("frame %d: %w", n, e)
		}
		if _, e = pipe.Write(shot); e != nil {
			return e
		}
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
	final := video
	if audio != "" {
		final = filepath.Join(work, "final.mp4")
		if err = run(ctx, "ffmpeg", "-v", "error", "-y", "-i", video, "-i", audio, "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-t", num(p.Document.Duration), "-movflags", "+faststart", final); err != nil {
			return err
		}
	}
	if err = r.metadata(work); err != nil {
		return err
	}
	if err = os.Rename(final, out); err != nil {
		return err
	}
	return os.Rename(filepath.Join(work, "facts.json"), strings.TrimSuffix(out, filepath.Ext(out))+".facts.json")
}
func (r *Renderer) metadata(dir string) error {
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
	data := map[string]any{"version": 1, "presentation": r.plan.Name, "configuration": r.plan.Document, "render": map[string]int{"w": r.plan.Width, "h": r.plan.Height, "fps": r.plan.FPS, "frames": r.plan.Frames}, "inputs": hashes, "tools": versions}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "facts.json"), b, 0o644)
}
