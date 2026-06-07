package prompter

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// PopupClass is the window class the floating instruction box is matched by.
const PopupClass = "backstage.popup"

// classRE is the Hyprland class match regex for the popup (dots escaped).
var classRE = "^(" + strings.ReplaceAll(PopupClass, ".", `\.`) + ")$"

// Hypr shows the popup as a floating, centered terminal window using omarchy's
// windowrule mechanism, and types into it by re-executing the backstage binary
// in __type mode. Ports popup.sh + typewriter.sh.
type Hypr struct {
	// Self is the path to the backstage binary (defaults to os.Executable()).
	Self string
	tmp  string
}

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

// Show writes text to a temp file, floats+centers+sizes the popup window, and
// spawns a terminal that types the text via `backstage __type`.
func (h *Hypr) Show(text string, opts Opts) error {
	if h.tmp != "" {
		_ = os.Remove(h.tmp)
		h.tmp = ""
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

	term := opts.Term
	if term == "" {
		term = "ghostty"
	}
	font := opts.FontSize
	if font == 0 {
		font = 18
	}
	cps := opts.CPS
	if cps == 0 {
		cps = 32
	}

	// Clear any prior popup, then float/size/center the next window of this class.
	_ = hyprctl("dispatch", "closewindow", "class:"+classRE)
	_ = hyprctl("keyword", "windowrule", "float on, match:class "+classRE)
	_ = hyprctl("keyword", "windowrule",
		fmt.Sprintf("size %d %d, match:class %s", opts.Width, opts.Height, classRE))
	_ = hyprctl("keyword", "windowrule", "center on, match:class "+classRE)

	header := `printf '\n  \033[2m── instruction.md ─────────────────────\033[0m\n\n'; `
	inner := header +
		shquote(h.self()) + " __type " + shquote(tmp.Name()) + " " + strconv.Itoa(cps) +
		"; read -r -t 600 _"
	cmd := exec.Command(term,
		"--class="+PopupClass,
		"--title=instruction.md",
		"--font-size="+strconv.Itoa(font),
		"-e", "bash", "-lc", inner)
	if err := cmd.Start(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("spawn popup terminal: %w", err)
	}
	h.tmp = tmp.Name()

	// Wait for it to map, then focus + recenter (center can miss over fullscreen).
	for i := 0; i < 20; i++ {
		if h.mapped() {
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
	err := hyprctl("dispatch", "closewindow", "class:"+classRE)
	if h.tmp != "" {
		if rmErr := os.Remove(h.tmp); err == nil && rmErr != nil && !os.IsNotExist(rmErr) {
			err = rmErr
		}
		h.tmp = ""
	}
	return err
}

func (h *Hypr) mapped() bool {
	out, err := exec.Command("hyprctl", "clients", "-j").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), `"class": "`+PopupClass+`"`)
}

// shquote single-quotes a string for safe embedding in a bash -lc command.
func shquote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
