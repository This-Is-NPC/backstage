package presentation

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRenderProgressIsRaceFree(t *testing.T) {
	requireRenderTest(t)
	useTempCache(t)
	plan := multiTrackPlan(t)
	if len(plan.Tracks) < 2 {
		t.Fatal("need two tracks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var buf bytes.Buffer
	if err := Render(ctx, plan, filepath.Join(t.TempDir(), "show.mp4"), &buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(">> render timings:")) {
		t.Fatal(buf.String())
	}
}
