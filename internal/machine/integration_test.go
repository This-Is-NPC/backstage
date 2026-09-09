package machine

import (
	"context"
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
