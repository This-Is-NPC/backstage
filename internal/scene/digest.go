package scene

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DigestOptions are the take-level values that change the picture without
// living on the scene file.
type DigestOptions struct {
	Speed       float64
	ShowStaging bool
	// StartState is "clean:<image-id>" after a clean restore, "continue:<after>"
	// for a live continuation, and empty for reuse and for a scene with no VM.
	StartState string
}

// fileRef is one hashed input, keyed by a workspace-relative path so moving
// the workspace does not change the digest.
type fileRef struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type digestVM struct {
	Stage    string `json:"stage,omitempty"`
	Open     string `json:"open,omitempty"`
	Language string `json:"language,omitempty"`
	Recorder string `json:"recorder,omitempty"`
}

type digestTransition struct {
	Cmd  string         `json:"cmd,omitempty"`
	Live LiveTransition `json:"live,omitempty"`
	Prop *fileRef       `json:"prop,omitempty"`
	FPS  int            `json:"fps"`
	W    int            `json:"w"`
	H    int            `json:"h"`
}

type digestDoc struct {
	Scene       Scene                       `json:"scene"`
	VM          *digestVM                   `json:"vm,omitempty"`
	Layout      Layout                      `json:"layout"`
	Popup       PopupCfg                    `json:"popup"`
	Term        string                      `json:"term"`
	Steps       []Step                      `json:"steps"`
	Aliases     map[string]Alias            `json:"aliases,omitempty"`
	Transitions map[string]digestTransition `json:"transitions,omitempty"`
	Hook        *fileRef                    `json:"hook,omitempty"`
	Props       []fileRef                   `json:"props,omitempty"`
	Inputs      []fileRef                   `json:"inputs,omitempty"`
	Env         map[string]string           `json:"env,omitempty"`
	RecordFPS   int                         `json:"record-fps"`
	Speed       float64                     `json:"speed"`
	ShowStaging bool                        `json:"show-staging"`
	StartState  string                      `json:"start-state,omitempty"`
}

// InputsDigest returns the SHA-256 hex of a canonical encoding of what changes
// the picture. It reads files and does not run the scene. Status (A6) can call
// it without a recorder.
func InputsDigest(p *Project, s *Scene, opts DigestOptions) (string, error) {
	doc, err := digestDocument(p, s, opts)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("inputs digest: %w", err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// ResolveStep expands a configured alias the same way the engine does: the
// alias replaces the action, and supplies a default target when the step
// leaves target empty.
func (p *Project) ResolveStep(st Step) (action, target string) {
	action, target = st.Action, st.Target
	if p == nil {
		return action, target
	}
	if al, ok := p.Aliases[action]; ok {
		action = al.Action
		if target == "" {
			target = al.Target
		}
	}
	return action, target
}

// StartStateToken is the digest start-state field. imageID is the restored
// catalog image after a clean Begin; it is ignored for continue and reuse.
func StartStateToken(s *Scene, imageID string) string {
	if s == nil || s.VM == "" {
		return ""
	}
	switch s.VMStartMode() {
	case "clean":
		return "clean:" + imageID
	case "continue":
		if s.VMStart != nil {
			return "continue:" + s.VMStart.After
		}
	}
	return ""
}

func digestDocument(p *Project, s *Scene, opts DigestOptions) (digestDoc, error) {
	if p == nil || s == nil {
		return digestDoc{}, fmt.Errorf("inputs digest: project and scene are required")
	}
	snap := *s
	snap.Narration = Narration{}
	snap.Audio = nil

	layout, err := digestLayout(p, s)
	if err != nil {
		return digestDoc{}, err
	}
	steps, aliases, props, transitions, err := digestSteps(p, s)
	if err != nil {
		return digestDoc{}, err
	}
	hook, err := digestRunningHook(p, s)
	if err != nil {
		return digestDoc{}, err
	}
	inputs, err := digestDeclaredInputs(p, s)
	if err != nil {
		return digestDoc{}, err
	}
	env := digestEnv(p)
	doc := digestDoc{
		Scene:       snap,
		VM:          digestResolvedVM(p, s),
		Layout:      layout,
		Popup:       p.Popup,
		Term:        p.Term,
		Steps:       steps,
		Aliases:     aliases,
		Transitions: transitions,
		Hook:        hook,
		Props:       props,
		Inputs:      inputs,
		Env:         env,
		RecordFPS:   p.Record.FPS,
		Speed:       opts.Speed,
		ShowStaging: opts.ShowStaging,
		StartState:  opts.StartState,
	}
	return doc, nil
}

func digestLayout(p *Project, s *Scene) (Layout, error) {
	if s.Type == "visual" || s.LayoutName() == "" {
		return Layout{}, nil
	}
	layout, ok := p.Layouts[s.LayoutName()]
	if !ok {
		return Layout{}, fmt.Errorf("inputs digest: layout %q not in config", s.LayoutName())
	}
	panes := make([]Pane, len(layout.Panes))
	copy(panes, layout.Panes)
	for i := range panes {
		panes[i].Cmd = p.foldLocation(panes[i].Cmd)
	}
	layout.Panes = panes
	return layout, nil
}

func digestResolvedVM(p *Project, s *Scene) *digestVM {
	if s.VM == "" {
		return nil
	}
	cfg := p.VMs[s.VM]
	recorder := cfg.Recorder
	if s.Recorder != "" {
		recorder = s.Recorder
	}
	if recorder == "" {
		recorder = "inside"
	}
	return &digestVM{
		Stage:    cfg.Stage,
		Open:     cfg.Open,
		Language: cfg.Language,
		Recorder: recorder,
	}
}

func digestSteps(p *Project, s *Scene) ([]Step, map[string]Alias, []fileRef, map[string]digestTransition, error) {
	steps := make([]Step, 0, len(s.Steps))
	aliases := map[string]Alias{}
	propSeen := map[string]fileRef{}
	transitions := map[string]digestTransition{}
	fps, w, h := p.ResolveRenderDims()
	for _, st := range s.Steps {
		if al, ok := p.Aliases[st.Action]; ok {
			aliases[st.Action] = al
		}
		action, target := p.ResolveStep(st)
		eff := st
		eff.Action = action
		eff.Target = target
		steps = append(steps, eff)
		switch action {
		case "prop":
			if st.Value == "" {
				return nil, nil, nil, nil, fmt.Errorf("inputs digest: prop step has no file")
			}
			ref, err := p.hashInput(st.Value)
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("inputs digest: prop %q: %w", st.Value, err)
			}
			propSeen[ref.Path] = ref
		case "transition":
			if _, ok := transitions[st.Value]; ok {
				continue
			}
			dt, err := digestUsedTransition(p, st.Value, fps, w, h)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			transitions[st.Value] = dt
		}
	}
	return steps, omitEmptyAliases(aliases), sortedRefs(propSeen), omitEmptyTransitions(transitions), nil
}

func digestUsedTransition(p *Project, name string, fps, w, h int) (digestTransition, error) {
	t, ok := p.Transitions[name]
	if !ok {
		return digestTransition{}, fmt.Errorf("inputs digest: transition %q not in config", name)
	}
	dt := digestTransition{
		Cmd:  p.foldLocation(t.Cmd),
		Live: t.Live,
		FPS:  fps,
		W:    w,
		H:    h,
	}
	if t.Live.Prop != "" {
		ref, err := p.hashInput(t.Live.Prop)
		if err != nil {
			return digestTransition{}, fmt.Errorf("inputs digest: transition %q live.prop: %w", name, err)
		}
		dt.Prop = &ref
	}
	return dt, nil
}

func digestRunningHook(p *Project, s *Scene) (*fileRef, error) {
	rel := runningHook(s, p)
	if rel == "" {
		return nil, nil
	}
	ref, err := p.hashInput(rel)
	if err != nil {
		return nil, fmt.Errorf("inputs digest: hook %q: %w", rel, err)
	}
	return &ref, nil
}

// runningHook is the hook runHooks would execute: setup on a fresh scene,
// otherwise reset when enabled, and none for continue.
func runningHook(s *Scene, p *Project) string {
	if s.VMStartMode() == "continue" {
		return ""
	}
	if s.Fresh && p.Hooks.Setup != "" {
		return p.Hooks.Setup
	}
	if s.ResetEnabled() && p.Hooks.Reset != "" {
		return p.Hooks.Reset
	}
	return ""
}

func digestDeclaredInputs(p *Project, s *Scene) ([]fileRef, error) {
	seen := map[string]fileRef{}
	for _, rel := range s.Inputs {
		refs, err := p.hashInputTree(rel)
		if err != nil {
			return nil, fmt.Errorf("inputs digest: input %q: %w", rel, err)
		}
		for _, ref := range refs {
			seen[ref.Path] = ref
		}
	}
	return sortedRefs(seen), nil
}

func digestEnv(p *Project) map[string]string {
	if len(p.Env) == 0 {
		return nil
	}
	out := make(map[string]string, len(p.Env))
	for k, v := range p.Env {
		out[k] = p.foldLocation(v)
	}
	return out
}

func (p *Project) hashInputTree(rel string) ([]fileRef, error) {
	abs, err := p.InputPath(rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("missing file")
		}
		return nil, err
	}
	if info.IsDir() {
		var refs []fileRef
		walkErr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				ref, err := p.symlinkFileRef(path)
				if err != nil {
					return err
				}
				refs = append(refs, ref)
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			if !fi.Mode().IsRegular() {
				return nil
			}
			ref, err := p.fileRef(path)
			if err != nil {
				return err
			}
			refs = append(refs, ref)
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
		return refs, nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		ref, err := p.symlinkFileRef(abs)
		if err != nil {
			return nil, err
		}
		return []fileRef{ref}, nil
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file or directory")
	}
	ref, err := p.fileRef(abs)
	if err != nil {
		return nil, err
	}
	return []fileRef{ref}, nil
}

func (p *Project) hashInput(rel string) (fileRef, error) {
	refs, err := p.hashInputTree(rel)
	if err != nil {
		return fileRef{}, err
	}
	if len(refs) != 1 {
		return fileRef{}, fmt.Errorf("expected a file")
	}
	return refs[0], nil
}

func (p *Project) fileRef(abs string) (fileRef, error) {
	return p.fileRefAt(abs, abs)
}

// symlinkFileRef hashes the confined target of a symlink and keys the entry
// by the link's own workspace-relative path, so replacing the link or the
// target both move the digest.
func (p *Project) symlinkFileRef(linkAbs string) (fileRef, error) {
	key, err := p.workspaceRel(linkAbs)
	if err != nil {
		return fileRef{}, err
	}
	leafRel, err := filepath.Rel(p.Dir, linkAbs)
	if err != nil {
		return fileRef{}, fmt.Errorf("symlink %q: %w", key, err)
	}
	resolved, err := p.InputPath(leafRel)
	if err != nil {
		return fileRef{}, fmt.Errorf("symlink %q: %w", key, err)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return fileRef{}, fmt.Errorf("symlink %q: missing file", key)
		}
		return fileRef{}, fmt.Errorf("symlink %q: %w", key, err)
	}
	if info.IsDir() {
		return fileRef{}, fmt.Errorf("symlink %q points to a directory", key)
	}
	if !info.Mode().IsRegular() {
		return fileRef{}, fmt.Errorf("symlink %q is not a regular file", key)
	}
	return p.fileRefAt(linkAbs, resolved)
}

func (p *Project) fileRefAt(keyAbs, readAbs string) (fileRef, error) {
	rel, err := p.workspaceRel(keyAbs)
	if err != nil {
		return fileRef{}, err
	}
	sum, err := hashFile(readAbs)
	if err != nil {
		return fileRef{}, err
	}
	return fileRef{Path: rel, SHA256: sum}, nil
}

func hashFile(abs string) (string, error) {
	f, err := os.Open(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("missing file")
		}
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (p *Project) workspaceRel(abs string) (string, error) {
	rel, err := filepath.Rel(p.WorkspaceRoot(), abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes workspace", abs)
	}
	return filepath.ToSlash(rel), nil
}

// foldLocation rewrites expanded leaf and workspace paths back to ${PROJECT}
// and ${WORKSPACE} so the digest does not depend on the absolute location.
func (p *Project) foldLocation(value string) string {
	if value == "" || p == nil {
		return value
	}
	dir := p.Dir
	ws := p.WorkspaceRoot()
	type pair struct {
		abs, token string
	}
	first, second := pair{dir, "${PROJECT}"}, pair{ws, "${WORKSPACE}"}
	if len(ws) > len(dir) {
		first, second = second, first
	}
	if first.abs != "" {
		value = strings.ReplaceAll(value, first.abs, first.token)
	}
	if second.abs != "" && second.abs != first.abs {
		value = strings.ReplaceAll(value, second.abs, second.token)
	}
	return value
}

func sortedRefs(seen map[string]fileRef) []fileRef {
	if len(seen) == 0 {
		return nil
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]fileRef, 0, len(keys))
	for _, k := range keys {
		out = append(out, seen[k])
	}
	return out
}

func omitEmptyAliases(m map[string]Alias) map[string]Alias {
	if len(m) == 0 {
		return nil
	}
	return m
}

func omitEmptyTransitions(m map[string]digestTransition) map[string]digestTransition {
	if len(m) == 0 {
		return nil
	}
	return m
}
