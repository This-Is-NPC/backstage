package recorder

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Duration reads how long a clip is, in seconds.
//
// It lives beside the recorders and not beside the code that stitches them
// because it is the one question every recorder's output can be asked, and both
// the engine (which checks a take against the window it was filmed in) and the
// production pipeline (which retimes clips) have to ask it the same way.
func Duration(clip string) (float64, error) {
	said, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=nw=1:nk=1", clip).Output()
	if err != nil {
		return 0, fmt.Errorf("reading the length of %s: %w", filepath.Base(clip), err)
	}
	length, err := strconv.ParseFloat(strings.TrimSpace(string(said)), 64)
	if err != nil || length <= 0 {
		return 0, fmt.Errorf("%s reports a length of %q", filepath.Base(clip),
			strings.TrimSpace(string(said)))
	}
	return length, nil
}
