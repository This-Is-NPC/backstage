package presentation

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

func TestRemotionExportRetainsMediaClocksAndMappedEvidence(t *testing.T) {
	requireRenderTest(t)
	p := layerSlotPlan(t, 320, 180, 10, 2, "single", replaceFit(layerFillHTML, "contain"), nil)
	defer p.Close()
	tr := p.Tracks["cam"]
	tr.Start = .3
	tr.Segments = []Segment{{To: 1, Rate: 2}, {To: .5, Rate: .5}}
	tr.Duration = segmentDuration(tr.Segments)
	tr.Camera = Camera{Keyframes: []CameraKeyframe{{At: .4, Region: Region{X: .1, Y: .1, W: .8, H: .8}, Ease: "cut"}}}
	p.Tracks["cam"] = tr
	p.Tracks["copy"] = tr
	fractional := tr
	fractional.Start = .35
	fractional.Segments = []Segment{{To: 1, Rate: 2}, {To: .51, Rate: .5}}
	fractional.Duration = segmentDuration(fractional.Segments)
	fractional.OnEnd = "hide"
	p.Tracks["fractional"] = fractional
	audioPath := filepath.Join(p.Project.Dir, "voice.wav")
	if err := run(context.Background(), "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=1.5", "-c:a", "pcm_s16le", audioPath); err != nil {
		t.Fatal(err)
	}
	p.AudioParts = []AudioPart{{ID: "voice", Path: audioPath, From: 0, To: 1.5, Rate: 1, At: .25, Duration: 1.5, Volume: 1, Narration: true}}
	p.Document.Sources["cam"] = Source{Scene: "explain"}
	p.Scenes["explain"] = &scene.Scene{Narration: scene.Narration{Cues: []scene.NarrationCue{{ID: "why", Start: .2, End: .4, Text: "A component driven by recording evidence"}}}}
	if err := facts.Write(facts.Path(tr.Media.Path), facts.Facts{Steps: []facts.Step{{ID: "save", Action: "keys", Start: .2, End: .4, Keys: []string{"ctrl+s"}}}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := ExportRemotion(ctx, p, "exports/react", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(out, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	var data bridgeBundle
	if err = json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	track := data.Tracks["cam"]
	if f := data.Tracks["fractional"]; f.From != 4 || f.End != 19 {
		t.Fatalf("fractional visibility bounds: %+v", f)
	}
	if c := data.Document.Tracks["cam"].Camera; len(c.Keyframes) != 1 || c.Keyframes[0].At != .4 || data.Document.Tracks["cam"].Start != .3 {
		t.Fatal("export lost compiled track timing or camera metadata")
	}
	if data.Version != 1 || data.Frames != 20 || track.Frames != 15 || track.From != 3 || len(track.Steps) != 2 || math.Abs(track.Steps[0].Start-.4) > 1e-9 || math.Abs(track.Steps[1].Start-1.2) > 1e-9 || len(track.Cues) != 2 || track.Cues[1].Start != track.Steps[1].Start {
		t.Fatalf("bundle=%+v, track=%+v", data, track)
	}
	if track.Original != data.Tracks["copy"].Original || track.Facts != data.Tracks["copy"].Facts {
		t.Fatal("shared take assets were duplicated")
	}
	if data.Audio == "" {
		t.Fatal("export omitted mixed audio")
	}
	mix, err := probe(filepath.Join(out, "public", data.Audio))
	if err != nil || !mix.HasAudio || math.Abs(mix.Duration-2) > .01 {
		t.Fatalf("exported mix: %+v: %v", mix, err)
	}
	prepared, err := prepareTrack(ctx, tr, t.TempDir(), "reference", 10, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertStaticFadePTS(t, filepath.Join(out, "public", track.Media), 15, 10)
	assertStaticFadeQuality(t, prepared, filepath.Join(out, "public", track.Media))
	// Studio uses the browser's video decoder, while offline Remotion rendering
	// uses FFmpeg. Verify both consumers accept the lossless exported stream.
	browser, err := dependencies()
	if err != nil {
		t.Fatal(err)
	}
	videoURL := (&url.URL{Scheme: "file", Path: filepath.Join(out, "public", track.Media)}).String()
	page := filepath.Join(t.TempDir(), "video.html")
	markup := `<!doctype html><video id="video" src="` + html.EscapeString(videoURL) + `"></video><script>video.onloadeddata=()=>document.body.append('VIDEO_READY');video.onerror=()=>document.body.append('VIDEO_ERROR')</script>`
	if err = os.WriteFile(page, []byte(markup), 0o600); err != nil {
		t.Fatal(err)
	}
	dom, err := command(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--allow-file-access-from-files", "--user-data-dir="+t.TempDir(), "--virtual-time-budget=2000", "--dump-dom", (&url.URL{Scheme: "file", Path: page}).String()).Output()
	if err != nil || !strings.Contains(string(dom), "VIDEO_READY</body>") {
		t.Fatalf("browser decode: %v: %s", err, dom)
	}
	for _, name := range []string{"index.jsx", "stage.jsx", "package.json", "README.md", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ExportRemotion(ctx, p, "exports/react", io.Discard); err == nil {
		t.Fatal("overwrote an existing export")
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err := ExportRemotion(ctx, p, "exports/cancelled", io.Discard); err == nil {
		t.Fatal("published a cancelled export")
	}
	entries, err := os.ReadDir(filepath.Dir(out))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".remotion-") || entry.Name() == "cancelled" {
			t.Fatal("partial export survived cancellation")
		}
	}
}
