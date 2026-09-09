// Package presentation compiles and renders existing media without staging guests.
package presentation

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

type Source struct {
	Scene string `json:"scene,omitempty"`
	File  string `json:"file,omitempty"`
}
type Segment struct {
	From float64 `json:"from"`
	To   float64 `json:"to"`
	Rate float64 `json:"rate"`
}
type Track struct {
	Source   string    `json:"source"`
	Start    float64   `json:"start"`
	Segments []Segment `json:"segments,omitempty"`
	OnEnd    string    `json:"on-end,omitempty"`
}
type Transition struct {
	Effect   string  `json:"effect"`
	Duration float64 `json:"duration"`
}
type Event struct {
	At         float64           `json:"at"`
	Layout     string            `json:"layout,omitempty"`
	Slots      map[string]string `json:"slots,omitempty"`
	Scene      string            `json:"scene,omitempty"`
	Transition Transition        `json:"transition,omitempty"`
}

// Audio selects a file, a track's original sound, or one narration cue.
// Independent audio uses At/From/To/Rate; follow-video uses the track's cuts.
type Audio struct {
	Asset   string   `json:"asset,omitempty"`
	ID      string   `json:"id"`
	File    string   `json:"file,omitempty"`
	Track   string   `json:"track,omitempty"`
	Scene   string   `json:"scene,omitempty"`
	Cue     string   `json:"cue,omitempty"`
	Mode    string   `json:"mode,omitempty"`
	At      float64  `json:"at,omitempty"`
	From    float64  `json:"from,omitempty"`
	To      float64  `json:"to,omitempty"`
	Rate    float64  `json:"rate,omitempty"`
	Volume  *float64 `json:"volume,omitempty"`
	Mute    bool     `json:"mute,omitempty"`
	FadeIn  float64  `json:"fade-in,omitempty"`
	FadeOut float64  `json:"fade-out,omitempty"`
	Loop    bool     `json:"loop,omitempty"`
	TrimEnd bool     `json:"trim-end,omitempty"`
}
type Caption struct {
	Scene    string  `json:"scene,omitempty"`
	Cue      string  `json:"cue"`
	Track    string  `json:"track,omitempty"`
	Audio    string  `json:"audio,omitempty"`
	Clock    string  `json:"clock"`
	At       float64 `json:"at,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	Slot     string  `json:"slot"`
}
type Document struct {
	Version    int               `json:"version"`
	Duration   float64           `json:"duration"`
	Render     scene.RenderCfg   `json:"render,omitempty"`
	Parameters map[string]any    `json:"parameters,omitempty"`
	Sources    map[string]Source `json:"sources"`
	Tracks     map[string]Track  `json:"tracks"`
	Timeline   []Event           `json:"timeline"`
	Audio      []Audio           `json:"audio,omitempty"`
	Captions   []Caption         `json:"captions,omitempty"`
}
type Media struct {
	Path               string
	Duration           float64
	HasVideo, HasAudio bool
}
type CompiledTrack struct {
	Track
	Media    Media
	Duration float64
}
type TextSpan struct {
	At   float64 `json:"at"`
	End  float64 `json:"end"`
	Text string  `json:"text"`
	Slot string  `json:"slot"`
}
type Plan struct {
	Project                    *scene.Project
	Name                       string
	Ref                        scene.PresentationRef
	Document                   Document
	Tracks                     map[string]CompiledTrack
	Scenes                     map[string]*scene.Scene
	Inputs                     map[string]string
	Text                       []TextSpan
	AudioParts                 []AudioPart
	Width, Height, FPS, Frames int
	Template                   string
}

func strictRead(path string, into any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err = d.Decode(into); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("%s: expected one JSON document", path)
	}
	return nil
}
func finite(n float64) bool    { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func validTime(n float64) bool { return finite(n) && n >= 0 }
func rat(n float64) *big.Rat   { r, _ := new(big.Rat).SetString(fmt.Sprintf("%.15g", n)); return r }
func segmentDuration(segments []Segment) float64 {
	sum := new(big.Rat)
	for _, s := range segments {
		sum.Add(sum, new(big.Rat).Quo(new(big.Rat).Sub(rat(s.To), rat(s.From)), rat(s.Rate)))
	}
	n, _ := sum.Float64()
	return n
}
func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func (p *Plan) input(rel string) (string, error) {
	path, err := p.Project.SafePath(rel)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file: %s", rel)
	}
	p.Inputs[rel] = path
	return path, nil
}
func (p *Plan) loadScene(name string) (*scene.Scene, error) {
	if s := p.Scenes[name]; s != nil {
		return s, nil
	}
	path, err := p.Project.ScenePathSafe(name)
	if err != nil {
		return nil, err
	}
	s, err := scene.LoadScene(path)
	if err != nil {
		return nil, err
	}
	if err = s.Validate(p.Project); err != nil {
		return nil, err
	}
	p.Scenes[name] = s
	rel, _ := filepath.Rel(p.Project.Dir, path)
	p.Inputs[rel] = path
	if s.Type == "visual" {
		if _, err = p.input(s.Entry); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Load resolves all media and editorial timing. It never calls the recording engine.
func Load(p *scene.Project, name string) (*Plan, error) {
	copyProject := *p
	root, rootErr := filepath.Abs(p.Dir)
	if rootErr != nil {
		return nil, rootErr
	}
	copyProject.Dir = root
	p = &copyProject

	ref, ok := p.Presentations[name]
	if !ok {
		return nil, fmt.Errorf("unknown presentation %q", name)
	}
	plan := &Plan{Project: p, Name: name, Ref: ref, Tracks: map[string]CompiledTrack{}, Scenes: map[string]*scene.Scene{}, Inputs: map[string]string{}}
	path, err := plan.input(ref.File)
	if err != nil {
		return nil, err
	}
	if err = strictRead(path, &plan.Document); err != nil {
		return nil, err
	}
	d := &plan.Document
	if d.Version != 1 || !finite(d.Duration) || d.Duration <= 0 {
		return nil, fmt.Errorf("presentation requires version 1 and positive duration")
	}
	plan.Width = d.Render.W
	if plan.Width == 0 {
		plan.Width = p.Render.W
	}
	if plan.Width == 0 {
		plan.Width = 1920
	}
	plan.Height = d.Render.H
	if plan.Height == 0 {
		plan.Height = p.Render.H
	}
	if plan.Height == 0 {
		plan.Height = 1080
	}
	plan.FPS = d.Render.FPS
	if plan.FPS == 0 {
		plan.FPS = p.Render.FPS
	}
	if plan.FPS == 0 {
		plan.FPS = 30
	}
	if plan.Width < 2 || plan.Height < 2 || plan.Width%2 != 0 || plan.Height%2 != 0 || plan.Width > 8192 || plan.Height > 8192 || plan.FPS < 1 || plan.FPS > 120 {
		return nil, fmt.Errorf("render needs even dimensions 2..8192 and fps 1..120")
	}
	if d.Duration > 86400 {
		return nil, fmt.Errorf("presentation duration exceeds 24 hours")
	}
	plan.Frames = int(math.Ceil(d.Duration*float64(plan.FPS) - 1e-9))
	if ref.Template != "" {
		t, ok := p.Templates[ref.Template]
		if !ok {
			return nil, fmt.Errorf("unknown template %q", ref.Template)
		}
		plan.Template, err = plan.input(t.Entry)
		if err != nil {
			return nil, err
		}
	}
	sources := map[string]Media{}
	for _, id := range sortedKeys(d.Sources) {
		src := d.Sources[id]
		if id == "" {
			return nil, fmt.Errorf("source ID cannot be empty")
		}
		file := src.File
		if src.Scene != "" {
			s, e := plan.loadScene(src.Scene)
			if e != nil {
				return nil, e
			}
			if s.Type == "visual" {
				return nil, fmt.Errorf("visual scene %q belongs in timeline", src.Scene)
			}
			if file == "" {
				file = filepath.Join(p.Record.Out, s.Name+".mp4")
			}
		}
		if file == "" {
			return nil, fmt.Errorf("source %q requires file or scene", id)
		}
		path, e := plan.input(file)
		if e != nil {
			return nil, e
		}
		media, e := probe(path)
		if e != nil {
			return nil, e
		}
		if !media.HasVideo {
			return nil, fmt.Errorf("source %q has no video", id)
		}
		sources[id] = media
	}
	for _, id := range sortedKeys(d.Tracks) {
		tr := d.Tracks[id]
		m, ok := sources[tr.Source]
		if !ok || id == "" {
			return nil, fmt.Errorf("track %q has unknown source", id)
		}
		if !validTime(tr.Start) || tr.Start >= d.Duration {
			return nil, fmt.Errorf("track %q has invalid start", id)
		}
		if tr.OnEnd == "" {
			tr.OnEnd = "freeze"
		}
		if tr.OnEnd != "freeze" && tr.OnEnd != "hide" {
			return nil, fmt.Errorf("track %q: on-end must be freeze or hide", id)
		}
		if len(tr.Segments) == 0 {
			tr.Segments = []Segment{{To: m.Duration, Rate: 1}}
		}
		for i := range tr.Segments {
			s := &tr.Segments[i]
			if s.Rate == 0 {
				s.Rate = 1
			}
			if !validTime(s.From) || !finite(s.To) || s.To <= s.From || s.To > m.Duration+0.001 || !finite(s.Rate) || s.Rate <= 0 {
				return nil, fmt.Errorf("track %q: invalid segment %d", id, i)
			}
		}
		plan.Tracks[id] = CompiledTrack{Track: tr, Media: m, Duration: segmentDuration(tr.Segments)}
	}
	if len(d.Timeline) == 0 || d.Timeline[0].At != 0 {
		return nil, fmt.Errorf("timeline must start at zero")
	}
	for i, e := range d.Timeline {
		end := d.Duration
		if i+1 < len(d.Timeline) {
			end = d.Timeline[i+1].At
		}
		if !validTime(e.At) || e.At >= end || end > d.Duration {
			return nil, fmt.Errorf("invalid timeline event %d", i)
		}
		if (e.Layout == "") == (e.Scene == "") {
			return nil, fmt.Errorf("event %d requires exactly one layout or scene", i)
		}
		if e.Scene != "" {
			s, er := plan.loadScene(e.Scene)
			if er != nil {
				return nil, er
			}
			if s.Type != "visual" || end-e.At > s.Duration+1e-9 || len(e.Slots) > 0 {
				return nil, fmt.Errorf("event %d needs a visual scene with sufficient duration and no slots", i)
			}
		}
		seenTracks := map[string]bool{}
		for slot, tr := range e.Slots {
			if seenTracks[tr] {
				return nil, fmt.Errorf("track %q occupies multiple slots; declare separate track instances", tr)
			}
			seenTracks[tr] = true
			if slot == "" {
				return nil, fmt.Errorf("empty slot")
			}
			if _, ok := plan.Tracks[tr]; !ok {
				return nil, fmt.Errorf("unknown track %q", tr)
			}
		}
		t := e.Transition
		if !validTime(t.Duration) || e.At+t.Duration > end {
			return nil, fmt.Errorf("transition overlaps next event")
		}
		if t.Effect != "" && t.Effect != "fade" && t.Effect != "morph" {
			return nil, fmt.Errorf("unknown transition %q", t.Effect)
		}
		if (t.Effect == "") != (t.Duration == 0) || (i == 0 && t.Duration > 0) {
			return nil, fmt.Errorf("transition needs effect and positive duration, after first event")
		}
		if t.Effect == "morph" && (e.Scene != "" || d.Timeline[i-1].Scene != "") {
			return nil, fmt.Errorf("morph requires two layouts; use fade for visual scenes")
		}
	}
	if err = plan.compileAudio(); err != nil {
		return nil, err
	}
	if err = plan.compileCaptions(); err != nil {
		return nil, err
	}
	return plan, nil
}

func (p *Plan) cue(sceneName, track, cueID string) (scene.NarrationCue, error) {
	if sceneName == "" && track != "" {
		tr, ok := p.Tracks[track]
		if !ok {
			return scene.NarrationCue{}, fmt.Errorf("unknown track %q", track)
		}
		sceneName = p.Document.Sources[tr.Source].Scene
	}
	if sceneName == "" {
		return scene.NarrationCue{}, fmt.Errorf("cue %q requires a scene", cueID)
	}
	s, err := p.loadScene(sceneName)
	if err != nil {
		return scene.NarrationCue{}, err
	}
	for _, c := range s.Narration.Cues {
		if c.ID == cueID {
			return c, nil
		}
	}
	return scene.NarrationCue{}, fmt.Errorf("unknown cue %q in scene %q", cueID, sceneName)
}

// mapSpan intersects a source interval with each selected segment, including repeats.
func mapSpan(t CompiledTrack, start, end float64) [][3]float64 {
	var spans [][3]float64
	elapsed := rat(t.Start)
	pos := t.Start
	for _, s := range t.Segments {
		a, b := math.Max(start, s.From), math.Min(end, s.To)
		if b > a {
			spans = append(spans, [3]float64{pos + (a-s.From)/s.Rate, pos + (b-s.From)/s.Rate, s.Rate})
		}
		elapsed.Add(elapsed, new(big.Rat).Quo(new(big.Rat).Sub(rat(s.To), rat(s.From)), rat(s.Rate)))
		pos, _ = elapsed.Float64()
	}
	return spans
}
func (p *Plan) compileCaptions() error {
	for _, c := range p.Document.Captions {
		cue, err := p.cue(c.Scene, c.Track, c.Cue)
		if err != nil {
			return err
		}
		if c.Slot == "" {
			return fmt.Errorf("caption needs slot")
		}
		var spans [][2]float64
		switch c.Clock {
		case "source":
			tr, ok := p.Tracks[c.Track]
			if !ok {
				return fmt.Errorf("source caption requires track")
			}
			for _, s := range mapSpan(tr, cue.Start, cue.End) {
				spans = append(spans, [2]float64{s[0], s[1]})
			}
		case "presentation":
			if !validTime(c.At) || !finite(c.Duration) || c.Duration <= 0 {
				return fmt.Errorf("caption requires at and positive duration")
			}
			spans = append(spans, [2]float64{c.At, c.At + c.Duration})
		case "audio":
			found := false
			for _, a := range p.AudioParts {
				if a.ID == c.Audio {
					found = true
					spans = append(spans, [2]float64{a.At, a.At + a.Duration})
				}
			}
			if !found {
				return fmt.Errorf("caption references unknown or muted audio %q", c.Audio)
			}
		default:
			return fmt.Errorf("caption clock must be source, presentation or audio")
		}
		for _, s := range spans {
			if s[0] >= p.Document.Duration {
				continue
			}
			end := math.Min(s[1], p.Document.Duration)
			if c.Clock == "presentation" && s[1] > p.Document.Duration {
				return fmt.Errorf("caption exceeds presentation")
			}
			p.Text = append(p.Text, TextSpan{At: s[0], End: end, Text: cue.Text, Slot: c.Slot})
		}
	}
	return nil
}

func (p *Plan) Output(override string) (string, error) {
	out := override
	if out == "" {
		out = p.Ref.Out
	}
	if out == "" {
		out = filepath.Join("exports", p.Name+".mp4")
	}
	path, err := p.Project.SafePath(out)
	if err != nil {
		return "", err
	}
	if strings.ToLower(filepath.Ext(path)) != ".mp4" {
		return "", fmt.Errorf("output must be .mp4")
	}
	for _, input := range p.Inputs {
		if input == path {
			return "", fmt.Errorf("output would overwrite input")
		}
	}
	return path, nil
}
