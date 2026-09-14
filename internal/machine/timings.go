package machine

import (
	"fmt"
	"io"
	"math"
	"os"
	"syscall"
	"time"
)

// StartTimes is what the last Restore or Begin completed. Failed phases are nil.
type StartTimes struct {
	RestoreStopSeconds     *float64
	RestoreActivateSeconds *float64
	BootSeconds            *float64
}

func (m *Manager) now() time.Time {
	if m != nil && m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) since(t time.Time) time.Duration {
	return m.now().Sub(t)
}

func seconds(d time.Duration) float64 {
	return math.Round(d.Seconds()*1000) / 1000
}

func secondsPtr(d time.Duration) *float64 {
	v := seconds(d)
	return &v
}

func (m *Manager) warn(msg string) {
	var w io.Writer = os.Stderr
	if m != nil && m.Output != nil {
		w = m.Output
	}
	fmt.Fprintf(w, "warning: %s\n", msg)
}

// LogTiming writes one "timing <name> <value>" line. A log failure is a warning.
func (m *Manager) LogTiming(r *Record, name string, value any) {
	m.logTiming(r, name, value)
}

func (m *Manager) logTiming(r *Record, name string, value any) {
	var msg string
	switch v := value.(type) {
	case float64:
		msg = fmt.Sprintf("timing %s %.3f", name, v)
	case int64:
		msg = fmt.Sprintf("timing %s %d", name, v)
	default:
		msg = fmt.Sprintf("timing %s %v", name, v)
	}
	if err := m.log(r, msg); err != nil {
		m.warn(fmt.Sprintf("timing log: %v", err))
	}
}

func (m *Manager) printCapture(r *Record, secs float64, allocated *int64) {
	if m == nil || m.Output == nil {
		return
	}
	if allocated == nil {
		fmt.Fprintf(m.Output, ">> stage %s: capture (%.1fs)\n", r.Name, secs)
		return
	}
	gib := float64(*allocated) / (1024 * 1024 * 1024)
	fmt.Fprintf(m.Output, ">> stage %s: capture (%.1fs, %.1f GiB)\n", r.Name, secs, gib)
}

func statImageDiskSizes(path string) (allocated, apparent int64, err error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, 0, err
	}
	return st.Blocks * 512, st.Size, nil
}

var imageDiskSizes = statImageDiskSizes

func (m *Manager) noteCapture(r *Record, began time.Time, disk string) (secs *float64, allocated, apparent *int64) {
	secs = secondsPtr(m.since(began))
	m.logTiming(r, "capture-seconds", *secs)
	alloc, appar, err := imageDiskSizes(disk)
	if err != nil {
		m.warn(fmt.Sprintf("timing log: image size: %v", err))
		m.printCapture(r, *secs, nil)
		return secs, nil, nil
	}
	m.logTiming(r, "capture-bytes", alloc)
	m.logTiming(r, "capture-apparent-bytes", appar)
	m.printCapture(r, *secs, &alloc)
	return secs, &alloc, &appar
}
