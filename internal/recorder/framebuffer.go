package recorder

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/This-Is-NPC/backstage/internal/encode"
)

// Framebuffer records a guest from outside its session, including logout,
// reboot and greeter scenes. Screenshots stream into one encoder; the final
// remux applies the measured capture clock without another lossy encode.
type Framebuffer struct {
	Domain  string
	URI     string
	FPS     int
	Encoder encode.Encoder
	Threads encode.Threads

	mu            sync.Mutex
	out           string
	done          chan struct{}
	cancelCapture context.CancelFunc
	cancelEncode  context.CancelFunc
	result        error
	frames        int
	began         time.Time
	stopped       time.Time
}

func NewFramebuffer(domain, uri string, fps int) *Framebuffer {
	if uri == "" {
		uri = "qemu:///system"
	}
	if fps <= 0 {
		fps = 4
	}
	return &Framebuffer{Domain: domain, URI: uri, FPS: fps}
}

func (f *Framebuffer) Start(outPath string) error {
	f.mu.Lock()
	if f.done != nil {
		f.mu.Unlock()
		return fmt.Errorf("the recorder was already started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.out, f.cancelCapture, f.done = outPath, cancel, make(chan struct{})
	done := f.done
	f.mu.Unlock()
	ready := make(chan struct{})
	go func() {
		err := f.record(ctx, ready)
		cancel()
		f.mu.Lock()
		f.result = err
		f.mu.Unlock()
		close(done)
	}()
	select {
	case <-ready:
		return nil
	case <-done:
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.result
	}
}

// Quiet mode keeps virsh's success message out of the PNG stream. Buffer one
// complete screenshot so a failed grab cannot corrupt the encoder's input.
func (f *Framebuffer) grab(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "virsh", "-q", "-c", f.URI, "screenshot", f.Domain, "/dev/stdout")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("screenshot %s: %w: %s", f.Domain, err, strings.TrimSpace(stderr.String()))
	}
	if _, err := png.DecodeConfig(bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("screenshot %s is not a PNG: %w", f.Domain, err)
	}
	return data, nil
}

func (f *Framebuffer) record(ctx context.Context, ready chan<- struct{}) error {
	if err := os.MkdirAll(filepath.Dir(f.out), 0o700); err != nil {
		return err
	}
	first, err := f.grab(ctx)
	if err != nil {
		return fmt.Errorf("%s cannot be photographed: %w", f.Domain, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "backstage-framebuffer-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	encoding, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.mu.Lock()
	f.cancelEncode = cancel
	f.mu.Unlock()
	nominal := filepath.Join(work, "capture.mp4")
	args := append(f.Encoder.Init(), "-y", "-v", "error", "-f", "image2pipe", "-probesize", "32", "-analyzeduration", "0",
		"-framerate", strconv.Itoa(f.FPS), "-c:v", "png", "-i", "pipe:0")
	if up := f.Encoder.Upload(); up != "" {
		args = append(args, "-vf", up)
	}
	args = append(args, f.Encoder.Args()...)
	args = append(args, "-threads", strconv.Itoa(f.Threads.Encode), "-video_track_timescale", "1000000000", nominal)
	cmd := exec.CommandContext(encoding, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		return err
	}
	encoded := make(chan struct{})
	var encodeErr error
	go func() { encodeErr = cmd.Wait(); close(encoded) }()
	defer func() { _ = input.Close(); cancel(); <-encoded }()
	if _, err := input.Write(first); err != nil {
		return fmt.Errorf("streaming the first frame: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	f.frames, f.began = 1, time.Now()
	f.mu.Unlock()
	close(ready)
	var streamErr error
capture:
	for {
		select {
		case <-ctx.Done():
			break capture
		case <-encoded:
			return fmt.Errorf("framebuffer encoder stopped during capture: %v: %s", encodeErr, strings.TrimSpace(stderr.String()))
		case <-time.After(time.Second / time.Duration(f.FPS)):
		}
		frame, err := f.grab(ctx)
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			// A reboot can temporarily remove the screen. Keep the last frame
			// and preserve the elapsed time through the measured output clock.
			continue
		}
		if _, err := input.Write(frame); err != nil {
			streamErr = fmt.Errorf("streaming a framebuffer frame: %w", err)
			break
		}
		f.mu.Lock()
		f.frames++
		f.mu.Unlock()
	}
	_ = input.Close()
	<-encoded
	if encodeErr != nil {
		return fmt.Errorf("encoding framebuffer: %w: %s", encodeErr, strings.TrimSpace(stderr.String()))
	}
	if streamErr != nil {
		return streamErr
	}
	f.mu.Lock()
	frames := f.frames
	f.mu.Unlock()
	if frames < 2 {
		return fmt.Errorf("only %d frame(s) came off %s", frames, f.Domain)
	}
	// Round the measured capture rate to four decimals for the output clock.
	// Packet timestamps must be rescaled as well as H.264 timing metadata;
	// changing -r alone corrupts B-frame PTS and loses the capture clock.
	clock, err := framebufferClock(f.FPS, f.Rate())
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.out), ".framebuffer-*.mp4")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Close(); err != nil {
		return err
	}
	args = append([]string{"-y", "-v", "error", "-i", nominal, "-c:v", "copy"}, clock...)
	args = append(args, tmp.Name())
	remux := exec.CommandContext(encoding, "ffmpeg", args...)
	if b, err := remux.CombinedOutput(); err != nil {
		return fmt.Errorf("applying the framebuffer clock: %w: %s", err, b)
	}
	return os.Rename(tmp.Name(), f.out)
}

func framebufferClock(fps int, measured float64) ([]string, error) {
	rate := strconv.FormatFloat(measured, 'f', 4, 64)
	parsed, err := strconv.ParseFloat(rate, 64)
	units := int64(parsed*10000 + 0.5)
	if err != nil || units <= 0 || fps <= 0 {
		return nil, fmt.Errorf("invalid framebuffer clock: nominal %d, measured %s fps", fps, rate)
	}
	// Match the MP4 timescale selected for an image sequence at this rate.
	a, b := units, int64(10000)
	for b != 0 {
		a, b = b, a%b
	}
	timescale := units / a
	for timescale < 10000 {
		timescale *= 2
	}
	// Keep the bitstream filter's input timebase. Explicit output timebase
	// rescaling differs across FFmpeg releases. Recover presentation/decode
	// frame numbers first, then place them on the measured capture clock.
	filter := fmt.Sprintf("h264_metadata=tick_rate=%d/10000,setts=pts=round(PTS*TB*%d)/%s/TB:dts=round(DTS*TB*%d)/%s/TB:duration=1/%s/TB", 2*units, fps, rate, fps, rate, rate)
	return []string{"-bsf:v", filter, "-video_track_timescale", strconv.FormatInt(timescale, 10)}, nil
}

// Stop is safe to repeat. Cancellation stops screenshot acquisition; encoding
// and the atomic remux finish before the completed take is returned.
func (f *Framebuffer) Stop() (string, error) {
	f.mu.Lock()
	done, cancel := f.done, f.cancelCapture
	if done == nil {
		f.mu.Unlock()
		return "", fmt.Errorf("the recorder was never started")
	}
	if f.stopped.IsZero() {
		f.stopped = time.Now()
	}
	f.mu.Unlock()
	cancel()
	select {
	case <-done:
	case <-time.After(finaliseWait):
		f.mu.Lock()
		kill := f.cancelEncode
		f.mu.Unlock()
		if kill != nil {
			kill()
		}
		<-done
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.out, f.result
}

// Rate reports the achieved rate after capture stops, before encoding cleanup.
func (f *Framebuffer) Rate() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	elapsed := f.stopped.Sub(f.began).Seconds()
	if f.stopped.IsZero() || f.began.IsZero() || f.frames < 2 || elapsed <= 0 {
		return 0
	}
	return float64(f.frames) / elapsed
}
