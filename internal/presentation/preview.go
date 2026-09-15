package presentation

import (
	"context"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func writePreview(ctx context.Context, p *Plan, out string, log io.Writer, opts PreviewOpts) error {
	ro, err := renderOptsFromPreview(p, opts)
	if err != nil {
		return err
	}
	return renderRange(ctx, p, out, log, ro)
}

// Preview renders a private preview once, then plays that exact result. It does
// not run an independent approximation of the timeline in a second player.
func Preview(ctx context.Context, p *Plan, log io.Writer, opts PreviewOpts) error {
	dir, err := os.MkdirTemp("", "backstage-preview-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "preview.mp4")
	if err = writePreview(ctx, p, out, log, opts); err != nil {
		return err
	}
	address, stop, err := servePreview(p, opts, out)
	if err != nil {
		return err
	}
	defer stop()
	fmt.Fprintf(log, ">> preview: %s (Ctrl-C to close)\n", address)
	if opener, err := exec.LookPath("xdg-open"); err == nil {
		_ = command(ctx, opener, address).Run()
	}
	<-ctx.Done()
	return nil
}

func servePreview(p *Plan, opts PreviewOpts, video string) (string, func(), error) {
	handler, err := previewPlayer(p, opts, video)
	if err != nil {
		return "", nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	return "http://" + listener.Addr().String(), func() { _ = server.Close() }, nil
}

func previewTitle(p *Plan, o PreviewOpts) string {
	title := "Backstage preview"
	var extra []string
	if o.From != 0 || o.To != p.Document.Duration {
		extra = append(extra, fmt.Sprintf("%s–%s s", strconv.FormatFloat(o.From, 'f', -1, 64), strconv.FormatFloat(o.To, 'f', -1, 64)))
	}
	if o.Scale != 1 {
		extra = append(extra, "×"+strconv.FormatFloat(o.Scale, 'f', -1, 64))
	}
	if len(extra) > 0 {
		return title + " " + strings.Join(extra, " ")
	}
	return title
}

func previewPlayer(p *Plan, opts PreviewOpts, video string) (http.Handler, error) {
	ro, err := renderOptsFromPreview(p, opts)
	if err != nil {
		return nil, err
	}
	clock := float64(ro.First) / float64(p.FPS)
	return previewHandler(previewTitle(p, opts), p.FPS, clock, video), nil
}

func previewHandler(title string, fps int, from float64, video string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/preview.mp4", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, video)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title><style>body{background:#11111a;color:#eee;font:18px sans-serif;margin:2rem}video{width:100%%;max-height:80vh}button{padding:12px}</style><video id="v" src="/preview.mp4" controls></video><p><button id="back">Previous frame</button> <button id="next">Next frame</button> <output id="time"></output></p><script>const v=document.getElementById('v'),fps=%d,from=%s;function step(d){v.pause();v.currentTime=Math.max(0,Math.min(v.duration,(Math.round(v.currentTime*fps)+d)/fps))}document.getElementById('back').onclick=()=>step(-1);document.getElementById('next').onclick=()=>step(1);v.ontimeupdate=()=>document.getElementById('time').textContent=(from+v.currentTime).toFixed(3)+' s';</script>`,
			html.EscapeString(title), fps, strconv.FormatFloat(from, 'f', -1, 64))
	})
	return mux
}

func InitTemplate(root, name string) error {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return fmt.Errorf("template name must be a single directory name")
	}
	dir, pathErr := (&scene.Project{Dir: root, Workspace: root}).OutputPath("templates", name)
	if pathErr != nil {
		return pathErr
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return err
	}
	for name, source := range map[string]string{"template.html": "template.html", "visual.html": "visual.html"} {
		b, err := web.ReadFile("web/" + source)
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}
