package production

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

// Retime rewrites a clip so that each stretch plays at its own rate, and
// returns the path of the new clip. The take on disk is never touched.
//
// The take is the evidence. Fifty real minutes of a machine spending a day is
// the thing that was proved, and it stays exactly as it was recorded. What
// speed to publish it at is a question somebody answers afterwards, more than
// once, and a clip written fast has thrown the answer away.
func Retime(clip string, segments []scene.Segment, badgeFont string) (string, error) {
	length, err := Duration(clip)
	if err != nil {
		return "", err
	}
	cuts, err := scene.Cuts(segments, length)
	if err != nil {
		return "", fmt.Errorf("%s: %w", filepath.Base(clip), err)
	}
	if len(cuts) == 1 && cuts[0].Rate == 1 && cuts[0].Badge == "" {
		return clip, nil
	}

	work, err := os.MkdirTemp("", "backstage-retime-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)

	var pieces []string
	for index, cut := range cuts {
		piece := filepath.Join(work, fmt.Sprintf("%03d.mp4", index))
		if err := onePiece(clip, piece, cut, badgeFont); err != nil {
			return "", err
		}
		pieces = append(pieces, piece)
	}

	list := filepath.Join(work, "pieces.txt")
	var lines strings.Builder
	for _, piece := range pieces {
		fmt.Fprintf(&lines, "file '%s'\n", piece)
	}
	if err := os.WriteFile(list, []byte(lines.String()), 0o644); err != nil {
		return "", err
	}
	out := strings.TrimSuffix(clip, filepath.Ext(clip)) + ".retimed.mp4"
	joined := exec.Command("ffmpeg", "-y", "-v", "error", "-f", "concat", "-safe", "0",
		"-i", list, "-c", "copy", out)
	if said, err := joined.CombinedOutput(); err != nil {
		return "", fmt.Errorf("joining the retimed pieces of %s: %s",
			filepath.Base(clip), said)
	}
	return out, nil
}

// onePiece cuts one stretch, changes its rate, and draws its badge.
func onePiece(clip, out string, cut scene.Cut, badgeFont string) error {
	// `setpts` and not a frame rate change: dividing the timestamps drops the
	// frames evenly and keeps the clip's own rate, so ten pieces at ten rates
	// still concatenate without a re-encode of the join.
	filter := fmt.Sprintf("setpts=PTS/%s", strconv.FormatFloat(cut.Rate, 'f', -1, 64))
	if cut.Badge != "" {
		filter += "," + badge(cut.Badge, badgeFont)
	}
	// -ss before -i seeks, -t is the length of the source stretch. The output
	// is shorter than -t by the rate, which is the point.
	trim := exec.Command("ffmpeg", "-y", "-v", "error",
		"-ss", strconv.FormatFloat(cut.From, 'f', 3, 64),
		"-t", strconv.FormatFloat(cut.To-cut.From, 'f', 3, 64),
		"-i", clip, "-vf", filter, "-an",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "20",
		"-pix_fmt", "yuv420p", out)
	if said, err := trim.CombinedOutput(); err != nil {
		return fmt.Errorf("retiming %.0fs..%.0fs at %gx: %s", cut.From, cut.To, cut.Rate, said)
	}
	return nil
}

// badge draws the rate over the middle of the picture, faintly.
//
// Faint on purpose, and always on while the stretch plays. A viewer who looks
// away and back has to be able to tell a fast film from a fast machine, and a
// mark that appeared once at the start cannot tell them anything later.
func badge(text, font string) string {
	quoted := strings.ReplaceAll(text, "'", `\'`)
	drawn := "drawtext=text='" + quoted + "'" +
		":x=(w-text_w)/2:y=(h-text_h)/2:fontsize=140:fontcolor=white@0.16"
	if font != "" {
		drawn += ":fontfile=" + font
	}
	return drawn
}

// Duration reads how long a clip is, in seconds.
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
