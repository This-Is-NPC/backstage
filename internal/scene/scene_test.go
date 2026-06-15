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
	if p.Popup.Style.FontSize != defPopupFontSize || p.Popup.Style.Title != defPopupTitle ||
		p.Popup.Style.Header != defPopupHeader || p.Popup.Style.Chrome != defPopupChrome ||
		p.Popup.Style.Class != defPopupClass {
		t.Errorf("popup style defaults not applied: %+v", p.Popup.Style)
	}
	if p.Term != defTerm {
		t.Errorf("term default = %s, want %s", p.Term, defTerm)
	}
}

func TestPopupStyleValidation(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	badChrome := write("bad-chrome.json", `{
		"popup": {"style": {"chrome": "loud"}},
		"layouts": {"solo": {"panes": [{"name": "t"}]}}
	}`)
	if _, err := LoadProject(badChrome); err == nil {
		t.Error("expected invalid chrome to fail")
	}

	badClass := write("bad-class.json", `{
		"popup": {"style": {"class": "bad class"}},
		"layouts": {"solo": {"panes": [{"name": "t"}]}}
	}`)
	if _, err := LoadProject(badClass); err == nil {
		t.Error("expected invalid class to fail")
	}

	badTitle := write("bad-title.json", `{
		"popup": {"style": {"title": "safe\u001b]0;evil\u0007"}},
		"layouts": {"solo": {"panes": [{"name": "t"}]}}
	}`)
	if _, err := LoadProject(badTitle); err == nil {
		t.Error("expected control characters in title to fail")
	}
}

func TestPopupClassFor(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	// A valid custom class is returned as-is.
	good := write("good.json", `{"popup": {"style": {"class": "my.popup-1"}}}`)
	if got := PopupClassFor(good); got != "my.popup-1" {
		t.Errorf("PopupClassFor(valid) = %q, want %q", got, "my.popup-1")
	}

	// A class with spaces/shell metacharacters must NOT be passed through to
	// hyprctl even on this tolerant path; fall back to the default class.
	bad := write("bad.json", `{"popup": {"style": {"class": "evil class; rm -rf /"}}}`)
	if got := PopupClassFor(bad); got != defPopupClass {
		t.Errorf("PopupClassFor(unsafe) = %q, want default %q", got, defPopupClass)
	}

	// No class / missing file → default.
	none := write("none.json", `{"popup": {"style": {}}}`)
	if got := PopupClassFor(none); got != defPopupClass {
		t.Errorf("PopupClassFor(empty) = %q, want default %q", got, defPopupClass)
	}
	if got := PopupClassFor(filepath.Join(dir, "missing.json")); got != defPopupClass {
		t.Errorf("PopupClassFor(missing) = %q, want default %q", got, defPopupClass)
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
	dir := t.TempDir()
	live := filepath.Join(dir, "live.sh")
	if err := os.WriteFile(live, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Project{
		Dir:         dir,
		Layouts:     map[string]Layout{"solo": {Panes: []Pane{{Name: "t"}}}, "screen": {Panes: []Pane{}}},
		Aliases:     map[string]Alias{"okt-terminal": {Action: "keys", Target: "okt"}},
		Transitions: map[string]Transition{"chapter": {Live: LiveTransition{Prop: "live.sh"}}},
	}
	ok := &Scene{Name: "ok", Layout: "solo", Steps: []Step{
		{Action: "dialog", Value: "hi"},
		{Action: "okt-terminal", Commands: []string{"m"}}, // alias
		{Action: "transition", Value: "chapter"},
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

	badTransition := &Scene{Name: "x", Layout: "solo", Steps: []Step{{Action: "transition", Value: "missing"}}}
	if err := badTransition.Validate(p); err == nil {
		t.Error("expected unknown-transition error")
	}

	noSteps := &Scene{Name: "x", Layout: "solo"}
	if err := noSteps.Validate(p); err == nil {
		t.Error("expected no-steps error")
	}

	screenOnly := &Scene{Name: "screen", Layout: "screen", Steps: []Step{
		{Action: "transition", Value: "chapter"},
		{Action: "dialog", Value: "hi"},
	}}
	if err := screenOnly.Validate(p); err != nil {
		t.Errorf("screen-only scene rejected: %v", err)
	}
	badPaneAction := &Scene{Name: "x", Layout: "screen", Steps: []Step{{Action: "run", Value: "date"}}}
	if err := badPaneAction.Validate(p); err == nil {
		t.Error("expected pane action on screen-only layout to fail")
	}
}

func TestValidateTransitionModes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "live.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Project{Dir: dir, Transitions: map[string]Transition{
		"offline": {Cmd: "render --out {{out}}"},
		"live":    {Live: LiveTransition{Prop: "live.sh"}},
		"both":    {Cmd: "render --out {{out}}", Live: LiveTransition{Prop: "live.sh"}},
		"empty":   {},
		"badcmd":  {Cmd: "render"},
	}}
	for _, name := range []string{"offline", "live", "both"} {
		if err := p.ValidateTransition(name); err != nil {
			t.Errorf("%s should validate: %v", name, err)
		}
	}
	for _, name := range []string{"empty", "badcmd", "missing"} {
		if err := p.ValidateTransition(name); err == nil {
			t.Errorf("%s should fail validation", name)
		}
	}
	// RenderMode precedence: live wins over offline; an offline-only transition is
	// not live; a malformed offline cmd is rejected even alongside a live block.
	if p.Transitions["live"].RenderMode() != RenderLive {
		t.Error("live transition should report RenderLive")
	}
	if p.Transitions["offline"].RenderMode() != RenderOffline {
		t.Error("offline-only transition should report RenderOffline")
	}
	if p.Transitions["both"].RenderMode() != RenderLive {
		t.Error("transition with both should prefer live (RenderLive)")
	}
}

func TestValidateTransitionOfflineCmdWithLive(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "live.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Project{Dir: dir, Transitions: map[string]Transition{
		// malformed offline cmd (no {{out}}) PLUS a live block: the offline cmd
		// must still be validated rather than masked by the live block.
		"badboth": {Cmd: "render", Live: LiveTransition{Prop: "live.sh"}},
	}}
	if err := p.ValidateTransition("badboth"); err == nil {
		t.Error("offline cmd missing {{out}} should fail even when a live block exists")
	}
}

func TestFontSizeNegativeRejected(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	neg := write("neg-font.json", `{
		"popup": {"style": {"fontSize": -5}},
		"layouts": {"solo": {"panes": [{"name": "t"}]}}
	}`)
	if _, err := LoadProject(neg); err == nil {
		t.Error("negative fontSize should be rejected")
	}
	// explicit 0 is coerced to the default (not rejected).
	zero := write("zero-font.json", `{
		"popup": {"style": {"fontSize": 0}},
		"layouts": {"solo": {"panes": [{"name": "t"}]}}
	}`)
	p, err := LoadProject(zero)
	if err != nil {
		t.Fatalf("fontSize 0 should default, got error: %v", err)
	}
	if p.Popup.Style.FontSize != defPopupFontSize {
		t.Errorf("fontSize 0 should default to %d, got %d", defPopupFontSize, p.Popup.Style.FontSize)
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
	if _, err := p.SafePath("linked", "newdir", "out.mp4"); err == nil {
		t.Fatal("SafePath should reject symlink ancestor escapes with missing child directories")
	}
}

func TestPropCommandUsesProjectContextAndProcessGroup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "live.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Project{Dir: dir, Env: map[string]string{"BACKSTAGE_TEST_ENV": "yes"}}
	cmd, err := p.PropCommand("live.sh", []string{"--flag"})
	if err != nil {
		t.Fatalf("PropCommand: %v", err)
	}
	if cmd.Dir != dir {
		t.Errorf("PropCommand dir = %q, want %q", cmd.Dir, dir)
	}
	if len(cmd.Args) != 2 || cmd.Args[1] != "--flag" {
		t.Errorf("PropCommand args = %v", cmd.Args)
	}
	foundEnv := false
	for _, kv := range cmd.Env {
		if kv == "BACKSTAGE_TEST_ENV=yes" {
			foundEnv = true
			break
		}
	}
	if !foundEnv {
		t.Errorf("PropCommand env missing project env: %v", cmd.Env)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("PropCommand must start props in their own process group")
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
