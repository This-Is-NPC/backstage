package machine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in only. Creates its own domains and never adopts an existing VM.
// BACKSTAGE_VM_INSTALL_TEST=1 exercises the ISO path without libguestfs.
// BACKSTAGE_VM_INTEGRATION=1 additionally exercises clone/restore/delete.
func TestRealOmarchyStages(t *testing.T) {
	full := os.Getenv("BACKSTAGE_VM_INTEGRATION") == "1"
	if !full && os.Getenv("BACKSTAGE_VM_INSTALL_TEST") != "1" {
		t.Skip("set BACKSTAGE_VM_INTEGRATION=1 to provision real VMs")
	}
	if full {
		if _, err := exec.LookPath("guestfish"); err != nil {
			t.Fatal("install libguestfs before the full VM acceptance test")
		}
	}
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Minute)
	defer cancel()
	name := "accept-" + randomID()[:8]
	if requested := os.Getenv("BACKSTAGE_TEST_STAGE"); requested != "" {
		if err := ValidateName(requested); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(requested, "accept-") {
			t.Fatal("acceptance stage names must start with accept-")
		}
		name = requested
	}
	cloneName := name + "-clone"
	release, err := m.Store.LockMany(name, cloneName, "image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { release() }()
	if err := m.ensurePool(ctx); err != nil {
		t.Fatal(err)
	}
	spec := DefaultSpec()
	if v := os.Getenv("BACKSTAGE_TEST_OMARCHY"); v != "" {
		spec.Omarchy = v
	}
	if !full {
		source, err := resolveISO(ctx, spec.Omarchy)
		if err != nil {
			t.Fatal(err)
		}
		r := testRecord(name)
		if existing, e := m.Store.Load(name); e == nil {
			r = existing
		}
		r.Domain = "backstage-" + name
		r.Status = "building"
		r.Source = source
		r.Spec = spec
		if err := m.Store.Save(r); err != nil {
			t.Fatal(err)
		}
		t.Logf("installation stage: %s; logs: %s", name, m.Store.Dir(name))
		base, err := m.installBase(ctx, r)
		if err != nil {
			t.Fatalf("installation retained for diagnosis (%s): %v", name, err)
		}
		if err := atomicJSON(m.Store.Root+"/bases/"+baseKey(spec, source)+".json", base.ID); err != nil {
			t.Fatal(err)
		}
		if err := m.Delete(ctx, r); err != nil {
			t.Fatal(err)
		}
		return
	}
	r, err := m.Create(ctx, name, spec)
	if err != nil {
		t.Fatalf("create %s (retained for diagnosis): %v", name, err)
	}
	t.Logf("origin: %s", name)
	g, err := m.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.InSession("printf installed > /home/omarchy/backstage-acceptance"); err != nil {
		t.Fatal(err)
	}
	checks := map[string]string{
		"backstage-check.py": "import os,pathlib; assert os.environ.get('BACKSTAGE_CHAIN') == 'kept'; pathlib.Path('/home/omarchy/backstage-session-ok').touch()",
		"backstage-reuse.py": "import os,pathlib; assert not os.environ.get('BACKSTAGE_CHAIN'); assert pathlib.Path('/home/omarchy/backstage-chain').exists(); pathlib.Path('/home/omarchy/backstage-reuse-ok').touch()",
		"backstage-clean.py": "import pathlib; assert not pathlib.Path('/home/omarchy/backstage-chain').exists(); assert pathlib.Path('/home/omarchy/backstage-acceptance').exists(); pathlib.Path('/home/omarchy/backstage-clean-ok').touch()",
	}
	for file, script := range checks {
		if _, err := g.InSession("printf %s " + quote(script) + " > /home/omarchy/" + file); err != nil {
			t.Fatal(err)
		}
	}
	if _, exists := r.Snapshots["installed"]; !exists {
		if err := m.Snapshot(ctx, r, "installed"); err != nil {
			t.Fatal(err)
		}
		logProvisionTimings(t, filepath.Join(m.Store.Dir(r.Name), "provision.log"),
			"shutdown-seconds", "capture-seconds", "capture-bytes", "capture-apparent-bytes")
	}
	clone, err := m.Store.Load(cloneName)
	if err != nil || clone.Status != "ready" {
		clone, err = m.Clone(ctx, r, cloneName, "installed")
		if err != nil {
			t.Fatal(err)
		}
	} else {
		initial, err := m.Store.Image(clone.Snapshots["initial"])
		if err != nil {
			t.Fatal(err)
		}
		if initial.Source.Image != r.Snapshots["installed"] {
			t.Fatal("existing acceptance clone has a different origin")
		}
	}
	// The CLI must acquire its own stage locks while recording.
	release()
	acceptanceScenes(t, ctx, m, clone)
	release, err = m.Store.LockMany(name, cloneName, "image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	clone, err = m.Store.Load(cloneName)
	if err != nil {
		t.Fatal(err)
	}
	g, err = m.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	cg, err := m.Start(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"cat /etc/machine-id", "cat /etc/hostname", "cat /etc/ssh/ssh_host_ed25519_key.pub"} {
		a, e := g.SSH(command)
		if e != nil {
			t.Fatal(e)
		}
		b, e := cg.SSH(command)
		if e != nil {
			t.Fatal(e)
		}
		if a == b {
			t.Fatalf("clones share identity: %s", command)
		}
	}
	if _, err := cg.Root("test -f /home/omarchy/backstage-acceptance"); err != nil {
		t.Fatal("clone lost snapshot contents")
	}
	if _, err := cg.InSession("rm /home/omarchy/backstage-acceptance"); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore(ctx, clone, "initial"); err != nil {
		t.Fatal(err)
	}
	logProvisionTimings(t, filepath.Join(m.Store.Dir(clone.Name), "provision.log"),
		"restore-stop-seconds", "restore-activate-seconds")
	cg, err = m.Start(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cg.Root("test -f /home/omarchy/backstage-acceptance"); err != nil {
		t.Fatal("restore did not recover initial clone state")
	}
	if err := m.Delete(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(ctx, clone, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(ctx, clone); err != nil {
		t.Fatal("clone depends on deleted origin", err)
	}
	if err := m.Delete(ctx, clone); err != nil {
		t.Fatal(err)
	}
}

func logProvisionTimings(t *testing.T, path string, names ...string) {
	t.Helper()
	logged := provisionTimingLines(t, path)
	for _, line := range logged {
		t.Logf("%s", line)
	}
	joined := strings.Join(logged, "\n")
	for _, name := range names {
		if !strings.Contains(joined, "timing "+name) {
			t.Fatalf("missing timing %s in %s", name, path)
		}
	}
}

func provisionTimingLines(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("provision.log: %v", err)
	}
	var logged []string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, "timing ") {
			logged = append(logged, line)
		}
	}
	return logged
}

func logNewProvisionTimings(t *testing.T, path string, seen *int) []string {
	t.Helper()
	lines := provisionTimingLines(t, path)
	if *seen > len(lines) {
		*seen = 0
	}
	fresh := append([]string(nil), lines[*seen:]...)
	*seen = len(lines)
	for _, line := range fresh {
		t.Logf("%s", line)
	}
	return fresh
}

func timingField(lines []string, name string) string {
	prefix := "timing " + name + " "
	got := ""
	for _, line := range lines {
		if i := strings.Index(line, prefix); i >= 0 {
			got = strings.TrimSpace(line[i+len(prefix):])
		}
	}
	return got
}

func acceptanceScenes(t *testing.T, ctx context.Context, m *Manager, r *Record) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "backstage")
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", bin, "../../cmd/backstage").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	project := map[string]any{"record": map[string]any{"fps": 30, "out": "recordings"}, "vms": map[string]any{"demo": map[string]string{"stage": r.Name}}, "layouts": map[string]any{"solo": map[string]any{"panes": []any{map[string]string{"name": "term"}}}}}
	if err := atomicJSON(filepath.Join(dir, "backstage.json"), project); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mode, after string
		commands          []string
		marker            string
	}{
		{"one", "clean", "", []string{"export BACKSTAGE_CHAIN=kept", "touch /home/omarchy/backstage-chain"}, "backstage-chain"},
		{"two", "continue", "one", []string{"python3 /home/omarchy/backstage-check.py"}, "backstage-session-ok"},
		{"three", "reuse", "", []string{"python3 /home/omarchy/backstage-reuse.py"}, "backstage-reuse-ok"},
		{"four", "clean", "", []string{"python3 /home/omarchy/backstage-clean.py"}, "backstage-clean-ok"},
	} {
		steps := []any{}
		for _, command := range tc.commands {
			steps = append(steps, map[string]any{"action": "run", "target": "term", "value": command, "delay-after": 2})
		}
		steps = append(steps, map[string]any{"action": "wait", "hold": 3})
		start := map[string]string{"mode": tc.mode}
		if tc.after != "" {
			start["after"] = tc.after
		}
		scene := map[string]any{"name": tc.name, "layout": "solo", "vm": "demo", "vm-start": start, "steps": steps}
		path := filepath.Join(dir, "scenes", tc.name+".json")
		if err := atomicJSON(path, scene); err != nil {
			t.Fatal(err)
		}
		out, err := exec.CommandContext(ctx, bin, "play", path).CombinedOutput()
		t.Logf("scene %s:\n%s", tc.name, out)
		if err != nil {
			t.Fatalf("scene %s: %v", tc.name, err)
		}
		current, err := m.Store.Load(r.Name)
		if err != nil {
			t.Fatal(err)
		}
		g, err := m.Start(ctx, current)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := g.Root("test -f /home/omarchy/" + tc.marker); err != nil {
			t.Fatalf("%s failed its on-screen state assertion", tc.name)
		}
		if _, err := os.Stat(filepath.Join(dir, "recordings", tc.name+".facts.json")); err != nil {
			t.Fatal("no provenance", err)
		}
	}
}

// Opt-in. A restored delta must boot with the same guest marker as a
// complete image of that state; delta-on-delta stays incremental below
// the host limit and flattens above it. Deleting the origin stage keeps
// the clone's backing images.
func TestRealDeltaImages(t *testing.T) {
	if os.Getenv("BACKSTAGE_VM_INTEGRATION") != "1" {
		t.Skip("set BACKSTAGE_VM_INTEGRATION=1 to provision real VMs")
	}
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Minute)
	defer cancel()
	name := "accept-" + randomID()[:8]
	cloneName := name + "-clone"
	release, err := m.Store.LockMany(name, cloneName, "image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { release() }()
	r, err := m.Create(ctx, name, DefaultSpec())
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	originGone := false
	t.Cleanup(func() {
		if !originGone {
			_ = m.Delete(context.Background(), r)
		}
	})
	initial, err := m.Store.Image(r.Snapshots["initial"])
	if err != nil || initial.Parent != "" || initial.Schema != ImageSchema {
		t.Fatalf("save-initial must stay complete: %+v %v", initial, err)
	}
	if err := m.Restore(ctx, r, "initial"); err != nil {
		t.Fatal(err)
	}
	g, err := m.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.InSession("printf marked > /home/omarchy/backstage-delta"); err != nil {
		t.Fatal(err)
	}
	if err := m.Snapshot(ctx, r, "marked"); err != nil {
		t.Fatal(err)
	}
	delta, err := m.Store.Image(r.Snapshots["marked"])
	if err != nil || delta.Parent != r.Snapshots["initial"] || delta.Schema != ImageSchemaV2 {
		t.Fatalf("first snapshot should be a delta on initial: %+v %v", delta, err)
	}
	zero := 0
	m.MaxImageDepth = &zero
	if err := m.Snapshot(ctx, r, "marked-full"); err != nil {
		t.Fatal(err)
	}
	full, err := m.Store.Image(r.Snapshots["marked-full"])
	if err != nil || full.Parent != "" || full.Schema != ImageSchema {
		t.Fatalf("marked-full must stay complete: %+v %v", full, err)
	}
	m.MaxImageDepth = nil
	requireDeltaMarker := func(snap string) {
		t.Helper()
		if err := m.Restore(ctx, r, snap); err != nil {
			t.Fatal(err)
		}
		g, err := m.Start(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := g.Root("test -f /home/omarchy/backstage-delta"); err != nil {
			t.Fatalf("restored %s lost the marker", snap)
		}
	}
	requireDeltaMarker("marked")
	requireDeltaMarker("marked-full")
	if err := m.Restore(ctx, r, "marked"); err != nil {
		t.Fatal(err)
	}
	g, err = m.Start(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.InSession("printf marked2 > /home/omarchy/backstage-delta-2"); err != nil {
		t.Fatal(err)
	}
	if err := m.Snapshot(ctx, r, "marked2"); err != nil {
		t.Fatal(err)
	}
	second, err := m.Store.Image(r.Snapshots["marked2"])
	if err != nil || second.Parent != r.Snapshots["marked"] || second.Schema != ImageSchemaV2 {
		t.Fatalf("marked2 should be a delta on marked: %+v %v", second, err)
	}
	one := 1
	m.MaxImageDepth = &one
	if err := m.Restore(ctx, r, "marked2"); err != nil {
		t.Fatal(err)
	}
	originLog := filepath.Join(m.Store.Dir(r.Name), "provision.log")
	seenFlat := len(provisionTimingLines(t, originLog))
	if err := m.Snapshot(ctx, r, "flat"); err != nil {
		t.Fatal(err)
	}
	flat, err := m.Store.Image(r.Snapshots["flat"])
	if err != nil || flat.Parent != "" {
		t.Fatalf("above the limit must flatten: %+v %v", flat, err)
	}
	freshFlat := logNewProvisionTimings(t, originLog, &seenFlat)
	if !strings.Contains(strings.Join(freshFlat, "\n"), "timing capture-fallback "+fallbackDepthLimit) {
		t.Fatalf("flat missing depth-limit: %v", freshFlat)
	}
	clone, err := m.Clone(ctx, r, cloneName, "marked")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = m.Delete(context.Background(), clone)
	})
	if clone.Source.Image != delta.ID {
		t.Fatalf("clone source: %s", clone.Source.Image)
	}
	initClone, err := m.Store.Image(clone.Snapshots["initial"])
	if err != nil || initClone.Parent != "" {
		t.Fatalf("clone initial must stay complete: %+v %v", initClone, err)
	}
	logProvisionTimings(t, filepath.Join(m.Store.Dir(r.Name), "provision.log"))
	if err := m.Delete(ctx, r); err != nil {
		t.Fatal(err)
	}
	originGone = true
	if _, err := m.Store.Image(initial.ID); err != nil {
		t.Fatalf("origin initial json collected: %v", err)
	}
	if _, err := os.Stat(m.Store.imageJSON(initial.ID)); err != nil {
		t.Fatal("origin initial json missing")
	}
	if _, err := os.Stat(initial.Disk); err != nil {
		t.Fatal("origin initial disk collected")
	}
	cg, err := m.Start(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cg.Root("test -f /home/omarchy/backstage-delta"); err != nil {
		t.Fatal("clone lost the marker after origin delete")
	}
	logProvisionTimings(t, filepath.Join(m.Store.Dir(clone.Name), "provision.log"))
}

// Opt-in measurement. Walks from initial (complete, depth 0) through
// max-image-depth+1. A depth-row N measures boot and a guest read of
// the depth-N image; mode, capture and bytes are from the capture in
// that iteration, which produces depth N+1 or a complete image when
// N+1 would pass the limit. The host page cache is not dropped (no
// sudo), so read may underestimate chain cost. A real run sets
// BACKSTAGE_IMAGE_DEPTH=8 (10 iterations; 120-minute context).
func TestMeasureImageDepthChain(t *testing.T) {
	if os.Getenv("BACKSTAGE_VM_DEPTH_MEASURE") != "1" {
		t.Skip("set BACKSTAGE_VM_DEPTH_MEASURE=1 to measure real depth costs")
	}
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Minute)
	defer cancel()
	limit, origin, err := m.imageDepthLimit()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("max-image-depth %d (%s)", limit, origin)
	t.Logf("depth-row N is boot and read of the depth-N image; mode/capture/bytes are the capture that makes depth N+1, or a complete image when N+1 exceeds the limit")
	t.Logf("host page cache is not dropped (no sudo); guest read may underestimate backing-chain cost")
	name := "accept-" + randomID()[:8]
	release, err := m.Store.LockMany(name, "image-catalog")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { release() }()
	r, err := m.Create(ctx, name, DefaultSpec())
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = m.Delete(context.Background(), r)
	})
	if err := m.Restore(ctx, r, "initial"); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(m.Store.Dir(r.Name), "provision.log")
	seen := len(provisionTimingLines(t, logPath))
	for depth := 0; depth <= limit+1; depth++ {
		snap := "depth-" + randomID()[:8]
		began := time.Now()
		g, err := m.Start(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		boot := seconds(time.Since(began))
		if _, err := g.Root("sh -c 'sync; echo 3 > /proc/sys/vm/drop_caches'"); err != nil {
			t.Fatal(err)
		}
		hasBash := true
		if _, err := g.Root("command -v bash"); err != nil {
			hasBash = false
		}
		began = time.Now()
		if hasBash {
			if _, err := g.Root("bash -c 'set -o pipefail; tar -cf - /usr | cat > /dev/null'"); err != nil {
				t.Fatal(err)
			}
		} else if _, err := g.Root("sh -c 'find /usr -xdev -type f -exec cat {} + > /dev/null'"); err != nil {
			t.Fatal(err)
		}
		read := seconds(time.Since(began))
		file := fmt.Sprintf("/home/omarchy/backstage-depth-%d.bin", depth)
		if _, err := g.InSession(fmt.Sprintf("dd if=/dev/urandom of=%s bs=1M count=256 status=none", file)); err != nil {
			t.Fatal(err)
		}
		if err := m.Snapshot(ctx, r, snap); err != nil {
			t.Fatal(err)
		}
		fresh := logNewProvisionTimings(t, logPath, &seen)
		mode := timingField(fresh, "capture-mode")
		capture := timingField(fresh, "capture-seconds")
		bytes := timingField(fresh, "capture-bytes")
		loggedDepth := timingField(fresh, "image-depth")
		if mode == "" || capture == "" || bytes == "" || loggedDepth == "" {
			t.Fatalf("missing capture timings at depth %d: %v", depth, fresh)
		}
		img, err := m.Store.Image(r.Snapshots[snap])
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("depth-row depth=%d mode=%s boot=%g read=%g capture=%s bytes=%s parent=%s", depth, mode, boot, read, capture, bytes, img.Parent)
		if err := m.Restore(ctx, r, snap); err != nil {
			t.Fatal(err)
		}
		seen = len(provisionTimingLines(t, logPath))
	}
}
