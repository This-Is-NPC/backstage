package recorder

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Framebuffer records a libvirt guest from outside it, by asking qemu for the
// screen rather than asking the guest to record itself.
//
// **It is for the takes the guest cannot record.** `WF` runs inside the
// session, which is the right place for almost everything and is exactly wrong
// for a scene that ends with that session being killed: the recorder dies with
// it, mid-frame, and the clip is truncated or has no duration at all. A scene
// that films a logout, a crash, a reboot or a greeter has to be filmed from
// outside.
//
// The picture is the framebuffer, so nothing inside the guest can affect it:
// no compositor, no protocol, no permission, no GPU. That is the whole appeal.
// The cost is the rate. Each frame is a full grab over libvirt, so this is
// coarse where wf-recorder is smooth, and a scene should ask for it only when
// it needs what it buys.
type Framebuffer struct {
	Domain string
	URI    string
	// FPS is the rate to aim for. The real rate is whatever the grabs achieved
	// and is measured rather than assumed; see Stop.
	FPS int

	mu      sync.Mutex
	dir     string
	out     string
	stop    chan struct{}
	done    chan struct{}
	frames  int
	began   time.Time
	stopped time.Time
}

// NewFramebuffer returns a recorder for one domain.
func NewFramebuffer(domain, uri string, fps int) *Framebuffer {
	if uri == "" {
		uri = "qemu:///system"
	}
	if fps <= 0 {
		fps = 4
	}
	return &Framebuffer{Domain: domain, URI: uri, FPS: fps}
}

// Start begins grabbing frames.
func (f *Framebuffer) Start(outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "backstage-frames-*")
	if err != nil {
		return err
	}
	// One grab before anything else, so a domain that cannot be photographed at
	// all fails here rather than after a scene has been performed to nobody.
	if err := f.grab(dir, 0); err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("%s cannot be photographed: %w", f.Domain, err)
	}

	f.mu.Lock()
	f.dir, f.out, f.frames = dir, outPath, 1
	f.stop, f.done = make(chan struct{}), make(chan struct{})
	f.began = time.Now()
	stop, done := f.stop, f.done
	f.mu.Unlock()

	go func() {
		defer close(done)
		every := time.Second / time.Duration(f.FPS)
		for {
			select {
			case <-stop:
				return
			case <-time.After(every):
			}
			f.mu.Lock()
			index := f.frames
			f.mu.Unlock()
			// A failed grab is not the end of the take: a guest rebooting has
			// no screen for a moment, and that moment is often the thing being
			// filmed. The previous frame simply stands.
			if err := f.grab(dir, index); err == nil {
				f.mu.Lock()
				f.frames++
				f.mu.Unlock()
			}
		}
	}()
	return nil
}

// The extension is always .png and never the caller's choice. On some
// framebuffers `virsh screenshot` writes a PNG whatever name it is given, and
// ffmpeg picks its decoder from the extension -- so a file called .ppm is a
// file every frame of which ffmpeg rejects as invalid data, and the film comes
// out empty with no error anybody saw.
func framePath(dir string, index int) string {
	return filepath.Join(dir, fmt.Sprintf("%06d.png", index))
}

func (f *Framebuffer) grab(dir string, index int) error {
	path := framePath(dir, index)
	out, err := exec.Command("virsh", "-c", f.URI, "screenshot", f.Domain, path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", out)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return fmt.Errorf("no frame written")
	}
	return nil
}

// Stop ends the grabbing and assembles the frames.
func (f *Framebuffer) Stop() (string, error) {
	f.mu.Lock()
	dir, out, stop, done := f.dir, f.out, f.stop, f.done
	f.mu.Unlock()
	if dir == "" {
		return "", fmt.Errorf("the recorder was never started")
	}
	close(stop)
	<-done
	defer os.RemoveAll(dir)

	f.mu.Lock()
	f.stopped = time.Now()
	frames, elapsed := f.frames, f.stopped.Sub(f.began).Seconds()
	f.mu.Unlock()
	if frames < 2 {
		return "", fmt.Errorf("only %d frame(s) came off %s", frames, f.Domain)
	}

	// The rate the grabs actually achieved, and not the rate that was asked
	// for. A grab over libvirt costs more than a sleep, so a loop aiming at
	// four a second lands somewhere below it -- and assembling below-rate
	// frames at the asked-for rate is a film that plays fast, silently, by
	// exactly the fraction it fell short. Measuring makes it real time by
	// construction.
	rate := float64(frames) / elapsed
	if rate <= 0 {
		return "", fmt.Errorf("no time passed while recording %s", f.Domain)
	}

	assemble := exec.Command("ffmpeg", "-y", "-v", "error",
		"-framerate", strconv.FormatFloat(rate, 'f', 4, 64),
		"-i", filepath.Join(dir, "%06d.png"),
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "20",
		"-pix_fmt", "yuv420p", out)
	if said, err := assemble.CombinedOutput(); err != nil {
		return "", fmt.Errorf("assembling %d frames from %s: %s", frames, f.Domain, said)
	}
	return out, nil
}

// Rate reports the frames per second the last take actually achieved, for a
// caller that wants to say so out loud.
func (f *Framebuffer) Rate() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped.IsZero() || f.frames < 2 {
		return 0
	}
	return float64(f.frames) / f.stopped.Sub(f.began).Seconds()
}
