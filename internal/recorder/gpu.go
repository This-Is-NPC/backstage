package recorder

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// gpuBinary is omarchy's hardware screen recorder (the Alt+PrintScreen tool).
const gpuBinary = "gpu-screen-recorder"

// GPU records a whole monitor with gpu-screen-recorder: hardware-encoded, CFR,
// no interactive region picker. Ports rec.sh.
type GPU struct {
	Monitor string
	FPS     int

	cmd  *exec.Cmd
	out  string
	done chan error
	wait error
}

// NewGPU returns a recorder targeting the given monitor at fps.
func NewGPU(monitor string, fps int) *GPU {
	return &GPU{Monitor: monitor, FPS: fps}
}

// args builds the gpu-screen-recorder argv for an output path. Separated for
// testing without invoking the binary.
func (g *GPU) args(outPath string) []string {
	return []string{
		"-w", g.Monitor,
		"-k", "auto",
		"-f", strconv.Itoa(g.FPS),
		"-fm", "cfr",
		"-fallback-cpu-encoding", "yes",
		"-o", outPath,
	}
}

// Start launches the recorder and waits until the output file appears (encoder warm).
func (g *GPU) Start(outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
		return err
	}
	g.out = outPath
	g.cmd = exec.Command(gpuBinary, g.args(outPath)...)
	if err := g.cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", gpuBinary, err)
	}
	g.done = make(chan error, 1)
	g.wait = nil
	go func() { g.done <- g.cmd.Wait() }()
	for i := 0; i < 50; i++ { // ~10s
		if _, err := os.Stat(outPath); err == nil {
			return nil
		}
		select {
		case err := <-g.done:
			g.done = nil
			g.wait = err
			if err == nil {
				return fmt.Errorf("%s exited before creating %s", gpuBinary, outPath)
			}
			return fmt.Errorf("%s exited before creating %s: %w", gpuBinary, outPath, err)
		case <-time.After(200 * time.Millisecond):
		}
	}
	_ = g.stopProcess()
	return fmt.Errorf("%s did not create %s within 10s", gpuBinary, outPath)
}

// Stop SIGINTs the recorder so the mp4 is finalized, then waits for it to exit.
func (g *GPU) Stop() (string, error) {
	if g.cmd == nil || g.cmd.Process == nil {
		return "", nil
	}
	err := g.stopProcess()
	if err != nil {
		return g.out, err
	}
	fi, err := os.Stat(g.out)
	if err != nil {
		return g.out, fmt.Errorf("recording output missing: %w", err)
	}
	if fi.Size() == 0 {
		return g.out, fmt.Errorf("recording output is empty: %s", g.out)
	}
	return g.out, nil
}

func (g *GPU) stopProcess() error {
	if g.done == nil {
		return unexpectedWait(g.wait)
	}
	select {
	case err := <-g.done:
		g.done = nil
		g.wait = err
		return unexpectedWait(err)
	default:
	}
	if err := g.cmd.Process.Signal(syscall.SIGINT); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop %s: %w", gpuBinary, err)
	}
	err := <-g.done
	g.done = nil
	g.wait = err
	return unexpectedWait(err)
}

func unexpectedWait(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() && status.Signal() == syscall.SIGINT {
				return nil
			}
		}
	}
	return fmt.Errorf("%s exited unexpectedly: %w", gpuBinary, err)
}
