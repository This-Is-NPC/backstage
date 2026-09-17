package presentation

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServePreview(t *testing.T) {
	video := filepath.Join(t.TempDir(), "preview.mp4")
	if err := os.WriteFile(video, []byte("mp4-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	addr, stop, err := servePreview(previewPlan(8, 10), PreviewOpts{From: 0.22, To: 2, Scale: 1}, video)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	res, err := http.Get(addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	if !strings.Contains(html, "from=0.3") {
		t.Fatalf("clock: %s", html)
	}
	if strings.Contains(html, "from=0.22") {
		t.Fatalf("used opts.From: %s", html)
	}
}

func TestPreviewPlayer(t *testing.T) {
	video := filepath.Join(t.TempDir(), "preview.mp4")
	if err := os.WriteFile(video, []byte("mp4-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := previewPlayer(previewPlan(8, 10), PreviewOpts{From: 0.22, To: 2, Scale: 1}, video)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	if !strings.Contains(html, "from=0.3") {
		t.Fatalf("clock: %s", html)
	}
	if strings.Contains(html, "from=0.22") {
		t.Fatalf("used opts.From: %s", html)
	}
}

func TestPreviewHandler(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "preview.mp4")
	if err := os.WriteFile(video, []byte("mp4-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(previewHandler("Backstage preview 4–6 s ×0.5", 12, 4, video))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	if res.StatusCode != http.StatusOK {
		t.Fatal(res.Status)
	}
	if !strings.Contains(html, `<output id="time"></output>`) {
		t.Fatalf("output: %s", html)
	}
	if strings.Contains(html, "github.com") || strings.Contains(html, "internal/scene") {
		t.Fatalf("stray import in HTML: %s", html)
	}
	if !strings.Contains(html, "<title>Backstage preview 4–6 s ×0.5</title>") {
		t.Fatal(html)
	}
	if !strings.Contains(html, "fps=12") || !strings.Contains(html, "from=4") {
		t.Fatal(html)
	}

	player, err := previewPlayer(previewPlan(8, 10), PreviewOpts{From: 0.22, To: 2, Scale: 1}, video)
	if err != nil {
		t.Fatal(err)
	}
	clock := httptest.NewServer(player)
	t.Cleanup(clock.Close)
	res, err = http.Get(clock.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	html = string(body)
	if !strings.Contains(html, "fps=10") || !strings.Contains(html, "from=0.3") {
		t.Fatalf("clock: %s", html)
	}
	if strings.Contains(html, "from=0.22") {
		t.Fatalf("used opts.From: %s", html)
	}

	res, err = http.Get(srv.URL + "/preview.mp4")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || string(got) != "mp4-bytes" {
		t.Fatalf("video %d %q", res.StatusCode, got)
	}

	res, err = http.Get(srv.URL + "/other")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("other %d", res.StatusCode)
	}
}
