package prompter

import (
	"bufio"
	"io"
	"math"
	"os"
	"time"
)

const defCPS = 32

// caret is the trailing block printed after the text, held briefly so a
// recording catches the full message.
const caret = "\n\n  ▎"

// Typewriter prints text to w one rune at a time at cps characters/second,
// pausing longer on newlines and shorter on spaces so it reads as human typing.
// UTF-8 aware (ranges over runes). Ports typewriter.sh.
func Typewriter(w io.Writer, text string, cps float64) {
	cps = normalizeCPS(cps)
	bw := bufio.NewWriter(w)
	delay := time.Duration(float64(time.Second) / cps)
	for _, ch := range text {
		bw.WriteRune(ch)
		bw.Flush()
		switch ch {
		case '\n':
			time.Sleep(delay * 6)
		case ' ':
			time.Sleep(time.Duration(float64(delay) * 0.4))
		default:
			time.Sleep(delay)
		}
	}
	bw.WriteString(caret)
	bw.Flush()
	time.Sleep(1500 * time.Millisecond)
}

// TypeDuration estimates how long Typewriter takes for text at cps, so the
// caller knows how long to hold the popup before closing it. Mirrors the
// legacy type_duration: ~1.8s warm-up + per-rune pacing + 0.5s tail.
func TypeDuration(text string, cps float64) time.Duration {
	cps = normalizeCPS(cps)
	d := 1.0 / cps
	total := 1.8
	for _, ch := range text {
		switch ch {
		case '\n':
			total += d * 6
		case ' ':
			total += d * 0.4
		default:
			total += d
		}
	}
	total += 0.5
	return time.Duration(total * float64(time.Second))
}

func normalizeCPS(cps float64) float64 {
	if cps <= 0 || math.IsNaN(cps) || math.IsInf(cps, 0) {
		return defCPS
	}
	return cps
}

// RunType reads a file and types it to stdout. The popup terminal invokes the
// backstage binary in this mode so typing needs no external interpreter.
func RunType(path string, cps float64) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	Typewriter(os.Stdout, string(b), cps)
	return nil
}
