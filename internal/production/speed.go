package production

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/This-Is-NPC/backstage/internal/budget"
	"github.com/This-Is-NPC/backstage/internal/encode"
	"github.com/This-Is-NPC/backstage/internal/recorder"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

// retime rewrites a clip so that each stretch plays at its own rate, and
// returns the path of the new clip. The take on disk is never touched.
//
// The take is the evidence. Fifty real minutes of a machine spending a day is
// the thing that was proved, and it stays exactly as it was recorded. What
// speed to publish it at is a question somebody answers afterwards, more than
// once, and a clip written fast has thrown the answer away.
func retime(ctx context.Context, clip string, segments []scene.Segment, cfg scene.RenderCfg, onInterrupt func()) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	length, err := recorder.Duration(clip)
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
	cleanupPiecesOnInterrupt := func() {
		_ = os.RemoveAll(work)
		if onInterrupt != nil {
			onInterrupt()
		}
	}

	pieces := make([]string, len(cuts))
	for index := range cuts {
		pieces[index] = filepath.Join(work, fmt.Sprintf("%03d.mp4", index))
	}
	encoder := encode.Encoder(cfg.Encoder)
	batchSize := retimeBatchSize(cfg, len(cuts))
	for first := 0; first < len(cuts); first += batchSize {
		end := min(first+batchSize, len(cuts))
		args := append(encoder.Init(), "-y", "-v", "error", "-filter_threads", strconv.Itoa(cfg.Threads.Filter))
		// Keep a separately sought input and output clock for each piece. A
		// shared trim/concat graph changes FFmpeg's frame selection at cuts.
		for _, cut := range cuts[first:end] {
			args = append(args,
				"-ss", strconv.FormatFloat(cut.From, 'f', 3, 64),
				"-t", strconv.FormatFloat(cut.To-cut.From, 'f', 3, 64), "-i", clip)
		}
		for index, cut := range cuts[first:end] {
			filter := "setpts=PTS/" + strconv.FormatFloat(cut.Rate, 'f', -1, 64)
			if cut.Badge != "" {
				filter += "," + badge(cut.Badge)
			}
			if upload := encoder.Upload(); upload != "" {
				filter += "," + upload
			}
			args = append(args, "-map", fmt.Sprintf("%d:v:0", index), "-vf", filter)
			args = append(args, encoder.Args()...)
			args = append(args, "-threads", strconv.Itoa(cfg.Threads.Encode), pieces[first+index])
		}
		if err := runProductionCommand(exec.CommandContext(ctx, "ffmpeg", args...), cleanupPiecesOnInterrupt); err != nil {
			return "", fmt.Errorf("retiming %s (pieces %d..%d): %w", filepath.Base(clip), first+1, end, err)
		}
	}

	out := strings.TrimSuffix(clip, filepath.Ext(clip)) + ".retimed.mp4"
	if err := concatFinal(ctx, pieces, out, work, cleanupPiecesOnInterrupt); err != nil {
		return "", fmt.Errorf("joining the retimed pieces of %s: %w",
			filepath.Base(clip), err)
	}
	return out, nil
}

// Explicit budgets and hardware encoders stay sequential. Automatic CPU batches
// retain each encoder's settings and reserve two GiB per concurrent piece.
func retimeBatchSize(cfg scene.RenderCfg, pieces int) int {
	if cfg.Encoder == "vaapi" || cfg.Threads.Encode != 0 || cfg.Threads.Filter != 0 {
		return 1
	}
	available, err := budget.ReadMemAvailable()
	if err != nil {
		return 1
	}
	return max(1, min(pieces, budget.NumCPU()/2, int(budget.MemoryBudget(available)/(2<<30))))
}

// badge draws the rate over the middle of the picture, faintly.
//
// Faint on purpose, and always on while the stretch plays. A viewer who looks
// away and back has to be able to tell a fast film from a fast machine, and a
// mark that appeared once at the start cannot tell them anything later.
func badge(text string) string {
	quoted := strings.ReplaceAll(text, "'", `\'`)
	return "drawtext=text='" + quoted + "'" +
		":x=(w-text_w)/2:y=(h-text_h)/2:fontsize=140:fontcolor=white@0.16"
}
