package presentation

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

//go:embed remotion/*
var remotionTemplate embed.FS

type bridgeStep struct {
	facts.Step
	SourceStart float64 `json:"source-start"`
	SourceEnd   float64 `json:"source-end"`
	Rate        float64 `json:"rate"`
}

type bridgeCue struct {
	scene.NarrationCue
	SourceStart float64 `json:"source-start"`
	SourceEnd   float64 `json:"source-end"`
	Rate        float64 `json:"rate"`
}

type bridgeTrack struct {
	Media    string       `json:"media"`
	Original string       `json:"original"`
	Facts    string       `json:"facts,omitempty"`
	From     int          `json:"from"`
	End      int          `json:"end"`
	Frames   int          `json:"frames"`
	OnEnd    string       `json:"on-end"`
	Steps    []bridgeStep `json:"steps"`
	Cues     []bridgeCue  `json:"cues"`
	Segments []Segment    `json:"segments"`
}

type bridgeBundle struct {
	Version  int                    `json:"version"`
	Name     string                 `json:"name"`
	Width    int                    `json:"width"`
	Height   int                    `json:"height"`
	FPS      int                    `json:"fps"`
	Frames   int                    `json:"frames"`
	Audio    string                 `json:"audio,omitempty"`
	Theme    Theme                  `json:"theme"`
	Document Document               `json:"document"`
	Text     []TextSpan             `json:"text"`
	Actions  []ActionSpan           `json:"actions"`
	Tracks   map[string]bridgeTrack `json:"tracks"`
}

// ExportRemotion publishes a portable React project only after all media and
// metadata are complete. Existing exports are never replaced.
func ExportRemotion(ctx context.Context, p *Plan, out string, progress io.Writer) (string, error) {
	if out == "" {
		out = filepath.Join("exports", p.Name+"-remotion")
	}
	dest, err := p.Project.OutputPath(out)
	if err != nil {
		return "", err
	}
	if err = p.checkOutput(dest); err != nil {
		return "", err
	}
	if _, err = os.Lstat(dest); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = fmt.Errorf("remotion export already exists: %s", dest)
		}
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	work, err := os.MkdirTemp(filepath.Dir(dest), ".remotion-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	media := filepath.Join(work, "public", "media")
	if err = os.MkdirAll(media, 0o755); err != nil {
		return "", err
	}
	prep, err := os.MkdirTemp(work, ".prepare-")
	if err != nil {
		return "", err
	}
	paths, _, err := prepareTracks(ctx, p, prep, progress, nil)
	if err != nil {
		return "", err
	}
	bundle := bridgeBundle{Version: 1, Name: p.Name, Width: p.Width, Height: p.Height, FPS: p.FPS, Frames: p.Frames, Theme: p.Document.theme(), Document: p.Document, Text: p.Text, Actions: p.Actions, Tracks: map[string]bridgeTrack{}}
	bundle.Document.Tracks = make(map[string]Track, len(p.Tracks))
	// Shared takes and facts are copied once, even when several tracks reuse them.
	copied := map[string]string{}
	copyAsset := func(path string) (string, error) {
		if rel, ok := copied[path]; ok {
			return rel, nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		rel := "media/source-" + strconv.Itoa(len(copied)) + filepath.Ext(path)
		if err := copyRegular(path, filepath.Join(work, "public", filepath.FromSlash(rel)), 0o644); err != nil {
			return "", err
		}
		copied[path] = rel
		return rel, nil
	}
	for i, id := range sortedKeys(p.Tracks) {
		tr := p.Tracks[id]
		bundle.Document.Tracks[id] = tr.Track
		rel := fmt.Sprintf("media/track-%d.mp4", i)
		// Reuse Backstage's exact cut/rate preparation. Lossless H.264 makes its
		// FFV1 frames portable to Chromium without introducing another retimer.
		if err = run(ctx, "ffmpeg", "-v", "error", "-y", "-i", paths[id], "-an", "-c:v", "libx264", "-crf", "0", "-preset", "ultrafast", "-threads", "1", "-movflags", "+faststart", filepath.Join(work, "public", filepath.FromSlash(rel))); err != nil {
			return "", err
		}
		n, err := countPackets(ctx, filepath.Join(work, "public", filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		original, err := copyAsset(tr.Media.Path)
		if err != nil {
			return "", err
		}
		bt := bridgeTrack{Media: rel, Original: original, From: int(math.Ceil(tr.Start*float64(p.FPS) - 1e-9)), End: int(math.Ceil((tr.Start+tr.Duration)*float64(p.FPS) - 1e-9)), Frames: n, OnEnd: tr.OnEnd, Segments: tr.Segments, Steps: []bridgeStep{}, Cues: []bridgeCue{}}
		if _, err = os.Stat(facts.Path(tr.Media.Path)); err == nil {
			recorded, err := p.recordedFacts(id)
			if err != nil {
				return "", err
			}
			bt.Facts, err = copyAsset(facts.Path(tr.Media.Path))
			if err != nil {
				return "", err
			}
			for _, step := range recorded.Steps {
				for _, span := range mapSpan(tr, step.Start, step.End) {
					if span.At >= p.Document.Duration {
						continue
					}
					mapped := step
					mapped.Start, mapped.End = span.At, min(span.End, p.Document.Duration)
					bt.Steps = append(bt.Steps, bridgeStep{mapped, span.SourceStart, span.SourceStart + (mapped.End-span.At)*span.Rate, span.Rate})
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if src := p.Document.Sources[tr.Source]; src.Scene != "" {
			s, err := p.loadScene(src.Scene)
			if err != nil {
				return "", err
			}
			for _, cue := range s.Narration.Cues {
				for _, span := range mapSpan(tr, cue.Start, cue.End) {
					if span.At >= p.Document.Duration {
						continue
					}
					mapped := cue
					mapped.Start, mapped.End = span.At, min(span.End, p.Document.Duration)
					bt.Cues = append(bt.Cues, bridgeCue{mapped, span.SourceStart, span.SourceStart + (mapped.End-span.At)*span.Rate, span.Rate})
				}
			}
		}
		bundle.Tracks[id] = bt
	}
	audio, _, err := p.mix(ctx, prep, nil)
	if err != nil {
		return "", err
	}
	if audio != "" {
		bundle.Audio, err = copyAsset(audio)
		if err != nil {
			return "", err
		}
	}
	if err = os.RemoveAll(prep); err != nil {
		return "", err
	}
	files, err := remotionTemplate.ReadDir("remotion")
	if err != nil {
		return "", err
	}
	for _, file := range files {
		body, err := remotionTemplate.ReadFile("remotion/" + file.Name())
		if err != nil {
			return "", err
		}
		if err = os.WriteFile(filepath.Join(work, file.Name()), body, 0o644); err != nil {
			return "", err
		}
	}
	body, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(work, "backstage.json"), append(body, '\n'), 0o644); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = os.Rename(work, dest); err != nil {
		return "", err
	}
	return dest, nil
}
