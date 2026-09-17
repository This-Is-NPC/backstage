package scene

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInputsDigestIgnoresNarration(t *testing.T) {
	p, s := digestFixture(t, t.TempDir())
	base, err := InputsDigest(p, s, digestOpts())
	if err != nil {
		t.Fatal(err)
	}
	s.Narration = Narration{Language: "pt-BR", Cues: []NarrationCue{
		{ID: "a", Start: 0, End: 1, Text: "changed caption"},
	}}
	s.Audio = map[string]SceneAudio{"room": {File: "missing-audio.wav"}}
	got, err := InputsDigest(p, s, digestOpts())
	if err != nil {
		t.Fatal(err)
	}
	if got != base {
		t.Fatal("narration and audio changed the digest")
	}
}

func TestInputsDigestMovesOnPictureInputs(t *testing.T) {
	dir := t.TempDir()
	p, s := digestFixture(t, dir)
	base, err := InputsDigest(p, s, digestOpts())
	if err != nil {
		t.Fatal(err)
	}

	step := *s
	step.Steps = append(append([]Step(nil), s.Steps...), Step{Action: "wait", DelayAfter: 2})
	if got := mustDigest(t, p, &step, digestOpts()); got == base {
		t.Fatal("step change kept the digest")
	}

	writeFile(t, filepath.Join(dir, "hooks", "reset.sh"), "#!/bin/sh\n# changed\n")
	if got := mustDigest(t, p, s, digestOpts()); got == base {
		t.Fatal("hook content change kept the digest")
	}
	writeFile(t, filepath.Join(dir, "hooks", "reset.sh"), "#!/bin/sh\n")

	writeFile(t, filepath.Join(dir, "props", "ok.sh"), "#!/bin/sh\n# prop\n")
	if got := mustDigest(t, p, s, digestOpts()); got == base {
		t.Fatal("prop content change kept the digest")
	}
	writeFile(t, filepath.Join(dir, "props", "ok.sh"), "#!/bin/sh\n")

	writeFile(t, filepath.Join(dir, "inputs", "data.txt"), "changed\n")
	if got := mustDigest(t, p, s, digestOpts()); got == base {
		t.Fatal("declared input change kept the digest")
	}
	writeFile(t, filepath.Join(dir, "inputs", "data.txt"), "data\n")

	p.Popup.CPS = p.Popup.CPS + 1
	if got := mustDigest(t, p, s, digestOpts()); got == base {
		t.Fatal("popup change kept the digest")
	}
	p.Popup.CPS--

	p.Term = "other-term"
	if got := mustDigest(t, p, s, digestOpts()); got == base {
		t.Fatal("term change kept the digest")
	}
	p.Term = defTerm

	al := p.Aliases["do-prop"]
	al.Target = "shell"
	p.Aliases["do-prop"] = al
	if got := mustDigest(t, p, s, digestOpts()); got == base {
		t.Fatal("used alias change kept the digest")
	}
	al.Target = ""
	p.Aliases["do-prop"] = al

	tr := p.Transitions["fade"]
	tr.Live.Args = []string{"--slow"}
	p.Transitions["fade"] = tr
	if got := mustDigest(t, p, s, digestOpts()); got == base {
		t.Fatal("used transition change kept the digest")
	}
	tr.Live.Args = nil
	p.Transitions["fade"] = tr

	opts := digestOpts()
	opts.Speed = 0.5
	if got := mustDigest(t, p, s, opts); got == base {
		t.Fatal("speed change kept the digest")
	}
	opts = digestOpts()
	opts.ShowStaging = true
	if got := mustDigest(t, p, s, opts); got == base {
		t.Fatal("show-staging change kept the digest")
	}
}

func TestInputsDigestIgnoresUnusedAliasAndTransition(t *testing.T) {
	dir := t.TempDir()
	p, s := digestFixture(t, dir)
	base := mustDigest(t, p, s, digestOpts())

	p.Aliases["unused-alias"] = Alias{Action: "keys", Target: "other"}
	writeFile(t, filepath.Join(dir, "props", "unused.sh"), "#!/bin/sh\n# unused now\n")
	tr := p.Transitions["unused-tr"]
	tr.Live.Args = []string{"nope"}
	p.Transitions["unused-tr"] = tr
	if got := mustDigest(t, p, s, digestOpts()); got != base {
		t.Fatal("unused alias or transition changed the digest")
	}
}

func TestInputsDigestPropAndTransitionDirectAndAliased(t *testing.T) {
	dir := t.TempDir()
	p, s := digestFixture(t, dir)
	base := mustDigest(t, p, s, digestOpts())

	writeFile(t, filepath.Join(dir, "props", "via-alias.sh"), "#!/bin/sh\n# via\n")
	if mustDigest(t, p, s, digestOpts()) == base {
		t.Fatal("aliased prop content change kept the digest")
	}
	writeFile(t, filepath.Join(dir, "props", "via-alias.sh"), "#!/bin/sh\n")

	writeFile(t, filepath.Join(dir, "props", "wipe.sh"), "#!/bin/sh\n# wipe\n")
	if mustDigest(t, p, s, digestOpts()) == base {
		t.Fatal("aliased transition prop change kept the digest")
	}
	writeFile(t, filepath.Join(dir, "props", "wipe.sh"), "#!/bin/sh\n")

	writeFile(t, filepath.Join(dir, "props", "fade.sh"), "#!/bin/sh\n# fade\n")
	if mustDigest(t, p, s, digestOpts()) == base {
		t.Fatal("direct transition prop change kept the digest")
	}
}

func TestInputsDigestDirectoryInput(t *testing.T) {
	dir := t.TempDir()
	p, s := digestFixture(t, dir)
	base := mustDigest(t, p, s, digestOpts())
	writeFile(t, filepath.Join(dir, "inputs", "dir", "nested.txt"), "changed\n")
	if mustDigest(t, p, s, digestOpts()) == base {
		t.Fatal("file inside an inputs directory kept the digest")
	}
}

func TestInputsDigestDirectoryFileSymlink(t *testing.T) {
	dir := t.TempDir()
	p, s := digestFixture(t, dir)
	base := mustDigest(t, p, s, digestOpts())
	nested := filepath.Join(dir, "inputs", "dir", "nested.txt")
	link := filepath.Join(dir, "inputs", "dir", "alias.txt")
	if err := os.Symlink("nested.txt", link); err != nil {
		t.Fatal(err)
	}
	withLink := mustDigest(t, p, s, digestOpts())
	if withLink == base {
		t.Fatal("file symlink inside inputs did not enter the digest")
	}
	writeFile(t, nested, "changed via link\n")
	if mustDigest(t, p, s, digestOpts()) == withLink {
		t.Fatal("changing a symlink target kept the digest")
	}
}

func TestInputsDigestDirectorySymlinkEscapes(t *testing.T) {
	dir := t.TempDir()
	p, s := digestFixture(t, dir)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	writeFile(t, outside, "secret\n")
	link := filepath.Join(dir, "inputs", "dir", "leak.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	_, err := InputsDigest(p, s, digestOpts())
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("escaping symlink: %v", err)
	}
}

func TestInputsDigestStableAcrossWorkspaceMove(t *testing.T) {
	src := t.TempDir()
	writeDigestTree(t, src)
	p1, err := LoadProject(filepath.Join(src, "leaf", "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	s1, err := LoadScene(filepath.Join(src, "leaf", "scenes", "demo.json"))
	if err != nil {
		t.Fatal(err)
	}
	first := mustDigest(t, p1, s1, digestOpts())

	dst := t.TempDir()
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	p2, err := LoadProject(filepath.Join(dst, "leaf", "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	s2, err := LoadScene(filepath.Join(dst, "leaf", "scenes", "demo.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p1.Dir == p2.Dir {
		t.Fatal("move did not change the leaf path")
	}
	if got := mustDigest(t, p2, s2, digestOpts()); got != first {
		t.Fatalf("digest changed after move\nfrom %s\nto   %s", p1.Dir, p2.Dir)
	}
}

func TestInputsDigestStartState(t *testing.T) {
	dir := t.TempDir()
	p, s := digestFixture(t, dir)
	s.VM = "laptop"
	p.VMs = map[string]VMCfg{"laptop": {Stage: "demo", Open: "term", Language: "C.UTF-8"}}
	reuse := mustDigest(t, p, s, DigestOptions{Speed: 1})
	if tok := StartStateToken(s, "img-a"); tok != "" {
		t.Fatalf("reuse token = %q", tok)
	}
	s.VMStart = &VMStart{Mode: "clean"}
	if tok := StartStateToken(s, "img-a"); tok != "clean:img-a" {
		t.Fatalf("clean token = %q", tok)
	}
	cleanA := mustDigest(t, p, s, DigestOptions{Speed: 1, StartState: "clean:img-a"})
	cleanB := mustDigest(t, p, s, DigestOptions{Speed: 1, StartState: "clean:img-b"})
	if cleanA == reuse || cleanA == cleanB {
		t.Fatal("clean image id did not move the digest")
	}
	s.VMStart = &VMStart{Mode: "continue", After: "01-install"}
	if tok := StartStateToken(s, ""); tok != "continue:01-install" {
		t.Fatalf("continue token = %q", tok)
	}
	host := *s
	host.VM = ""
	host.VMStart = nil
	if tok := StartStateToken(&host, "img-a"); tok != "" {
		t.Fatalf("host token = %q", tok)
	}
	opts := DigestOptions{Speed: 1}
	if mustDigest(t, p, &host, opts) == mustDigest(t, p, func() *Scene {
		cp := *s
		cp.VM = "laptop"
		cp.VMStart = &VMStart{Mode: "reuse"}
		return &cp
	}(), opts) {
		t.Fatal("host and reuse scenes should still differ by the vm entry")
	}
	reuseScene := *s
	reuseScene.VM = "laptop"
	reuseScene.VMStart = &VMStart{Mode: "reuse"}
	if StartStateToken(&reuseScene, "img") != "" {
		t.Fatal("reuse should have no start state")
	}
}

func TestInputsDigestMissingInput(t *testing.T) {
	dir := t.TempDir()
	p, s := digestFixture(t, dir)
	s.Inputs = append(s.Inputs, "inputs/missing.txt")
	_, err := InputsDigest(p, s, digestOpts())
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing input: %v", err)
	}
}

func TestValidateInputsConfined(t *testing.T) {
	dir := t.TempDir()
	p := &Project{Dir: dir, Workspace: dir, Layouts: map[string]Layout{"solo": {Panes: []Pane{{Name: "t"}}}}}
	ok := &Scene{Name: "ok", Layout: "solo", Inputs: []string{"props/a.sh"}, Steps: []Step{{Action: "wait"}}}
	if err := ok.Validate(p); err != nil {
		t.Fatalf("relative input rejected: %v", err)
	}
	escape := &Scene{Name: "x", Layout: "solo", Inputs: []string{"../outside"}, Steps: []Step{{Action: "wait"}}}
	if err := escape.Validate(p); err == nil {
		t.Fatal("escaping input accepted")
	}
	empty := &Scene{Name: "x", Layout: "solo", Inputs: []string{"  "}, Steps: []Step{{Action: "wait"}}}
	if err := empty.Validate(p); err == nil {
		t.Fatal("empty input accepted")
	}
}

func digestOpts() DigestOptions {
	return DigestOptions{Speed: 1}
}

func mustDigest(t *testing.T, p *Project, s *Scene, opts DigestOptions) string {
	t.Helper()
	sum, err := InputsDigest(p, s, opts)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

func digestFixture(t *testing.T, dir string) (*Project, *Scene) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "hooks", "reset.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(dir, "hooks", "setup.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(dir, "props", "ok.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(dir, "props", "via-alias.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(dir, "props", "fade.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(dir, "props", "wipe.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(dir, "props", "unused.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(dir, "inputs", "data.txt"), "data\n")
	writeFile(t, filepath.Join(dir, "inputs", "dir", "nested.txt"), "nested\n")
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"record": {"fps": 30},
		"env": {"HOME_DIR": "${PROJECT}/.home"},
		"hooks": {"reset": "hooks/reset.sh", "setup": "hooks/setup.sh"},
		"aliases": {
			"do-prop": {"action": "prop"},
			"do-fade": {"action": "transition"},
			"unused-alias": {"action": "wait"}
		},
		"transitions": {
			"fade": {"live": {"prop": "props/fade.sh"}},
			"wipe": {"live": {"prop": "props/wipe.sh"}},
			"unused-tr": {"live": {"prop": "props/unused.sh"}}
		},
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash ${WORKSPACE}/bin/tool"}]}}
	}`)
	p, err := LoadProject(filepath.Join(dir, "backstage.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Scene{
		Name:   "demo",
		Layout: "solo",
		Inputs: []string{"inputs/data.txt", "inputs/dir"},
		Steps: []Step{
			{Action: "wait"},
			{Action: "prop", Value: "props/ok.sh"},
			{Action: "do-prop", Value: "props/via-alias.sh"},
			{Action: "transition", Value: "fade"},
			{Action: "do-fade", Value: "wipe"},
		},
		Narration: Narration{Language: "en", Cues: []NarrationCue{
			{ID: "a", Start: 0, End: 1, Text: "hello"},
		}},
	}
	return p, s
}

func writeDigestTree(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "bin", "tool"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(root, "hooks", "reset.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(root, "leaf", "props", "ok.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(root, "leaf", "inputs", "data.txt"), "data\n")
	writeFile(t, filepath.Join(root, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash ${WORKSPACE}/bin/tool"}]}},
		"env": {"LIB": "${WORKSPACE}/lib"},
		"hooks": {"reset": "hooks/reset.sh"}
	}`)
	writeFile(t, filepath.Join(root, "leaf", "backstage.json"), `{
		"extends": "../backstage.json",
		"env": {"HOME_DIR": "${PROJECT}/.home"},
		"record": {"fps": 30}
	}`)
	writeFile(t, filepath.Join(root, "leaf", "scenes", "demo.json"), `{
		"name": "demo",
		"layout": "solo",
		"inputs": ["inputs/data.txt"],
		"steps": [{"action": "prop", "value": "props/ok.sh"}]
	}`)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, body, 0o644)
	})
}
