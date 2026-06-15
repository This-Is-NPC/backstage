package prompter

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Default popup-style values. These are the single source of truth for the
// built-in prompter's defaults; scene config defaulting (loader.applyDefaults)
// and the Hypr driver both reference these so the two can't drift.
const (
	DefaultFontSize = 18
	DefaultTitle    = "instruction.md"
	DefaultHeader   = "instruction.md"
	DefaultChrome   = "default"
	DefaultClass    = "backstage.popup"
	DefaultCPS      = 32
	DefaultTerm     = "ghostty"
)

// Opts configure a popup: its size, typing speed, and the terminal used.
type Opts struct {
	Width    int
	Height   int
	CPS      int
	Term     string // terminal command (e.g. "ghostty")
	FontSize int
	Title    string
	Header   string
	Chrome   string
	Class    string
}

// Prompter shows a floating instruction box that types text on screen.
type Prompter interface {
	// Preflight verifies the driver's runtime dependencies are available before
	// any recording starts, so a scene with dialog steps fails fast with a clear
	// error instead of producing a video with no narration. term is the
	// configured terminal command (empty selects the driver default).
	Preflight(term string) error
	// PreflightHypr verifies only the compositor dependency (hyprctl), not the
	// terminal. A scene that records a live transition overlay but no dialog uses
	// the compositor for the overlay but never opens the prompter terminal, so it
	// must fail fast on a missing compositor without requiring the terminal.
	PreflightHypr() error
	// Show opens the popup and types text into it; it stays until Close.
	Show(text string, opts Opts) error
	// Close dismisses the current popup.
	Close() error
}

// ValidateTitle rejects terminal/window metadata controls, including newlines,
// ESC/OSC sequences, DEL, and C1 controls.
func ValidateTitle(title string) error {
	if r, ok := unsafeTitleRune(title); ok {
		return fmt.Errorf("popup.style.title must not contain control character U+%04X", r)
	}
	return nil
}

// SafeTitle removes metadata controls before a title is passed to a terminal.
func SafeTitle(title string) string {
	var b strings.Builder
	for len(title) > 0 {
		r, size := utf8.DecodeRuneInString(title)
		if r == utf8.RuneError && size == 1 {
			title = title[size:]
			continue
		}
		if !isTitleControl(r) {
			b.WriteRune(r)
		}
		title = title[size:]
	}
	return b.String()
}

func unsafeTitleRune(title string) (rune, bool) {
	for len(title) > 0 {
		r, size := utf8.DecodeRuneInString(title)
		if r == utf8.RuneError && size == 1 {
			return rune(title[0]), true
		}
		if isTitleControl(r) {
			return r, true
		}
		title = title[size:]
	}
	return 0, false
}

func isTitleControl(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}
