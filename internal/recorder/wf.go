package recorder

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/This-Is-NPC/backstage/internal/guest"
)

// wfBinary records a wlroots screen through wlr-screencopy, encoding in
// software.
const wfBinary = "wf-recorder"

// WF records an Omarchy guest's own screen, from inside the guest.
//
// Not gpu-screen-recorder, and not from the host. These guests have a `card0`
// and no render node, so the hardware recorder's MESA cannot find a driver
// (`ZINK: failed to choose pdev`) and it writes no file at all. And recording
// the guest from the host means recording a *window* showing the guest: the
// host compositor's scaling, the viewer's chrome and the host's own cursor all
// end up in the film. wf-recorder inside gives the guest's framebuffer at its
// native size and nothing else.
type WF struct {
	Guest *guest.Guest
	FPS   int

	remote string
	out    string
}

// NewWF returns a recorder for one guest.
func NewWF(g *guest.Guest, fps int) *WF { return &WF{Guest: g, FPS: fps} }

// args builds the wf-recorder argv. Separated so the shape can be checked
// without a guest, the way GPU.args is.
func (w *WF) args(remote string) []string {
	args := []string{"-c", "libx264", "-f", remote}
	if w.FPS > 0 {
		args = append(args, "-r", strconv.Itoa(w.FPS))
	}
	return args
}

// Start begins recording inside the guest and returns once the file exists.
func (w *WF) Start(outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
		return err
	}
	w.out = outPath
	w.remote = "/tmp/backstage-take.mp4"
	if _, err := w.Guest.Root("rm -f " + w.remote); err != nil {
		return fmt.Errorf("clearing the last take on %s: %w", w.Guest.Domain, err)
	}
	// Detached through the session's own manager: a recorder started as a
	// background job of an ssh dies when that ssh returns, and the failure is a
	// scene that plays perfectly and writes nothing.
	command := fmt.Sprintf("systemd-run --user --collect --unit=backstage-recorder %s %s",
		wfBinary, strings.Join(w.args(w.remote), " "))
	if out, err := w.Guest.InSession(command); err != nil {
		return fmt.Errorf("starting %s on %s: %s", wfBinary, w.Guest.Domain, strings.TrimSpace(out))
	}
	// Warm, and proven warm: the encoder writes its header a moment after the
	// unit starts, and a scene that began typing before that loses its opening.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if w.Guest.Try("test -s " + w.remote) {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("%s wrote nothing on %s within twenty seconds", wfBinary, w.Guest.Domain)
}

// Stop ends the recording and brings the clip back to the host.
func (w *WF) Stop() (string, error) {
	if w.remote == "" {
		return "", fmt.Errorf("the recorder was never started")
	}
	// SIGINT and not SIGTERM: wf-recorder finalises the container on an
	// interrupt and is killed by a terminate, and a killed one leaves an mp4
	// with no duration that half the players refuse.
	_, _ = w.Guest.Root("pkill -INT -x " + wfBinary)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if !w.Guest.Try("pgrep -x " + wfBinary) {
			break
		}
		time.Sleep(time.Second)
	}
	_, _ = w.Guest.InSession("systemctl --user reset-failed backstage-recorder")
	if err := w.Guest.Fetch(w.remote, w.out); err != nil {
		return "", err
	}
	info, err := os.Stat(w.out)
	if err != nil {
		return "", err
	}
	if info.Size() == 0 {
		return "", fmt.Errorf("the take from %s is empty", w.Guest.Domain)
	}
	return w.out, nil
}
