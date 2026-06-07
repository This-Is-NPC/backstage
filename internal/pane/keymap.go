package pane

import "strings"

// keymap maps human key names (as written in a scene's "keys" commands) to tmux
// send-keys key tokens. Ported verbatim from the legacy runner.
var keymap = map[string]string{
	"esc": "Escape", "escape": "Escape", "enter": "Enter", "return": "Enter",
	"tab": "Tab", "space": "Space", "backspace": "BSpace", "bspace": "BSpace",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right",
	"up arrow": "Up", "down arrow": "Down", "left arrow": "Left", "right arrow": "Right",
	"pageup": "PageUp", "pgup": "PageUp", "pagedown": "PageDown", "pgdn": "PageDown",
	"home": "Home", "end": "End", "delete": "Delete", "del": "Delete",
	"f1": "F1", "f2": "F2", "f3": "F3", "f4": "F4", "f5": "F5", "f6": "F6",
}

// Token kinds returned by ToToken.
const (
	KindKey = "key" // a named key/chord, sent without -l
	KindLit = "lit" // a literal string, sent with -l
)

// ToToken classifies a "keys" command into a tmux send-keys token: a named key
// (esc/enter/arrows/fN), a ctrl+/alt+ chord (C-/M-), or else a literal string.
func ToToken(cmd string) (kind, token string) {
	c := strings.ToLower(strings.TrimSpace(cmd))
	if v, ok := keymap[c]; ok {
		return KindKey, v
	}
	if strings.HasPrefix(c, "ctrl+") {
		return KindKey, "C-" + c[len("ctrl+"):]
	}
	if strings.HasPrefix(c, "alt+") {
		return KindKey, "M-" + c[len("alt+"):]
	}
	return KindLit, cmd
}
