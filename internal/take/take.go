// Package take publishes a recorded clip and its facts as one generation.
//
// A video and its facts are one publication. Two file renames are not a pair.
// Default play output writes into a pending directory, then commits a
// versioned manifest. The documented SCENE.mp4 path is a projection of that
// generation, never a hardlink.
package take

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

const (
	clipName       = "clip.mp4"
	factsName      = "clip.facts.json"
	leaseName      = "lease"
	lockName       = "lock"
	takesDir       = ".takes"
	attemptsDir    = "attempts"
	pendingPrefix  = ".pending-"
	creatingPrefix = ".creating-"
	creatingMaxAge = time.Hour
	idLayout       = "20060102T150405.000000000Z"
	manifestVer    = 1
)

var idRE = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[0-9a-f]{4}$`)

// Manifest is the publication commit beside the stable paths.
type Manifest struct {
	Version    int    `json:"version"`
	Generation string `json:"generation"`
	Clip       string `json:"clip"`
	Facts      string `json:"facts"`
}

// Paths locates one scene's takes under the leaf record.out.
type Paths struct {
	OutDir string
	Scene  string
}

// ForScene resolves record.out for a named scene inside the leaf project.
func ForScene(p *scene.Project, name string) (Paths, error) {
	if err := scene.ValidateName("scene", name); err != nil {
		return Paths{}, err
	}
	out := p.Record.Out
	if out == "" {
		out = "recordings"
	}
	dir, err := p.OutputPath(out)
	if err != nil {
		return Paths{}, err
	}
	return Paths{OutDir: dir, Scene: name}, nil
}

func (p Paths) sceneDir() string    { return filepath.Join(p.OutDir, takesDir, p.Scene) }
func (p Paths) lockPath() string    { return filepath.Join(p.sceneDir(), lockName) }
func (p Paths) attemptsDir() string { return filepath.Join(p.sceneDir(), attemptsDir) }
func (p Paths) pendingDir(id string) string {
	return filepath.Join(p.sceneDir(), pendingPrefix+id)
}
func (p Paths) creatingDir(id string) string {
	return filepath.Join(p.sceneDir(), creatingPrefix+id)
}
func (p Paths) generationDir(id string) string {
	return filepath.Join(p.sceneDir(), id)
}
func (p Paths) attemptDir(id string) string {
	return filepath.Join(p.attemptsDir(), id)
}
func (p Paths) Manifest() string   { return filepath.Join(p.OutDir, p.Scene+".take.json") }
func (p Paths) StableClip() string { return filepath.Join(p.OutDir, p.Scene+".mp4") }
func (p Paths) StableFacts() string {
	return filepath.Join(p.OutDir, p.Scene+".facts.json")
}

func newID(now time.Time) string {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		b[0], b[1] = 0x0f, 0x0f
	}
	return now.UTC().Format(idLayout) + "-" + hex.EncodeToString(b[:])
}

func validID(id string) bool {
	return idRE.MatchString(id)
}

func parseIDTime(id string) (time.Time, error) {
	id = strings.TrimPrefix(id, pendingPrefix)
	id = strings.TrimPrefix(id, creatingPrefix)
	if !validID(id) {
		return time.Time{}, fmt.Errorf("take id %q is invalid", id)
	}
	return time.Parse(idLayout, id[:len(idLayout)])
}

// OverwritesTake reports that output is a published take path for sceneName:
// the stable clip, facts, manifest, or anything inside .takes/.
func OverwritesTake(p *scene.Project, sceneName, output string) error {
	paths, err := ForScene(p, sceneName)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	for _, banned := range []string{paths.StableClip(), paths.StableFacts(), paths.Manifest()} {
		abs, err := filepath.Abs(banned)
		if err != nil {
			return err
		}
		if out == abs {
			return fmt.Errorf("output would overwrite a published take")
		}
	}
	takesRoot := filepath.Join(paths.OutDir, takesDir)
	rel, err := filepath.Rel(takesRoot, out)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("output would overwrite a published take")
	}
	return nil
}

func writeJSON(path string, v any) error {
	return writeJSONTracked(path, v, nil)
}

func writeJSONTracked(path string, v any, track func(string)) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if track != nil {
		track(tmpName)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
