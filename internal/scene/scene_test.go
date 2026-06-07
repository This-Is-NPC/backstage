package scene

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindConfig(t *testing.T) {
	root := t.TempDir()
	scenes := filepath.Join(root, "scenes")
	if err := os.MkdirAll(scenes, 0o755); err != nil {
		t.Fatal(err)
	}
	scenePath := filepath.Join(scenes, "01.json")
	if err := os.WriteFile(scenePath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	bs := filepath.Join(root, "backstage.json")
	if err := os.WriteFile(bs, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, dir, err := FindConfig(scenePath)
	if err != nil {
		t.Fatalf("FindConfig: %v", err)
	}
	if cfg != bs {
		t.Errorf("cfg = %s, want %s", cfg, bs)
	}
	if dir != root {
		t.Errorf("dir = %s, want %s", dir, root)
	}

	// none found once the config is gone
	os.Remove(bs)
	if _, _, err := FindConfig(scenePath); err == nil {
		t.Error("expected error when no config exists")
	}
}

func TestExpandAndDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "backstage.json")
	if err := os.WriteFile(cfg, []byte(`{
		"env": {"HOME_DIR": "${PROJECT}/.home", "ALT": "$PROJECT/x"},
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := LoadProject(cfg)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	if got, want := p.Env["HOME_DIR"], dir+"/.home"; got != want {
		t.Errorf("${PROJECT} expand = %s, want %s", got, want)
	}
	if got, want := p.Env["ALT"], dir+"/x"; got != want {
		t.Errorf("$PROJECT expand = %s, want %s", got, want)
	}
	if p.Record.Monitor != defMonitor || p.Record.FPS != defFPS || p.Record.Out != defOut {
		t.Errorf("record defaults not applied: %+v", p.Record)
	}
	if p.Popup.CPS != defCPS || len(p.Popup.Size) != 2 {
		t.Errorf("popup defaults not applied: %+v", p.Popup)
	}
	if p.Term != defTerm {
		t.Errorf("term default = %s, want %s", p.Term, defTerm)
	}
}

func TestResetDefault(t *testing.T) {
	s := &Scene{}
	if !s.ResetEnabled() {
		t.Error("ResetEnabled default should be true")
	}
	no := false
	s.Reset = &no
	if s.ResetEnabled() {
		t.Error("ResetEnabled should be false when set false")
	}
}

func TestValidate(t *testing.T) {
	p := &Project{
		Layouts: map[string]Layout{"solo": {Panes: []Pane{{Name: "t"}}}},
		Aliases: map[string]Alias{"okt-terminal": {Action: "keys", Target: "okt"}},
	}
	ok := &Scene{Name: "ok", Layout: "solo", Steps: []Step{
		{Action: "dialog", Value: "hi"},
		{Action: "okt-terminal", Commands: []string{"m"}}, // alias
	}}
	if err := ok.Validate(p); err != nil {
		t.Errorf("valid scene rejected: %v", err)
	}

	badLayout := &Scene{Name: "x", Layout: "missing", Steps: []Step{{Action: "wait"}}}
	if err := badLayout.Validate(p); err == nil {
		t.Error("expected unknown-layout error")
	}

	badAction := &Scene{Name: "x", Layout: "solo", Steps: []Step{{Action: "frobnicate"}}}
	if err := badAction.Validate(p); err == nil {
		t.Error("expected unknown-action error")
	}

	noSteps := &Scene{Name: "x", Layout: "solo"}
	if err := noSteps.Validate(p); err == nil {
		t.Error("expected no-steps error")
	}
}

func TestManifestPane(t *testing.T) {
	m := &Manifest{
		Panes: map[string]string{"okt": "%1", "agent": "%2"},
		Order: []string{"okt", "agent"},
	}
	if got := m.Pane("agent"); got != "%2" {
		t.Errorf("Pane(agent) = %s, want %%2", got)
	}
	if got := m.Pane("", "nope"); got != "%1" {
		t.Errorf("Pane fallback to first = %s, want %%1", got)
	}
	if got := m.Pane("missing", "okt"); got != "%1" {
		t.Errorf("Pane second candidate = %s, want %%1", got)
	}
}

func TestSafePath(t *testing.T) {
	p := &Project{Dir: t.TempDir()}
	if got, err := p.SafePath("recordings", "demo.mp4"); err != nil || filepath.Dir(got) != filepath.Join(p.Dir, "recordings") {
		t.Fatalf("SafePath valid = %s, %v", got, err)
	}
	for _, bad := range []string{"../x", "/tmp/x"} {
		if _, err := p.SafePath(bad); err == nil {
			t.Fatalf("SafePath(%q) should reject project escape", bad)
		}
	}
	outside := t.TempDir()
	link := filepath.Join(p.Dir, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := p.SafePath("linked", "out.mp4"); err == nil {
		t.Fatal("SafePath should reject symlink escapes")
	}
}

func TestValidateNameAndEnvKey(t *testing.T) {
	if err := ValidateName("scene", "01-intro.ok"); err != nil {
		t.Fatalf("valid scene name rejected: %v", err)
	}
	if err := ValidateName("scene", "../intro"); err == nil {
		t.Fatal("scene name with path separator should be rejected")
	}
	if err := ValidateEnvKey("APP_HOME"); err != nil {
		t.Fatalf("valid env key rejected: %v", err)
	}
	if err := ValidateEnvKey("APP;rm"); err == nil {
		t.Fatal("shell env key should be rejected")
	}
}
