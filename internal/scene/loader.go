package scene

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// configName is the project config filename.
const configName = "backstage.json"

// Defaults mirror core.sh so a sparse config still works.
const (
	defMonitor = "eDP-1"
	defFPS     = 30
	defOut     = "recordings"
	defCPS     = 32
	defTerm    = "ghostty"
)

var defPopupSize = []int{1200, 560}

// FindConfig walks up from a scene path to the nearest project config file,
// returning the config path and the project directory that holds it.
func FindConfig(scenePath string) (cfgPath, projectDir string, err error) {
	abs, err := filepath.Abs(scenePath)
	if err != nil {
		return "", "", err
	}
	d := filepath.Dir(abs)
	for {
		c := filepath.Join(d, configName)
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", "", fmt.Errorf("no %s found above %s", configName, scenePath)
		}
		d = parent
	}
}

// LoadScene reads and decodes a scene JSON file. When the scene has no name,
// it is derived from the file's base name (without extension).
func LoadScene(path string) (*Scene, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Scene
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("scene %s: %w", path, err)
	}
	if s.Name == "" {
		base := filepath.Base(path)
		s.Name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return &s, nil
}

// LoadProject reads a project config, records its directory, fills defaults, and
// expands ${PROJECT}/$PROJECT in env values to the project root.
func LoadProject(cfgPath string) (*Project, error) {
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, err
	}
	var p Project
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("config %s: %w", cfgPath, err)
	}
	p.Dir = filepath.Dir(cfgPath)
	p.applyDefaults()
	for k, v := range p.Env {
		if err := ValidateEnvKey(k); err != nil {
			return nil, err
		}
		p.Env[k] = p.Expand(v)
	}
	return &p, nil
}

func (p *Project) applyDefaults() {
	if p.Record.Monitor == "" {
		p.Record.Monitor = defMonitor
	}
	if p.Record.FPS == 0 {
		p.Record.FPS = defFPS
	}
	if p.Record.Out == "" {
		p.Record.Out = defOut
	}
	if len(p.Popup.Size) != 2 {
		p.Popup.Size = append([]int(nil), defPopupSize...)
	}
	if p.Popup.CPS == 0 {
		p.Popup.CPS = defCPS
	}
	if p.Term == "" {
		p.Term = defTerm
	}
	// render fps falls back to the recorder fps; w/h stay 0 (monitor native).
	if p.Render.FPS == 0 {
		p.Render.FPS = p.Record.FPS
	}
}

// Expand replaces ${PROJECT} and $PROJECT with the project root directory.
func (p *Project) Expand(value string) string {
	r := strings.ReplaceAll(value, "${PROJECT}", p.Dir)
	return strings.ReplaceAll(r, "$PROJECT", p.Dir)
}
