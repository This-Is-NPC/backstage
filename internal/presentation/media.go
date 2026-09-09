package presentation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, args...)
	scene.SetProcessGroup(c)
	c.Cancel = func() error {
		if c.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	c.WaitDelay = 3 * time.Second
	return c
}
func run(ctx context.Context, name string, args ...string) error {
	c := command(ctx, name, args...)
	var b bytes.Buffer
	c.Stderr = &b
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, b.String())
	}
	return nil
}
func num(n float64) string { return strconv.FormatFloat(n, 'f', 9, 64) }
func probe(path string) (Media, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b, err := command(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration:stream=codec_type", "-of", "json", path).Output()
	if err != nil {
		return Media{}, fmt.Errorf("probe %s: %w", path, err)
	}
	var v struct {
		Format  struct{ Duration string }
		Streams []struct {
			CodecType string `json:"codec_type"`
		}
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return Media{}, err
	}
	d, err := strconv.ParseFloat(v.Format.Duration, 64)
	if err != nil || !finite(d) || d <= 0 {
		return Media{}, fmt.Errorf("invalid media duration: %s", path)
	}
	m := Media{Path: path, Duration: d}
	for _, s := range v.Streams {
		m.HasAudio = m.HasAudio || s.CodecType == "audio"
		m.HasVideo = m.HasVideo || s.CodecType == "video"
	}
	return m, nil
}

type AudioPart struct {
	ID, Path                                              string
	At, From, To, Rate, Duration, Volume, FadeIn, FadeOut float64
	Loop                                                  bool
}

func (p *Plan) compileAudio() error {
	ids := map[string]bool{}
	for _, a := range p.Document.Audio {
		if a.ID == "" || ids[a.ID] {
			return fmt.Errorf("audio needs unique ID")
		}
		ids[a.ID] = true
		if a.Mode == "" {
			a.Mode = "independent"
		}
		if a.Mode != "independent" && a.Mode != "follow-video" {
			return fmt.Errorf("audio %q: invalid mode", a.ID)
		}
		if a.Rate == 0 {
			a.Rate = 1
		}
		if !finite(a.Rate) || a.Rate <= 0 || !validTime(a.At) || !validTime(a.From) || !validTime(a.To) || !validTime(a.FadeIn) || !validTime(a.FadeOut) {
			return fmt.Errorf("audio %q has invalid timing", a.ID)
		}
		volume := 1.0
		if a.Volume != nil {
			volume = *a.Volume
		}
		if !validTime(volume) {
			return fmt.Errorf("invalid audio volume")
		}
		path := a.File
		if a.Asset != "" {
			if path != "" || a.Cue != "" {
				return fmt.Errorf("audio must select file, cue or asset")
			}
			sceneName := a.Scene
			if sceneName == "" {
				if tr, ok := p.Tracks[a.Track]; ok {
					sceneName = p.Document.Sources[tr.Source].Scene
				}
			}
			if sceneName == "" {
				return fmt.Errorf("audio asset requires a scene")
			}
			owner, e := p.loadScene(sceneName)
			if e != nil {
				return e
			}
			asset, ok := owner.Audio[a.Asset]
			if !ok || asset.File == "" {
				return fmt.Errorf("unknown audio asset %q", a.Asset)
			}
			path = asset.File
		}
		offset := 0.0
		cueEnd := 0.0
		if a.Cue != "" {
			if path != "" {
				return fmt.Errorf("audio must select file or cue")
			}
			cue, err := p.cue(a.Scene, a.Track, a.Cue)
			if err != nil {
				return err
			}
			path = cue.Audio
			offset = cue.Start
			cueEnd = cue.End
			if path == "" {
				return fmt.Errorf("cue %q has no audio file", a.Cue)
			}
		}
		if path == "" {
			tr, ok := p.Tracks[a.Track]
			if !ok {
				return fmt.Errorf("audio %q requires file, cue or track", a.ID)
			}
			path = tr.Media.Path
		} else {
			var err error
			path, err = p.input(path)
			if err != nil {
				return err
			}
		}
		m, err := probe(path)
		if err != nil {
			return err
		}
		if !m.HasAudio {
			return fmt.Errorf("audio %q: input has no audio", a.ID)
		}
		if a.Mute {
			continue
		}
		add := func(part AudioPart) error {
			if part.At >= p.Document.Duration {
				return nil
			}
			if part.At+part.Duration > p.Document.Duration+1e-8 {
				if !a.TrimEnd && a.Mode == "independent" {
					return fmt.Errorf("audio %q exceeds presentation; set trim-end or cut explicitly", a.ID)
				}
				part.Duration = p.Document.Duration - part.At
				part.To = part.From + part.Duration*part.Rate
			}
			if part.Duration > 0 {
				part.ID = a.ID
				part.Path = path
				part.Volume = volume
				part.FadeIn = math.Min(a.FadeIn, part.Duration)
				part.FadeOut = math.Min(a.FadeOut, part.Duration)
				p.AudioParts = append(p.AudioParts, part)
			}
			return nil
		}
		if a.Mode == "follow-video" {
			tr, ok := p.Tracks[a.Track]
			if !ok {
				return fmt.Errorf("follow-video audio requires track")
			}
			if a.At != 0 || a.From != 0 || a.To != 0 || a.Rate != 1 || a.Loop {
				return fmt.Errorf("follow-video timing comes from the video segments")
			}
			end := offset + m.Duration
			if cueEnd > 0 {
				end = math.Min(end, cueEnd)
			}
			pos := tr.Start
			for _, s := range tr.Segments {
				lo, hi := math.Max(offset, s.From), math.Min(end, s.To)
				if hi > lo {
					if err = add(AudioPart{At: pos + (lo-s.From)/s.Rate, From: lo - offset, To: hi - offset, Rate: s.Rate, Duration: (hi - lo) / s.Rate}); err != nil {
						return err
					}
				}
				pos += (s.To - s.From) / s.Rate
			}
		} else {
			if a.To == 0 {
				a.To = m.Duration
			}
			if a.From >= a.To || a.To > m.Duration+0.001 {
				return fmt.Errorf("audio %q has invalid source interval", a.ID)
			}
			duration := (a.To - a.From) / a.Rate
			if a.Loop {
				duration = p.Document.Duration - a.At
			}
			if a.At >= p.Document.Duration {
				return fmt.Errorf("audio starts after presentation")
			}
			if err = add(AudioPart{At: a.At, From: a.From, To: a.To, Rate: a.Rate, Duration: duration, Loop: a.Loop}); err != nil {
				return err
			}
		}
	}
	return nil
}
func tempo(rate float64) string {
	var s []string
	for rate > 2 {
		s = append(s, "atempo=2")
		rate /= 2
	}
	for rate < 0.5 {
		s = append(s, "atempo=0.5")
		rate /= 0.5
	}
	return strings.Join(append(s, "atempo="+num(rate)), ",")
}
func (p *Plan) mix(ctx context.Context, dir string) (string, error) {
	if len(p.AudioParts) == 0 {
		return "", nil
	}
	args := []string{"-v", "error", "-y", "-threads", "1"}
	var paths []string
	for i, a := range p.AudioParts {
		path := filepath.Join(dir, fmt.Sprintf("audio-%d.wav", i))
		filter := "atrim=start=" + num(a.From) + ":end=" + num(a.To) + ",asetpts=PTS-STARTPTS," + tempo(a.Rate) + ",aresample=48000,aformat=channel_layouts=stereo"
		if err := run(ctx, "ffmpeg", "-v", "error", "-y", "-i", a.Path, "-vn", "-af", filter, "-c:a", "pcm_s16le", path); err != nil {
			return "", err
		}
		if a.Loop {
			args = append(args, "-stream_loop", "-1")
		}
		args = append(args, "-i", path)
		paths = append(paths, path)
	}
	var filters []string
	var inputs strings.Builder
	for i, a := range p.AudioParts {
		delay := int64(math.Round(a.At * 48000))
		label := fmt.Sprintf("a%d", i)
		f := fmt.Sprintf("[%d:a]asetpts=N/SR/TB,atrim=duration=%s,asetpts=PTS-STARTPTS,volume=%s", i, num(a.Duration), num(a.Volume))
		if a.FadeIn > 0 {
			f += ",afade=t=in:st=0:d=" + num(a.FadeIn)
		}
		if a.FadeOut > 0 {
			f += ",afade=t=out:st=" + num(a.Duration-a.FadeOut) + ":d=" + num(a.FadeOut)
		}
		f += fmt.Sprintf(",adelay=%dS:all=1[%s]", delay, label)
		filters = append(filters, f)
		fmt.Fprintf(&inputs, "[%s]", label)
	}
	filters = append(filters, fmt.Sprintf("%samix=inputs=%d:normalize=0,alimiter=limit=0.95:level=0:latency=1,apad,atrim=duration=%s[mix]", inputs.String(), len(paths), num(p.Document.Duration)))
	path := filepath.Join(dir, "mix.wav")
	args = append(args, "-filter_complex_threads", "1", "-filter_complex", strings.Join(filters, ";"), "-map", "[mix]", "-ar", "48000", "-ac", "2", "-c:a", "pcm_s16le", "-t", num(p.Document.Duration), path)
	return path, run(ctx, "ffmpeg", args...)
}

// Prepare each selected track once. FFV1 retains frames without storing PNG sequences.
func prepareTrack(ctx context.Context, t CompiledTrack, dir, id string, fps int) (string, error) {
	var filters []string
	var labels strings.Builder
	for i, s := range t.Segments {
		label := fmt.Sprintf("s%d", i)
		filters = append(filters, fmt.Sprintf("[0:v]trim=start=%s:end=%s,setpts=(PTS-STARTPTS)/%s,setsar=1[%s]", num(s.From), num(s.To), num(s.Rate), label))
		fmt.Fprintf(&labels, "[%s]", label)
	}
	filters = append(filters, fmt.Sprintf("%sconcat=n=%d:v=1:a=0,fps=%d[v]", labels.String(), len(t.Segments), fps))
	out := filepath.Join(dir, id+".mkv")
	err := run(ctx, "ffmpeg", "-v", "error", "-y", "-threads", "1", "-i", t.Media.Path, "-filter_complex_threads", "1", "-filter_complex", strings.Join(filters, ";"), "-map", "[v]", "-an", "-c:v", "ffv1", "-threads", "1", out)
	return out, err
}

// Decoder has bounded memory and reads exactly one PNG at a time from FFmpeg.
type decoder struct {
	cmd    *exec.Cmd
	reader io.ReadCloser
	stderr bytes.Buffer
	frame  int
	last   []byte
	done   bool
}

func newDecoder(ctx context.Context, path string) (*decoder, error) {
	d := &decoder{frame: -1}
	d.cmd = command(ctx, "ffmpeg", "-v", "error", "-threads", "1", "-i", path, "-an", "-threads", "1", "-f", "image2pipe", "-c:v", "png", "-")
	d.cmd.Stderr = &d.stderr
	var err error
	d.reader, err = d.cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = d.cmd.Start(); err != nil {
		return nil, err
	}
	return d, nil
}
func readPNG(r io.Reader) ([]byte, error) {
	var b bytes.Buffer
	head := make([]byte, 8)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, err
	}
	if !bytes.Equal(head, []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		return nil, fmt.Errorf("invalid PNG stream")
	}
	b.Write(head)
	for {
		h := make([]byte, 8)
		if _, err := io.ReadFull(r, h); err != nil {
			return nil, err
		}
		n := uint32(h[0])<<24 | uint32(h[1])<<16 | uint32(h[2])<<8 | uint32(h[3])
		if n > 128<<20 {
			return nil, fmt.Errorf("oversized PNG chunk")
		}
		b.Write(h)
		if _, err := io.CopyN(&b, r, int64(n)+4); err != nil {
			return nil, err
		}
		if string(h[4:]) == "IEND" {
			return b.Bytes(), nil
		}
	}
}
func (d *decoder) get(frame int) ([]byte, error) {
	for d.frame < frame && !d.done {
		b, err := readPNG(d.reader)
		if err == io.EOF {
			d.done = true
			if e := d.cmd.Wait(); e != nil {
				return nil, fmt.Errorf("decode: %w: %s", e, d.stderr.String())
			}
			break
		}
		if err != nil {
			return nil, err
		}
		d.frame++
		d.last = b
	}
	if d.last == nil {
		return nil, fmt.Errorf("decoder returned no frames")
	}
	return d.last, nil
}
func (d *decoder) close() {
	_ = d.reader.Close()
	if !d.done {
		if d.cmd.Process != nil {
			_ = d.cmd.Process.Kill()
		}
		_ = d.cmd.Wait()
	}
}
