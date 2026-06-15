package recorder

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// gpuBinary is omarchy's hardware screen recorder (the Alt+PrintScreen tool).
const gpuBinary = "gpu-screen-recorder"

var (
	recorderStopGrace = 5 * time.Second
	recorderKillWait  = 2 * time.Second
)

// GPU records a whole monitor with gpu-screen-recorder: hardware-encoded, CFR,
// no interactive region picker. Ports rec.sh.
type GPU struct {
	Monitor string
	FPS     int

	mu            sync.Mutex
	cmd           *exec.Cmd
	out           string
	done          chan struct{}
	wait          error
	stopRequested bool
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
	cmd := exec.Command(gpuBinary, g.args(outPath)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	done := make(chan struct{})

	g.mu.Lock()
	if g.stopRequested {
		g.mu.Unlock()
		return fmt.Errorf("%s start cancelled", gpuBinary)
	}
	g.out = outPath
	g.cmd = cmd
	g.done = done
	g.wait = nil
	g.stopRequested = false
	if err := cmd.Start(); err != nil {
		g.cmd = nil
		g.done = nil
		g.mu.Unlock()
		return fmt.Errorf("start %s: %w", gpuBinary, err)
	}
	go func() {
		err := cmd.Wait()
		g.mu.Lock()
		g.wait = err
		close(done)
		g.mu.Unlock()
	}()
	g.mu.Unlock()
	for i := 0; i < 50; i++ { // ~10s
		if _, err := os.Stat(outPath); err == nil {
			return nil
		}
		select {
		case <-done:
			if _, err := os.Stat(outPath); err == nil {
				return nil
			}
			err := g.waitErr()
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

// Stop SIGINTs the recorder so the mp4 is finalized, then waits with a bounded
// SIGKILL fallback so interrupt cleanup cannot hang forever.
func (g *GPU) Stop() (string, error) {
	g.mu.Lock()
	out := g.out
	cmd := g.cmd
	if cmd == nil {
		g.stopRequested = true
	}
	g.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return "", nil
	}
	err := g.stopProcess()
	if err != nil {
		return out, err
	}
	fi, err := os.Stat(out)
	if err != nil {
		return out, fmt.Errorf("recording output missing: %w", err)
	}
	if fi.Size() == 0 {
		return out, fmt.Errorf("recording output is empty: %s", out)
	}
	return out, nil
}

func (g *GPU) stopProcess() error {
	g.mu.Lock()
	cmd := g.cmd
	done := g.done
	wait := g.wait
	g.mu.Unlock()
	if cmd == nil || cmd.Process == nil || done == nil {
		return unexpectedWait(wait)
	}
	select {
	case <-done:
		return unexpectedWait(g.waitErr())
	default:
	}
	if err := signalRecorder(cmd, syscall.SIGINT); err != nil {
		return fmt.Errorf("stop %s: %w", gpuBinary, err)
	}
	select {
	case <-done:
		return unexpectedWait(g.waitErr())
	case <-time.After(recorderStopGrace):
	}

	timeoutErr := fmt.Errorf("stop %s: did not exit within %s after SIGINT; sent SIGKILL", gpuBinary, recorderStopGrace)
	if err := signalRecorder(cmd, syscall.SIGKILL); err != nil {
		return errors.Join(timeoutErr, fmt.Errorf("kill %s: %w", gpuBinary, err))
	}
	select {
	case <-done:
		if err := expectedWait(g.waitErr(), syscall.SIGINT, syscall.SIGKILL); err != nil {
			return errors.Join(timeoutErr, err)
		}
		return timeoutErr
	case <-time.After(recorderKillWait):
		return errors.Join(timeoutErr, fmt.Errorf("%s did not exit after SIGKILL", gpuBinary))
	}
}

func signalRecorder(cmd *exec.Cmd, sig syscall.Signal) error {
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

func (g *GPU) waitErr() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.wait
}

func unexpectedWait(err error) error {
	return expectedWait(err, syscall.SIGINT)
}

func expectedWait(err error, signals ...syscall.Signal) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				for _, sig := range signals {
					if status.Signal() == sig {
						return nil
					}
				}
			}
		}
	}
	return fmt.Errorf("%s exited unexpectedly: %w", gpuBinary, err)
}
