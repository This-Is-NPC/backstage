package take

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

// Handle is a leased view of one complete clip/facts pair.
type Handle struct {
	Clip  string
	Facts string
	files []*os.File
}

// Close releases the generation lease and any leftover scene lock.
func (h *Handle) Close() error {
	if h == nil {
		return nil
	}
	var err error
	for _, f := range h.files {
		if f != nil {
			err = joinErr(err, f.Close())
		}
	}
	h.files = nil
	return err
}

func joinErr(err, next error) error {
	if next == nil {
		return err
	}
	if err == nil {
		return next
	}
	return fmt.Errorf("%v; %w", err, next)
}

// Open resolves the published take for a scene. A missing manifest falls
// back to the legacy stable pair. A present but invalid layout is an error.
func Open(p *scene.Project, name string) (*Handle, error) {
	paths, err := ForScene(p, name)
	if err != nil {
		return nil, err
	}
	return openPaths(paths)
}

func openPaths(p Paths) (*Handle, error) {
	manPath := p.Manifest()
	st, err := os.Stat(manPath)
	if err != nil {
		if os.IsNotExist(err) {
			return openLegacy(p)
		}
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("take manifest %s is not a file", manPath)
	}
	if _, err := os.Stat(p.lockPath()); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("take manifest %s has no scene lock", manPath)
		}
		return nil, err
	}
	sceneLock, err := lockShared(p.lockPath())
	if err != nil {
		return nil, err
	}
	man, err := readManifest(manPath)
	if err != nil {
		closeFile(sceneLock)
		return nil, err
	}
	genDir := p.generationDir(man.Generation)
	leasePath := filepath.Join(genDir, leaseName)
	if _, err := os.Stat(leasePath); err != nil {
		closeFile(sceneLock)
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("generation %s has no lease", man.Generation)
		}
		return nil, err
	}
	lease, err := lockShared(leasePath)
	if err != nil {
		closeFile(sceneLock)
		return nil, err
	}
	clip, facts, err := generationFiles(p, man)
	if err != nil {
		closeFile(lease)
		closeFile(sceneLock)
		return nil, err
	}
	closeFile(sceneLock)
	return &Handle{Clip: clip, Facts: facts, files: []*os.File{lease}}, nil
}

func openLegacy(p Paths) (*Handle, error) {
	clip := p.StableClip()
	if _, err := os.Stat(clip); err != nil {
		return nil, fmt.Errorf("no published take for %s: %w", p.Scene, err)
	}
	if _, err := os.Stat(p.lockPath()); err != nil {
		if os.IsNotExist(err) {
			// No lock file: a concurrent first publication may replace
			// SCENE.mp4 while this handle is open.
			return &Handle{Clip: clip, Facts: p.StableFacts()}, nil
		}
		return nil, err
	}
	sceneLock, err := lockShared(p.lockPath())
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(p.Manifest()); err == nil {
		closeFile(sceneLock)
		return openPaths(p)
	}
	return &Handle{Clip: clip, Facts: p.StableFacts(), files: []*os.File{sceneLock}}, nil
}

func readManifest(path string) (Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	var m Manifest
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Manifest{}, fmt.Errorf("%s: expected one JSON document", path)
	}
	if m.Version != manifestVer || m.Generation == "" || m.Clip == "" || m.Facts == "" {
		return Manifest{}, fmt.Errorf("%s: invalid take manifest", path)
	}
	if filepath.IsAbs(m.Clip) || filepath.IsAbs(m.Facts) {
		return Manifest{}, fmt.Errorf("%s: clip and facts must be relative", path)
	}
	if _, err := parseIDTime(m.Generation); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

func generationFiles(p Paths, m Manifest) (clip, facts string, err error) {
	if !validID(m.Generation) {
		return "", "", fmt.Errorf("invalid generation id %q", m.Generation)
	}
	sceneDir, err := scene.ConfinedPath(p.OutDir, p.OutDir, takesDir, p.Scene)
	if err != nil {
		return "", "", err
	}
	genDir, err := scene.ConfinedPath(sceneDir, p.OutDir, m.Generation)
	if err != nil {
		return "", "", err
	}
	clip, err = scene.ConfinedPath(genDir, genDir, m.Clip)
	if err != nil {
		return "", "", err
	}
	if _, err := scene.ConfinedPath(p.OutDir, p.OutDir, relTo(p.OutDir, clip)); err != nil {
		return "", "", err
	}
	facts, err = scene.ConfinedPath(genDir, genDir, m.Facts)
	if err != nil {
		return "", "", err
	}
	if _, err := scene.ConfinedPath(p.OutDir, p.OutDir, relTo(p.OutDir, facts)); err != nil {
		return "", "", err
	}
	for _, path := range []string{clip, facts} {
		st, err := os.Stat(path)
		if err != nil {
			return "", "", fmt.Errorf("generation file %s: %w", path, err)
		}
		if !st.Mode().IsRegular() {
			return "", "", fmt.Errorf("generation file %s is not regular", path)
		}
	}
	return clip, facts, nil
}

func relTo(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}
