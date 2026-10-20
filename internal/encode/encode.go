// Package encode shares H.264 settings across recording and composition.
package encode

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

type Encoder string

// Init opens the hardware device before inputs are decoded.
func (e Encoder) Init() []string {
	if e == "vaapi" {
		return []string{"-init_hw_device", "vaapi=va", "-filter_hw_device", "va"}
	}
	return nil
}

// Upload finishes a CPU filter graph for the hardware encoder.
func (e Encoder) Upload() string {
	if e == "vaapi" {
		return "format=nv12,hwupload"
	}
	return ""
}

func (e Encoder) Args() []string {
	return e.QualityArgs("veryfast", 20)
}

// QualityArgs keeps codec selection shared when a pipeline needs a measured
// quality setting. Hardware encoders use quality as QP; libx264 uses CRF.
func (e Encoder) QualityArgs(preset string, quality int) []string {
	if e == "vaapi" {
		return []string{"-an", "-c:v", "h264_vaapi", "-rc_mode", "CQP", "-qp", strconv.Itoa(quality)}
	}
	return []string{"-an", "-c:v", "libx264", "-preset", preset, "-crf", strconv.Itoa(quality), "-pix_fmt", "yuv420p"}
}

// Probe checks the requested geometry and reports the driver for cache keys.
func (e Encoder) Probe(ctx context.Context, w, h, fps int) (string, error) {
	if e != "vaapi" {
		return "", nil
	}
	args := append(e.Init(), "-v", "verbose", "-f", "lavfi", "-i", fmt.Sprintf("color=s=%dx%d:r=%d:d=1", w, h, fps), "-vf", e.Upload())
	args = append(args, e.Args()...)
	args = append(args, "-frames:v", "1", "-f", "null", "-")
	b, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("render.encoder vaapi: %w: %s", err, strings.TrimSpace(string(b)))
	}
	for _, line := range strings.Split(string(b), "\n") {
		if _, driver, ok := strings.Cut(line, "VAAPI driver: "); ok {
			return strings.TrimSpace(driver), nil
		}
	}
	return "", fmt.Errorf("render.encoder vaapi: FFmpeg did not report the VAAPI driver")
}

// Threads is the CPU budget for preparing, filtering and encoding video.
type Threads struct {
	Prepare int `json:"prepare,omitempty"`
	Filter  int `json:"filter,omitempty"`
	Encode  int `json:"encode,omitempty"`
}

// Resolve divides preparation threads across parallel inputs; encoders share
// the total encode budget at the caller.
func (t Threads) Resolve(inputs int) (prepare, filter, encode int) {
	n := max(1, runtime.NumCPU())
	prepare, filter, encode = t.Prepare, t.Filter, t.Encode
	if prepare == 0 {
		prepare = max(1, n/min(max(1, inputs), n))
	}
	if filter == 0 {
		filter = 1
	}
	if encode == 0 {
		encode = n
	}
	return
}
