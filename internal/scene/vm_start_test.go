package scene

import "testing"

func TestVMStartPolicies(t *testing.T) {
	p := &Project{VMs: map[string]VMCfg{"managed": {Stage: "demo"}, "external": {Domain: "external", User: "u"}}}
	truth := true
	for _, tc := range []struct {
		name, vm   string
		start      *VMStart
		reset      *bool
		fresh, bad bool
	}{
		{"legacy", "external", nil, nil, false, false},
		{"clean", "managed", &VMStart{Mode: "clean"}, nil, false, false},
		{"snapshot", "managed", &VMStart{Mode: "clean", Snapshot: "installed"}, nil, false, false},
		{"reuse", "managed", &VMStart{Mode: "reuse"}, nil, false, false},
		{"continue", "managed", &VMStart{Mode: "continue", After: "one"}, nil, false, false},
		{"external-clean", "external", &VMStart{Mode: "clean"}, nil, false, true},
		{"no-after", "managed", &VMStart{Mode: "continue"}, nil, false, true},
		{"reset-continue", "managed", &VMStart{Mode: "continue", After: "one"}, &truth, false, true},
		{"fresh-continue", "managed", &VMStart{Mode: "continue", After: "one"}, nil, true, true},
		{"invalid", "managed", &VMStart{Mode: "unknown"}, nil, false, true},
		{"reuse-snapshot", "managed", &VMStart{Mode: "reuse", Snapshot: "initial"}, nil, false, true},
		{"no-vm", "", &VMStart{Mode: "reuse"}, nil, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Scene{Name: "two", VM: tc.vm, VMStart: tc.start, Reset: tc.reset, Fresh: tc.fresh}
			err := s.ValidateVMStart(p)
			if (err != nil) != tc.bad {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
}

func TestManagedReferenceRejectsConnectionOverrides(t *testing.T) {
	for _, vm := range []VMCfg{{Stage: "demo", Domain: "d"}, {Stage: "demo", User: "u"}, {Stage: "demo", Admin: "a"}, {Stage: "demo", Key: "k"}, {Stage: "demo", URI: "qemu:///session"}} {
		p := &Project{VMs: map[string]VMCfg{"demo": vm}}
		p.applyDefaults()
		if p.ValidateConfig() == nil {
			t.Fatalf("accepted ambiguous reference %+v", vm)
		}
	}
	p := &Project{VMs: map[string]VMCfg{"demo": {Stage: "demo", Open: "app", Language: "C.UTF-8", Recorder: "framebuffer"}}}
	p.applyDefaults()
	if err := p.ValidateConfig(); err != nil {
		t.Fatal(err)
	}
}
