package prompter

// Opts configure a popup: its size, typing speed, and the terminal used.
type Opts struct {
	Width    int
	Height   int
	CPS      int
	Term     string // terminal command (e.g. "ghostty")
	FontSize int
}

// Prompter shows a floating instruction box that types text on screen.
type Prompter interface {
	// Show opens the popup and types text into it; it stays until Close.
	Show(text string, opts Opts) error
	// Close dismisses the current popup.
	Close() error
}
