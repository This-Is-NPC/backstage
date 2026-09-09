package presentation

import (
	"context"
	"fmt"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Preview renders a private preview once, then plays that exact result. It does
// not run an independent approximation of the timeline in a second player.
func Preview(ctx context.Context, p *Plan, log io.Writer) error {
	dir, err := os.MkdirTemp("", "backstage-preview-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "preview.mp4")
	if err = Render(ctx, p, out, log); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/preview.mp4", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, out) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Backstage preview</title><style>body{background:#11111a;color:#eee;font:18px sans-serif;margin:2rem}video{width:100%%;max-height:80vh}button{padding:12px}</style><video id="v" src="/preview.mp4" controls></video><p><button id="back">Previous frame</button> <button id="next">Next frame</button> <output id="time"
 "github.com/This-Is-NPC/backstage/internal/scene"></output></p><script>const v=document.getElementById('v'),fps=%d;function step(d){v.pause();v.currentTime=Math.max(0,Math.min(v.duration,(Math.round(v.currentTime*fps)+d)/fps))}document.getElementById('back').onclick=()=>step(-1);document.getElementById('next').onclick=()=>step(1);v.ontimeupdate=()=>document.getElementById('time').textContent=v.currentTime.toFixed(3)+' s';</script>`, p.FPS)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	address := "http://" + listener.Addr().String()
	fmt.Fprintf(log, ">> preview: %s (Ctrl-C to close)\n", address)
	if opener, err := exec.LookPath("xdg-open"); err == nil {
		_ = command(ctx, opener, address).Run()
	}
	<-ctx.Done()
	return nil
}

func InitTemplate(root, name string) error {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return fmt.Errorf("template name must be a single directory name")
	}
	dir, pathErr := (&scene.Project{Dir: root}).SafePath("templates", name)
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
