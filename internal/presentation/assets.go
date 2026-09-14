package presentation

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func servePresentation(p *scene.Project, prefix string, files map[string]string, mu *sync.Mutex) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
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
		rel, ok := assetRelFromRequest(prefix, req.URL.Path)
		if !ok {
			http.NotFound(w, req)
			return
		}
		switch strings.ToLower(filepath.Ext(rel)) {
		case ".html", ".css", ".js", ".svg", ".png", ".jpg", ".jpeg", ".webp", ".woff", ".woff2", ".ttf", ".otf":
		default:
			http.NotFound(w, req)
			return
		}
		file, e := resolveWorkspaceAsset(p, rel)
		if e != nil {
			http.NotFound(w, req)
			return
		}
		info, e := os.Stat(file)
		if e != nil || !info.Mode().IsRegular() {
			http.NotFound(w, req)
			return
		}
		if files != nil && mu != nil {
			mu.Lock()
			files[rel] = file
			mu.Unlock()
		}
		f, e := os.Open(file)
		if e != nil {
			http.NotFound(w, req)
			return
		}
		defer f.Close()
		http.ServeContent(w, req, filepath.Base(file), info.ModTime(), f)
	}
}

func timelineEvents(p *Plan, baseURL string) ([]map[string]any, error) {
	events := make([]map[string]any, 0, len(p.Document.Timeline))
	for _, e := range p.Document.Timeline {
		u := baseURL + "builtin/template.html"
		if p.Template != "" {
			asset, err := workspaceAssetURL(p.Project, baseURL, p.Template)
			if err != nil {
				return nil, err
			}
			u = asset
		}
		var narration any
		if e.Scene != "" {
			s := p.Scenes[e.Scene]
			if s == nil {
				return nil, fmt.Errorf("unknown visual scene %q", e.Scene)
			}
			abs, err := p.Project.InputPath(s.Entry)
			if err != nil {
				return nil, fmt.Errorf("visual scene %s: %w", e.Scene, err)
			}
			u, err = workspaceAssetURL(p.Project, baseURL, abs)
			if err != nil {
				return nil, fmt.Errorf("visual scene %s: %w", e.Scene, err)
			}
			narration = s.Narration
		}
		events = append(events, map[string]any{"at": e.At, "layout": e.Layout, "slots": e.Slots, "transition": e.Transition, "url": u, "narration": narration})
	}
	return events, nil
}

func workspaceAssetURL(p *scene.Project, baseURL, abs string) (string, error) {
	rel, err := workspaceRel(p, abs)
	if err != nil {
		return "", err
	}
	if rel == "" || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("asset %q is outside the workspace", abs)
	}
	return assetURL(baseURL, rel), nil
}

func assetURL(baseURL, rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return baseURL + "asset/" + strings.Join(parts, "/")
}

func workspaceRel(p *scene.Project, abs string) (string, error) {
	rel, err := filepath.Rel(p.WorkspaceRoot(), abs)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func assetRelFromRequest(prefix, urlPath string) (string, bool) {
	if !strings.HasPrefix(urlPath, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(urlPath, prefix)
	if !strings.HasPrefix(rest, "asset/") {
		return "", false
	}
	rel := strings.TrimPrefix(rest, "asset/")
	cleaned := path.Clean(rel)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || path.IsAbs(cleaned) {
		return "", false
	}
	return cleaned, true
}

func resolveWorkspaceAsset(p *scene.Project, rel string) (string, error) {
	ws := p.WorkspaceRoot()
	return scene.ConfinedPath(ws, ws, filepath.FromSlash(rel))
}
