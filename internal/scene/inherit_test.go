package scene

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadProjectWithoutExtendsMatchesBefore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hooks", "reset.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"env": {"HOME_DIR": "${PROJECT}/.home", "ALT": "$PROJECT/x"},
		"hooks": {"reset": "hooks/reset.sh"},
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}}
	}`)
	p, err := LoadProject(filepath.Join(dir, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Workspace != p.Dir {
		t.Fatalf("Workspace = %q, want Dir %q", p.Workspace, p.Dir)
	}
	if p.Extends != "" {
		t.Fatalf("Extends = %q, want empty", p.Extends)
	}
	if got, want := p.Env["HOME_DIR"], dir+"/.home"; got != want {
		t.Fatalf("${PROJECT} = %s, want %s", got, want)
	}
	if _, err := p.InputPath("hooks/reset.sh"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.InputPath("../x"); err == nil {
		t.Fatal("project without extends should still reject leaf escapes")
	}
}

func TestExtendsRootSkipsValidation(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}}
	}`)
	leaf := filepath.Join(ws, "en")
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{
		"extends": "../backstage.json",
		"record": {"out": "../escape"}
	}`)
	if _, err := LoadProject(filepath.Join(leaf, "backstage.json")); err == nil {
		t.Fatal("escaping record.out should fail LoadProject")
	}
	root, err := ExtendsRoot(filepath.Join(leaf, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(ws)
	if err != nil {
		t.Fatal(err)
	}
	if root != want {
		t.Fatalf("ExtendsRoot %s, want %s", root, want)
	}
}

func TestExtendsChainAndWorkspace(t *testing.T) {
	ws := t.TempDir()
	lang := filepath.Join(ws, "lang")
	leaf := filepath.Join(lang, "pt")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{
		"term": "ghostty",
		"layouts": {"solo": {"panes": [{"name": "t"}]}}
	}`)
	writeFile(t, filepath.Join(lang, "backstage.json"), `{
		"extends": "../backstage.json",
		"record": {"fps": 60}
	}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{
		"extends": "../backstage.json",
		"term": "kitty"
	}`)
	p, err := LoadProject(filepath.Join(leaf, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Workspace != ws {
		t.Fatalf("Workspace = %q, want %q", p.Workspace, ws)
	}
	if p.Dir != leaf {
		t.Fatalf("Dir = %q, want %q", p.Dir, leaf)
	}
	if p.Term != "kitty" {
		t.Fatalf("term = %q, want kitty", p.Term)
	}
	if p.Record.FPS != 60 {
		t.Fatalf("fps = %d, want 60", p.Record.FPS)
	}
	if p.Origins["term"] != filepath.Join(leaf, "backstage.json") {
		t.Fatalf("term origin = %s", p.Origins["term"])
	}
	if p.Origins["record.fps"] != filepath.Join(lang, "backstage.json") {
		t.Fatalf("fps origin = %s", p.Origins["record.fps"])
	}
}

func TestExtendsValidation(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{"extends": "../missing.json"}`)
	if _, err := LoadProject(filepath.Join(leaf, "backstage.json")); err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Fatalf("missing extends: %v", err)
	}

	writeFile(t, filepath.Join(ws, "not-config.json"), `{}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{"extends": "../not-config.json"}`)
	if _, err := LoadProject(filepath.Join(leaf, "backstage.json")); err == nil || !strings.Contains(err.Error(), configName) {
		t.Fatalf("wrong filename: %v", err)
	}

	writeFile(t, filepath.Join(leaf, "backstage.json"), `{"extends": ".."}`)
	if _, err := LoadProject(filepath.Join(leaf, "backstage.json")); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("directory extends: %v", err)
	}

	sib := filepath.Join(ws, "sib")
	writeFile(t, filepath.Join(sib, "backstage.json"), `{}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{"extends": "../sib/backstage.json"}`)
	if _, err := LoadProject(filepath.Join(leaf, "backstage.json")); err == nil || !strings.Contains(err.Error(), "not a strict ancestor") {
		t.Fatalf("non-ancestor: %v", err)
	}

	abs := filepath.Join(ws, "backstage.json")
	writeFile(t, abs, `{"layouts": {"solo": {"panes": [{"name": "t"}]}}}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{"extends": "`+abs+`"}`)
	if _, err := LoadProject(filepath.Join(leaf, "backstage.json")); err == nil || !strings.Contains(err.Error(), "must be relative") {
		t.Fatalf("absolute extends: %v", err)
	}
}

func TestNamedMapReplaceWholeAndNullRemoves(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"vms": {
			"desktop": {"stage": "root", "language": "en_US.UTF-8"},
			"extra": {"stage": "keep"}
		}
	}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{
		"extends": "../backstage.json",
		"vms": {
			"desktop": {"stage": "leaf"},
			"extra": null
		}
	}`)
	p, err := LoadProject(filepath.Join(leaf, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.VMs["desktop"].Stage != "leaf" || p.VMs["desktop"].Language != "" {
		t.Fatalf("desktop not replaced whole: %+v", p.VMs["desktop"])
	}
	if _, ok := p.VMs["extra"]; ok {
		t.Fatal("null entry should remove inherited extra")
	}
}

func TestEmptyEnvValueIsKept(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"env": {"FOO": ""}
	}`)
	p, err := LoadProject(filepath.Join(dir, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := p.Env["FOO"]
	if !ok || got != "" {
		t.Fatalf("empty env without extends: ok=%v value=%q env=%v", ok, got, p.Env)
	}
}

func TestLeafEmptyEnvOverridesInherited(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"env": {"FOO": "from-parent", "KEEP": "yes"}
	}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{
		"extends": "../backstage.json",
		"env": {"FOO": ""}
	}`)
	p, err := LoadProject(filepath.Join(leaf, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := p.Env["FOO"]
	if !ok || got != "" {
		t.Fatalf("leaf empty env: ok=%v value=%q env=%v", ok, got, p.Env)
	}
	if p.Env["KEEP"] != "yes" {
		t.Fatalf("inherited KEEP: %+v", p.Env)
	}
}

func TestInheritedAbsolutePathIsRefused(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	reset := filepath.Join(ws, "hooks", "reset.sh")
	writeFile(t, reset, "#!/bin/sh\n")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"hooks": {"reset": "`+reset+`"}
	}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{"extends": "../backstage.json"}`)
	err := mustLoadErr(t, filepath.Join(leaf, "backstage.json"))
	if !strings.Contains(err.Error(), "hooks.reset") || !strings.Contains(err.Error(), "must be relative") {
		t.Fatalf("absolute inherited path: %v", err)
	}
}

func mustLoadErr(t *testing.T, path string) error {
	t.Helper()
	_, err := LoadProject(path)
	if err == nil {
		t.Fatal("expected load error")
	}
	return err
}

func TestSettingsFieldOverrideAndEmptyClears(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	writeFile(t, filepath.Join(ws, "hooks", "setup.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"record": {"fps": 60, "monitor": "DP-1"},
		"popup": {"cps": 40, "style": {"fontSize": 22, "chrome": "minimal"}},
		"render": {"w": 1280, "h": 720},
		"hooks": {"setup": "hooks/setup.sh", "reset": "hooks/reset.sh"},
		"term": "alacritty"
	}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{
		"extends": "../backstage.json",
		"record": {"fps": 30},
		"popup": {"style": {"fontSize": 0}},
		"render": {"w": 0},
		"hooks": {"setup": ""},
		"term": ""
	}`)
	p, err := LoadProject(filepath.Join(leaf, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Record.FPS != 30 || p.Record.Monitor != "DP-1" {
		t.Fatalf("record merge: %+v", p.Record)
	}
	if p.Popup.CPS != 40 || p.Popup.Style.Chrome != "minimal" {
		t.Fatalf("popup field merge: %+v", p.Popup)
	}
	if p.Popup.Style.FontSize != defPopupFontSize {
		t.Fatalf("fontSize 0 should default, got %d", p.Popup.Style.FontSize)
	}
	if p.Render.W != 0 || p.Render.H != 720 {
		t.Fatalf("render clear: %+v", p.Render)
	}
	if p.Hooks.Setup != "" || p.Hooks.Reset == "" {
		t.Fatalf("hooks clear: %+v", p.Hooks)
	}
	if p.Term != defTerm {
		t.Fatalf("term clear = %q, want default %q", p.Term, defTerm)
	}
}

func TestInheritedFileRefsResolveToSameFile(t *testing.T) {
	ws := t.TempDir()
	a := filepath.Join(ws, "a")
	b := filepath.Join(ws, "b")
	writeFile(t, filepath.Join(ws, "hooks", "reset.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(ws, "templates", "shared", "index.html"), "<html></html>")
	writeFile(t, filepath.Join(ws, "props", "live.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"hooks": {"reset": "hooks/reset.sh"},
		"templates": {"shared": {"entry": "templates/shared/index.html"}},
		"transitions": {"slide": {"live": {"prop": "props/live.sh"}}}
	}`)
	writeFile(t, filepath.Join(a, "backstage.json"), `{"extends": "../backstage.json"}`)
	writeFile(t, filepath.Join(b, "backstage.json"), `{"extends": "../backstage.json"}`)
	pa, err := LoadProject(filepath.Join(a, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	pb, err := LoadProject(filepath.Join(b, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if pa.Hooks.Reset != "../hooks/reset.sh" {
		t.Fatalf("rewritten reset = %q", pa.Hooks.Reset)
	}
	ra, err := pa.InputPath(pa.Hooks.Reset)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := pb.InputPath(pb.Hooks.Reset)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(ws, "hooks", "reset.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if ra != want || rb != want {
		t.Fatalf("resolved hooks differ: %s %s want %s", ra, rb, want)
	}
	if pa.Templates["shared"].Entry != "../templates/shared/index.html" {
		t.Fatalf("template entry = %q", pa.Templates["shared"].Entry)
	}
	if pa.Transitions["slide"].Live.Prop != "../props/live.sh" {
		t.Fatalf("live.prop = %q", pa.Transitions["slide"].Live.Prop)
	}
}

func TestCommandsAreNotRewritten(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cwd": "work", "cmd": "bash ${WORKSPACE}/tools/run"}]}},
		"transitions": {"fade": {"cmd": "render --root ${WORKSPACE} --out {{out}}"}}
	}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{"extends": "../backstage.json"}`)
	p, err := LoadProject(filepath.Join(leaf, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	pane := p.Layouts["solo"].Panes[0]
	if pane.Cwd != "work" {
		t.Fatalf("cwd rewritten: %q", pane.Cwd)
	}
	if pane.Cmd != "bash "+ws+"/tools/run" {
		t.Fatalf("cmd = %q", pane.Cmd)
	}
	if p.Transitions["fade"].Cmd != "render --root "+ws+" --out {{out}}" {
		t.Fatalf("transition cmd = %q", p.Transitions["fade"].Cmd)
	}
}

func TestWorkspaceAndProjectExpandInEnv(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"env": {"W": "${WORKSPACE}/shared", "P": "${PROJECT}/local", "WS": "$WORKSPACE/x"}
	}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{"extends": "../backstage.json"}`)
	p, err := LoadProject(filepath.Join(leaf, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Env["W"] != ws+"/shared" || p.Env["WS"] != ws+"/x" {
		t.Fatalf("workspace env: %+v", p.Env)
	}
	if p.Env["P"] != leaf+"/local" {
		t.Fatalf("project env: %+v", p.Env)
	}
}

func TestCommandExpandsOnlyWorkspaceBrace(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "echo $WORKSPACE ${PROJECT} ${WORKSPACE}"}]}}
	}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{"extends": "../backstage.json"}`)
	p, err := LoadProject(filepath.Join(leaf, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	got := p.Layouts["solo"].Panes[0].Cmd
	want := "echo $WORKSPACE ${PROJECT} " + ws
	if got != want {
		t.Fatalf("cmd = %q, want %q", got, want)
	}
}

func TestInputEscapeWorkspaceAndOutputEscapeLeaf(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{"layouts": {"solo": {"panes": [{"name": "t"}]}}}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{
		"extends": "../backstage.json",
		"hooks": {"reset": "../../outside.sh"}
	}`)
	_, err := LoadProject(filepath.Join(leaf, "backstage.json"))
	if err == nil || !strings.Contains(err.Error(), "hooks.reset") || !strings.Contains(err.Error(), "declared in") {
		t.Fatalf("input escape: %v", err)
	}

	writeFile(t, filepath.Join(leaf, "backstage.json"), `{
		"extends": "../backstage.json",
		"record": {"out": "../sibling-out"}
	}`)
	_, err = LoadProject(filepath.Join(leaf, "backstage.json"))
	if err == nil || !strings.Contains(err.Error(), "record.out") || !strings.Contains(err.Error(), "declared in") {
		t.Fatalf("output escape: %v", err)
	}
}

func TestInputSymlinkEscapeWorkspace(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{"layouts": {"solo": {"panes": [{"name": "t"}]}}}`)
	if err := os.Symlink(outside, filepath.Join(leaf, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{
		"extends": "../backstage.json",
		"hooks": {"reset": "linked/secret.sh"}
	}`)
	_, err := LoadProject(filepath.Join(leaf, "backstage.json"))
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink escape: %v", err)
	}
}

func TestPopupClassForReadsMergedChain(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{
		"popup": {"style": {"class": "from.parent"}}
	}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{
		"extends": "../backstage.json",
		"popup": {"style": {"chrome": "loud"}}
	}`)
	if got := PopupClassFor(filepath.Join(leaf, "backstage.json")); got != "from.parent" {
		t.Fatalf("merged class = %q", got)
	}
}

func TestNegativeFontSizeRejectedAfterMerge(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"popup": {"style": {"fontSize": -3}}
	}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{"extends": "../backstage.json"}`)
	if _, err := LoadProject(filepath.Join(leaf, "backstage.json")); err == nil {
		t.Fatal("merged negative fontSize should fail")
	}
}

func TestRenderThreadsMergeFieldByField(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"render": {"threads": {"prepare": 2, "encode": 8}}
	}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{
		"extends": "../backstage.json",
		"render": {"threads": {"encode": 4}}
	}`)
	p, err := LoadProject(filepath.Join(leaf, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Render.Threads.Prepare != 2 || p.Render.Threads.Encode != 4 || p.Render.Threads.Filter != 0 {
		t.Fatalf("threads merge replaced the object: %+v", p.Render.Threads)
	}
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{
		"extends": "../backstage.json",
		"render": {"threads": {"prepare": 0}}
	}`)
	p, err = LoadProject(filepath.Join(leaf, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Render.Threads.Prepare != 0 || p.Render.Threads.Encode != 8 {
		t.Fatalf("prepare 0 should clear and keep encode: %+v", p.Render.Threads)
	}
}

func TestHighRenderThreadsAccepted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"render": {"threads": {"encode": 999}}
	}`)
	p, err := LoadProject(filepath.Join(dir, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Render.Threads.Encode != 999 {
		t.Fatal(p.Render.Threads)
	}
}

func TestNegativeRenderWorkersRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"render": {"workers": -1}
	}`)
	if _, err := LoadProject(filepath.Join(dir, "backstage.json")); err == nil {
		t.Fatal("negative render.workers should fail")
	}
}

func TestRenderWorkersMergeFieldByField(t *testing.T) {
	ws := t.TempDir()
	leaf := filepath.Join(ws, "leaf")
	writeFile(t, filepath.Join(ws, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"render": {"workers": 4, "threads": {"encode": 8}}
	}`)
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{
		"extends": "../backstage.json",
		"render": {"workers": 2}
	}`)
	p, err := LoadProject(filepath.Join(leaf, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Render.Workers != 2 || p.Render.Threads.Encode != 8 {
		t.Fatalf("workers merge replaced threads: %+v", p.Render)
	}
	writeFile(t, filepath.Join(leaf, "backstage.json"), `{
		"extends": "../backstage.json",
		"render": {"workers": 0}
	}`)
	p, err = LoadProject(filepath.Join(leaf, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Render.Workers != 0 || p.Render.Threads.Encode != 8 {
		t.Fatalf("workers 0 should clear and keep encode: %+v", p.Render)
	}
}

func TestNegativeRenderThreadsRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t"}]}},
		"render": {"threads": {"encode": -1}}
	}`)
	if _, err := LoadProject(filepath.Join(dir, "backstage.json")); err == nil {
		t.Fatal("negative render.threads should fail")
	}
}
