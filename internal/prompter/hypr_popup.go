package prompter

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Hypr shows the popup as a floating, centered terminal window using omarchy's
// windowrule mechanism, and types into it by re-executing the backstage binary
// in __type mode. Ports popup.sh + typewriter.sh.
type Hypr struct {
	// Self is the path to the backstage binary (defaults to os.Executable()).
	Self string

	mu     sync.Mutex
	tmp    string
	class  string
	cmd    *exec.Cmd
	waitCh chan error
}

var popupTerminateGrace = 2 * time.Second

func hyprctl(args ...string) error {
	return exec.Command("hyprctl", args...).Run()
}

func (h *Hypr) self() string {
	if h.Self != "" {
		return h.Self
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "backstage"
}

// PreflightHypr verifies only the compositor dependency (hyprctl) is on PATH. A
// scene that records a live transition overlay (but no dialog) drives the
// compositor for the overlay yet never opens the prompter terminal, so requiring
// the terminal here would wrongly fail such a scene on a host without it.
func (h *Hypr) PreflightHypr() error {
	if _, err := exec.LookPath("hyprctl"); err != nil {
		return fmt.Errorf("popup driver hypr-terminal requires hyprctl: %w", err)
	}
	return nil
}

// Preflight verifies hyprctl and the configured terminal are on PATH so a scene
// with dialog steps fails fast before recording starts, rather than finalizing a
// narration-less video and surfacing the error afterwards. An empty term
// selects the driver default ("ghostty").
func (h *Hypr) Preflight(term string) error {
	if err := h.PreflightHypr(); err != nil {
		return err
	}
	if term == "" {
		term = DefaultTerm
	}
	if _, err := exec.LookPath(term); err != nil {
		return fmt.Errorf("popup driver hypr-terminal requires terminal %q: %w", term, err)
	}
	return nil
}

// Show writes text to a temp file, floats+centers+sizes the popup window, and
// spawns a terminal that types the text via `backstage __type`.
func (h *Hypr) Show(text string, opts Opts) error {
	h.closeExisting()
	term := opts.Term
	if term == "" {
		term = DefaultTerm
	}
	if err := h.Preflight(term); err != nil {
		return err
	}
	font := opts.FontSize
	if font == 0 {
		font = DefaultFontSize
	}
	title := SafeTitle(opts.Title)
	if title == "" {
		title = DefaultTitle
	}
	header := opts.Header
	if header == "" {
		header = title
	}
	chrome := opts.Chrome
	if chrome == "" {
		chrome = DefaultChrome
	}
	class := opts.Class
	if class == "" {
		class = DefaultClass
	}
	classRE := classREFor(class)
	cps := opts.CPS
	if cps == 0 {
		cps = DefaultCPS
	}
	tmp, err := os.CreateTemp("", "backstage-popup-*.txt")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	tmp.Close()

	// Clear any prior popup, then float/size/center the next window of this class.
	_ = hyprctl("dispatch", "closewindow", "class:"+classRE)
	_ = hyprctl("keyword", "windowrule", "float on, match:class "+classRE)
	_ = hyprctl("keyword", "windowrule",
		fmt.Sprintf("size %d %d, match:class %s", opts.Width, opts.Height, classRE))
	_ = hyprctl("keyword", "windowrule", "center on, match:class "+classRE)

	inner := headerCommand(header, chrome) +
		shquote(h.self()) + " __type " + shquote(tmp.Name()) + " " + strconv.Itoa(cps) +
		"; rm -f " + shquote(tmp.Name()) + "; read -r -t 600 _"
	cmd := exec.Command(term,
		"--class="+class,
		"--title="+title,
		"--font-size="+strconv.Itoa(font),
		"-e", "bash", "-lc", inner)
	setPopupProcessGroup(cmd)
	waitCh := make(chan error, 1)
	h.mu.Lock()
	if err := cmd.Start(); err != nil {
		h.mu.Unlock()
		os.Remove(tmp.Name())
		return fmt.Errorf("spawn popup terminal: %w", err)
	}
	go func() {
		waitCh <- cmd.Wait()
		close(waitCh)
	}()
	h.tmp = tmp.Name()
	h.class = class
	h.cmd = cmd
	h.waitCh = waitCh
	h.mu.Unlock()

	// Wait for it to map, then focus + recenter (center can miss over fullscreen).
	for i := 0; i < 20; i++ {
		if h.mapped(class) {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	_ = hyprctl("dispatch", "focuswindow", "class:"+classRE)
	_ = hyprctl("dispatch", "centerwindow")
	return nil
}

// Close dismisses the popup window.
func (h *Hypr) Close() error {
	h.mu.Lock()
	class := h.class
	h.mu.Unlock()
	return h.CloseClass(class)
}

// CloseClass dismisses a popup by class. Empty class falls back to the default.
func (h *Hypr) CloseClass(class string) error {
	if class == "" {
		class = DefaultClass
	}
	err := hyprctl("dispatch", "closewindow", "class:"+classREFor(class))
	tmp, cmd, waitCh := h.detachPopup(class)
	if tmp != "" {
		if rmErr := os.Remove(tmp); err == nil && rmErr != nil && !os.IsNotExist(rmErr) {
			err = rmErr
		}
	}
	if procErr := closePopupProcess(cmd, waitCh); err == nil && procErr != nil {
		err = procErr
	}
	return err
}

func (h *Hypr) closeExisting() {
	h.mu.Lock()
	hasPopup := h.tmp != "" || h.cmd != nil
	class := h.class
	h.mu.Unlock()
	if hasPopup {
		_ = h.CloseClass(class)
	}
}

func (h *Hypr) detachPopup(class string) (string, *exec.Cmd, chan error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.tmp == "" && h.cmd == nil {
		return "", nil, nil
	}
	trackedClass := h.class
	if trackedClass == "" {
		trackedClass = DefaultClass
	}
	if trackedClass != class {
		return "", nil, nil
	}
	tmp, cmd, waitCh := h.tmp, h.cmd, h.waitCh
	h.tmp = ""
	h.class = ""
	h.cmd = nil
	h.waitCh = nil
	return tmp, cmd, waitCh
}

func closePopupProcess(cmd *exec.Cmd, waitCh chan error) error {
	if cmd == nil || cmd.Process == nil || waitCh == nil {
		return nil
	}
	select {
	case err := <-waitCh:
		return expectedPopupExit(err)
	default:
	}
	if err := signalPopupProcessGroup(cmd, syscall.SIGTERM); err != nil {
		return fmt.Errorf("close popup terminal: %w", err)
	}
	select {
	case err := <-waitCh:
		return expectedPopupExit(err)
	case <-time.After(popupTerminateGrace):
	}
	if err := signalPopupProcessGroup(cmd, syscall.SIGKILL); err != nil {
		return fmt.Errorf("kill popup terminal: %w", err)
	}
	select {
	case err := <-waitCh:
		return expectedPopupExit(err)
	case <-time.After(popupTerminateGrace):
		return fmt.Errorf("popup terminal did not exit after SIGKILL")
	}
}

func setPopupProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func signalPopupProcessGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if sigErr := cmd.Process.Signal(sig); sigErr != nil && !errors.Is(sigErr, os.ErrProcessDone) {
			return errors.Join(err, sigErr)
		}
	}
	return nil
}

func expectedPopupExit(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				switch status.Signal() {
				case syscall.SIGTERM, syscall.SIGKILL, syscall.SIGHUP, syscall.SIGINT:
					return nil
				}
			}
		}
	}
	return err
}

func (h *Hypr) mapped(class string) bool {
	out, err := exec.Command("hyprctl", "clients", "-j").Output()
	if err != nil {
		return false
	}
	if class == "" {
		class = DefaultClass
	}
	return strings.Contains(string(out), `"class": "`+class+`"`)
}

func classREFor(class string) string {
	if class == "" {
		class = DefaultClass
	}
	return "^(" + regexp.QuoteMeta(class) + ")$"
}

func headerCommand(header, chrome string) string {
	header = literalHeader(header)
	switch chrome {
	case "none":
		return ""
	case "minimal":
		return "printf '%s' " + shquote("\n  "+header+"\n\n") + "; "
	default:
		return "printf '%s%s%s' " +
			shquote("\n  \033[2m── ") + " " +
			shquote(header) + " " +
			shquote(" ─────────────────────\033[0m\n\n") + "; "
	}
}

func literalHeader(header string) string {
	var b strings.Builder
	for _, r := range header {
		switch r {
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case 0x7f:
			b.WriteString(`\x7f`)
		default:
			if r < 0x20 || (r >= 0x80 && r <= 0x9f) {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// shquote single-quotes a string for safe embedding in a bash -lc command.
func shquote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
