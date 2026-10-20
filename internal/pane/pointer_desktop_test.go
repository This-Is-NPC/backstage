package pane

import (
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/facts"
)

// This opt-in check changes only a temporary output. It never moves the cursor.
func TestPointerTraceOnRotatedDesktop(t *testing.T) {
	if os.Getenv("BACKSTAGE_DESKTOP_TEST") != "1" {
		t.Skip("set BACKSTAGE_DESKTOP_TEST=1 to check pointer capture and output transforms")
	}
	hypr := func(args ...string) []byte {
		t.Helper()
		b, err := exec.Command("hyprctl", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("hyprctl %v: %v: %s", args, err, b)
		}
		return b
	}
	var original []desktopMonitor
	if err := json.Unmarshal(hypr("-j", "monitors"), &original); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("backstage-pointer-%d", time.Now().UnixNano())
	hypr("output", "create", "headless", name)
	t.Cleanup(func() {
		_ = exec.Command("hyprctl", "output", "remove", name).Run()
	})
	for transform := range 8 {
		hypr("eval", fmt.Sprintf(`hl.monitor({output=%q,mode="640x360@60",position="10000x10000",scale=1,transform=%d})`, name, transform))
		var monitor desktopMonitor
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			var outputs []desktopMonitor
			if err := json.Unmarshal(hypr("-j", "monitors"), &outputs); err != nil {
				t.Fatal(err)
			}
			for _, m := range outputs {
				if m.Name == name {
					monitor = m
				}
			}
			if monitor.Transform == transform && monitor.Width == 640 && monitor.Height == 360 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if monitor.Transform != transform || monitor.Width != 640 {
			t.Fatalf("virtual output not configured: %+v", monitor)
		}
		w, h := monitor.pixels()
		// A screenshot waits for a compositor frame after changing the mode.
		// Querying monitor metadata alone can precede cursor reconfiguration.
		imagePath := filepath.Join(t.TempDir(), "output.png")
		if b, err := exec.Command("grim", "-o", name, imagePath).CombinedOutput(); err != nil {
			t.Fatalf("grim: %v: %s", err, b)
		}
		file, err := os.Open(imagePath)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(file)
		_ = file.Close()
		if err != nil || cfg.Width != int(w) || cfg.Height != int(h) {
			t.Fatalf("capture transform %d: %dx%d vs %.0fx%.0f: %v", transform, cfg.Width, cfg.Height, w, h, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	var stops []func() []facts.PointerSample
	for _, monitor := range original {
		stops = append(stops, (&Tmux{Monitor: monitor.Name}).TracePointer(ctx, time.Now()))
	}
	<-ctx.Done()
	found := false
	for _, stop := range stops {
		for _, sample := range stop() {
			if sample.Pointer != nil {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("native desktop stream supplied no pointer positions")
	}
}

func TestPointerTraceReadsKernelButtons(t *testing.T) {
	if os.Getenv("BACKSTAGE_DESKTOP_TEST") != "1" {
		t.Skip("set BACKSTAGE_DESKTOP_TEST=1 to check kernel pointer events")
	}
	if f, err := os.OpenFile("/dev/uinput", os.O_WRONLY, 0); err == nil {
		_ = f.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// The button-only device has no axes and is not a usable libinput mouse.
		// Synthetic IPC positions isolate this kernel test from the user's cursor.
		source := "__name__='pointer_test'\n" + pointerScript + "\nquery=lambda command: [{'name':'test','width':800,'height':600,'scale':1}] if command=='monitors' else {'x':200,'y':150}\nmain()\n"
		stop := tracePointer(exec.CommandContext(ctx, "python3", "-u", "-c", source, "test"), cancel, time.Now(), false)
		b, err := exec.CommandContext(ctx, "python3", "-c", `import fcntl,os,struct,time
fd=os.open('/dev/uinput',os.O_WRONLY)
try:
 fcntl.ioctl(fd,0x40045564,1)
 fcntl.ioctl(fd,0x40045565,0x110)
 os.write(fd,struct.pack('80sHHHHI',b'backstage pointer validation',3,0,0,1,0)+bytes(1024))
 fcntl.ioctl(fd,0x5501)
 time.sleep(1.3)
 for value in [1,0]:
  os.write(fd,struct.pack('llHHi',0,0,1,0x110,value)+struct.pack('llHHi',0,0,0,0,0))
  time.sleep(.1)
finally:
 fcntl.ioctl(fd,0x5502)
 os.close(fd)
`).CombinedOutput()
		samples := stop()
		if err != nil {
			t.Fatalf("temporary pointer: %v: %s", err, b)
		}
		for _, sample := range samples {
			if sample.Pointer != nil && sample.Pointer.Click {
				return
			}
		}
		t.Fatal("kernel mouse press missing from trace")
	}
}
