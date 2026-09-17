package presentation

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestAssetRelFromRequestRefusesOutsideRoute(t *testing.T) {
	prefix := "/token/"
	if event, rel, ok := eventAssetRel(prefix, "/token/event-0/asset/templates/a.css"); !ok || event != 0 || rel != "templates/a.css" {
		t.Fatalf("ok path: %d %q %v", event, rel, ok)
	}
	if event, rel, ok := eventAssetRel(prefix, "/token/event-2/asset/logo.png"); !ok || event != 2 || rel != "logo.png" {
		t.Fatalf("event path: %d %q %v", event, rel, ok)
	}
	for _, url := range []string{
		"/token/builtin/runtime.html",
		"/token/asset/templates/a.css",
		"/token/other/file.css",
		"/token/event-0/asset/../secret.css",
		"/token/event-0/asset/foo/../../secret.css",
		"/token/event-0/foo/../asset/a.css",
		"/token/event-x/asset/a.css",
	} {
		if _, _, ok := eventAssetRel(prefix, url); ok {
			t.Fatalf("%s remapped", url)
		}
	}
}

func TestInheritedTemplateAssetsOverHTTP(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	shared := filepath.Join(ws, "templates", "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(leaf, "scenes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "index.html"), []byte(`<link rel="stylesheet" href="style.css"><img src="logo.png">`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "style.css"), []byte("body{color:red}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "logo.png"), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "backstage.json"), []byte(`{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"templates": {"shared": {"entry": "templates/shared/index.html"}}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "backstage.json"), []byte(`{
		"extends": "../backstage.json",
		"presentations": {"show": {"file": "show.json", "template": "shared"}}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leaf, "show.json"), []byte(`{"version":1,"duration":1,"timeline":[{"at":0,"layout":"single"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := scene.LoadProject(filepath.Join(leaf, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plan.Inputs["templates/shared/index.html"]; !ok {
		t.Fatalf("facts keys should be workspace-relative: %v", plan.Inputs)
	}
	rel, err := workspaceRel(p, plan.Template)
	if err != nil || rel != "templates/shared/index.html" {
		t.Fatalf("template url rel = %q, %v", rel, err)
	}
	events, err := timelineEvents(plan, "http://127.0.0.1/token/")
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := events[0]["url"].(string); u != "http://127.0.0.1/token/event-0/asset/templates/shared/index.html" {
		t.Fatalf("inherited template url: %s", u)
	}

	prefix := "/token/"
	mux := http.NewServeMux()
	mux.HandleFunc(prefix, servePresentation(p, prefix, nil, nil))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base := srv.URL + prefix

	for _, name := range []string{"templates/shared/index.html", "templates/shared/style.css", "templates/shared/logo.png"} {
		resp, err := http.Get(base + "event-0/asset/" + name)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", name, resp.StatusCode)
		}
		if len(body) == 0 {
			t.Fatalf("%s: empty body", name)
		}
	}

	resp, err := http.Get(base + "event-0/asset/../backstage.json")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("escaped asset route: %d", resp.StatusCode)
	}

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.css"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	resp, err = http.Get(base + "event-0/asset/linked/secret.css")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("symlink escape served: %d", resp.StatusCode)
	}
}

func TestTimelineEventsRejectsOutsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	if err := os.Mkdir(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	p := &scene.Project{Dir: leaf, Workspace: ws}
	outside := filepath.Join(t.TempDir(), "index.html")
	if err := os.WriteFile(outside, []byte("<html></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := &Plan{
		Project:  p,
		Template: outside,
		Document: Document{Timeline: []Event{{At: 0, Layout: "single"}}},
	}
	if _, err := timelineEvents(plan, "http://127.0.0.1/token/"); err == nil {
		t.Fatal("outside template should fail")
	}

	plan.Template = ""
	plan.Scenes = map[string]*scene.Scene{"explain": {Entry: "../../outside.html"}}
	plan.Document.Timeline = []Event{{At: 0, Scene: "explain"}}
	if _, err := timelineEvents(plan, "http://127.0.0.1/token/"); err == nil {
		t.Fatal("escaping visual entry should fail")
	}
}

func TestPresentationInputsStayLeafRelativeWithoutExtends(t *testing.T) {
	p := writeFixture(t, Document{Version: 1, Duration: 2, Timeline: []Event{{Scene: "explain"}}})
	plan, err := Load(p, "show")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plan.Inputs["show.json"]; !ok {
		t.Fatalf("without extends, input keys should stay project-relative: %v", plan.Inputs)
	}
	events, err := timelineEvents(plan, "http://127.0.0.1/token/")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events: %d", len(events))
	}
	u, _ := events[0]["url"].(string)
	if !strings.Contains(u, "event-0/asset/visual.html") {
		t.Fatalf("visual url: %s", u)
	}
}

func TestMissingAllowedAssetRecordedEmpty(t *testing.T) {
	dir := t.TempDir()
	p := &scene.Project{Dir: dir}
	files := map[int]map[string]string{}
	var mu sync.Mutex
	h := servePresentation(p, "/t/", files, &mu)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/t/event-0/asset/later.css", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("css status %d", rec.Code)
	}
	path, ok := files[0]["later.css"]
	if !ok || path != "" {
		t.Fatalf("missing css: ok=%v path=%q files=%v", ok, path, files)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/t/event-0/asset/secret.bin", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bin status %d", rec.Code)
	}
	if _, ok := files[0]["secret.bin"]; ok {
		t.Fatal("disallowed extension entered the manifest")
	}
}

func TestUnreadableAssetRecordedEmpty(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read mode 0 files")
	}
	dir := t.TempDir()
	p := &scene.Project{Dir: dir}
	path := filepath.Join(dir, "later.css")
	if err := os.WriteFile(path, []byte("body{}"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if f, err := os.Open(path); err == nil {
		_ = f.Close()
		t.Skip("open succeeded despite mode 0")
	}
	files := map[int]map[string]string{}
	var mu sync.Mutex
	h := servePresentation(p, "/t/", files, &mu)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/t/event-0/asset/later.css", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	got, ok := files[0]["later.css"]
	if !ok || got != "" {
		t.Fatalf("unreadable css: ok=%v path=%q files=%v", ok, got, files)
	}
}
