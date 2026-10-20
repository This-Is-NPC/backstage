package production

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/This-Is-NPC/backstage/internal/encode"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

// The complete H.264 configuration must match before clips can be copied.
// In particular, matching dimensions alone does not make SPS/PPS compatible.
type videoStream struct {
	Codec          string `json:"codec_name"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	PixelFormat    string `json:"pix_fmt"`
	Rate           string `json:"r_frame_rate"`
	AverageRate    string `json:"avg_frame_rate"`
	TimeBase       string `json:"time_base"`
	Aspect         string `json:"sample_aspect_ratio"`
	Profile        string `json:"profile"`
	Level          int    `json:"level"`
	BFrames        int    `json:"has_b_frames"`
	ColorRange     string `json:"color_range"`
	ColorSpace     string `json:"color_space"`
	ColorTransfer  string `json:"color_transfer"`
	ColorPrimaries string `json:"color_primaries"`
	ExtraData      string `json:"extradata_hash"`
}

func probeVideo(ctx context.Context, path string) (videoStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	args := []string{"-v", "error", "-select_streams", "v:0", "-show_streams", "-show_data_hash", "sha256", "-of", "json", path}
	b, err := exec.CommandContext(ctx, "ffprobe", args...).CombinedOutput()
	if err != nil {
		return videoStream{}, fmt.Errorf("ffprobe %s: %w: %s", path, err, b)
	}
	var data struct {
		Streams []videoStream `json:"streams"`
	}
	if err := json.Unmarshal(b, &data); err != nil {
		return videoStream{}, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	if len(data.Streams) != 1 || data.Streams[0].Width <= 0 || data.Streams[0].Height <= 0 {
		return videoStream{}, fmt.Errorf("ffprobe %s: expected one video stream with positive dimensions", path)
	}
	return data.Streams[0], nil
}

func copyCompatible(streams []videoStream, w, h, fps int) bool {
	if len(streams) == 0 {
		return false
	}
	rate := strconv.Itoa(fps) + "/1"
	first := streams[0]
	for _, s := range streams {
		if s != first || s.Codec != "h264" || s.Width != w || s.Height != h ||
			s.PixelFormat != "yuv420p" || s.Rate != rate || s.AverageRate != rate ||
			s.ExtraData == "" || (s.Aspect != "1:1" && s.Aspect != "N/A") {
			return false
		}
	}
	return true
}

func normalizeClips(ctx context.Context, clips []string, dir string, cfg scene.RenderCfg, w, h, fps int, cleanup func()) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	streams := make([]videoStream, len(clips))
	for i, clip := range clips {
		if clip == "" {
			return nil, fmt.Errorf("segment %d produced no clip", i)
		}
		s, err := probeVideo(ctx, clip)
		if err != nil {
			return nil, err
		}
		streams[i] = s
	}
	copyVideo := copyCompatible(streams, w, h, fps)
	filter, threads := cfg.Threads.Filter, cfg.Threads.Encode
	encoder := encode.Encoder(cfg.Encoder)
	outputs := make([]string, len(clips))
	for i, clip := range clips {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out := filepath.Join(dir, fmt.Sprintf("n%03d.mp4", i))
		args := []string{"-v", "error", "-y", "-i", clip, "-map", "0:v:0", "-an"}
		if copyVideo {
			args = append(args, "-c:v", "copy")
		} else {
			vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%d,setsar=1", w, h, w, h, fps)
			if up := encoder.Upload(); up != "" {
				vf += "," + up
			}
			args = append(encoder.Init(), args...)
			args = append(args, "-vf", vf, "-filter_threads", strconv.Itoa(filter))
			quality := encoder.Args()
			if encoder != "vaapi" {
				quality = encoder.QualityArgs("medium", 23)
			}
			args = append(args, quality...)
			args = append(args, "-threads", strconv.Itoa(threads))
		}
		args = append(args, out)
		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := runProductionCommand(cmd, cleanup); err != nil {
			return nil, fmt.Errorf("normalize %s: %w: %s", clip, err, strings.TrimSpace(stderr.String()))
		}
		outputs[i] = out
	}

	return outputs, nil
}
