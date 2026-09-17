package presentation

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func servePresentation(p *scene.Project, prefix string, files map[int]map[string]string, mu *sync.Mutex) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; frame-src 'self'; connect-src 'none'; media-src 'none'; object-src 'none'; base-uri 'self'")
		w.Header().Set("Cache-Control", "no-store")
		if !strings.HasPrefix(req.URL.Path, prefix) {
			http.NotFound(w, req)
			return
		}
		rel := strings.TrimPrefix(req.URL.Path, prefix)
		if strings.HasPrefix(rel, "builtin/") {
			serveBuiltin(w, req, strings.TrimPrefix(rel, "builtin/"))
			return
		}
		event, rest, ok := parseEventRest(rel)
		if !ok {
			http.NotFound(w, req)
			return
		}
		if strings.HasPrefix(rest, "builtin/") {
			serveBuiltin(w, req, strings.TrimPrefix(rest, "builtin/"))
			return
		}
		asset, ok := assetRelFromInner(rest)
		if !ok || !allowedAssetExt(asset) {
			http.NotFound(w, req)
			return
		}
		file, e := resolveWorkspaceAsset(p, asset)
		if e != nil {
			recordEventFile(files, mu, event, asset, "")
			http.NotFound(w, req)
			return
		}
		info, e := os.Stat(file)
		if e != nil || !info.Mode().IsRegular() {
			recordEventFile(files, mu, event, asset, "")
			http.NotFound(w, req)
			return
		}
		f, e := os.Open(file)
		if e != nil {
			recordEventFile(files, mu, event, asset, "")
			http.NotFound(w, req)
			return
		}
		defer f.Close()
		recordEventFile(files, mu, event, asset, file)
		http.ServeContent(w, req, filepath.Base(file), info.ModTime(), f)
	}
}

func allowedAssetExt(rel string) bool {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".html", ".css", ".js", ".svg", ".png", ".jpg", ".jpeg", ".webp", ".woff", ".woff2", ".ttf", ".otf":
		return true
	default:
		return false
	}
}

func recordEventFile(files map[int]map[string]string, mu *sync.Mutex, event int, rel, path string) {
	if files == nil || mu == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if files[event] == nil {
		files[event] = map[string]string{}
	}
	files[event][rel] = path
}

func serveBuiltin(w http.ResponseWriter, req *http.Request, name string) {
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
}

func timelineEvents(p *Plan, baseURL string) ([]map[string]any, error) {
	events := make([]map[string]any, 0, len(p.Document.Timeline))
	for i, e := range p.Document.Timeline {
		eventBase := baseURL + eventPrefix(i)
		u := eventBase + "builtin/template.html"
		if p.Template != "" {
			asset, err := workspaceAssetURL(p.Project, eventBase, p.Template)
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
			u, err = workspaceAssetURL(p.Project, eventBase, abs)
			if err != nil {
				return nil, fmt.Errorf("visual scene %s: %w", e.Scene, err)
			}
			narration = s.Narration
		}
		events = append(events, map[string]any{"at": e.At, "layout": e.Layout, "slots": e.Slots, "transition": e.Transition, "url": u, "narration": narration})
	}
	return events, nil
}

func eventPrefix(i int) string {
	return "event-" + strconv.Itoa(i) + "/"
}

func parseEventRest(rel string) (event int, rest string, ok bool) {
	if !strings.HasPrefix(rel, "event-") {
		return 0, "", false
	}
	body := strings.TrimPrefix(rel, "event-")
	slash := strings.IndexByte(body, '/')
	if slash < 1 {
		return 0, "", false
	}
	n, err := strconv.Atoi(body[:slash])
	if err != nil || n < 0 {
		return 0, "", false
	}
	return n, body[slash+1:], true
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

func eventAssetRel(prefix, urlPath string) (int, string, bool) {
	if !strings.HasPrefix(urlPath, prefix) {
		return 0, "", false
	}
	rest := strings.TrimPrefix(urlPath, prefix)
	event, inner, ok := parseEventRest(rest)
	if !ok {
		return 0, "", false
	}
	rel, ok := assetRelFromInner(inner)
	return event, rel, ok
}

func assetRelFromInner(inner string) (string, bool) {
	if !strings.HasPrefix(inner, "asset/") {
		return "", false
	}
	rel := strings.TrimPrefix(inner, "asset/")
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
