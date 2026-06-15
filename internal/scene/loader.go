package scene

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/This-Is-NPC/backstage/internal/prompter"
)

// configName is the project config filename.
const configName = "backstage.json"

// Defaults mirror core.sh so a sparse config still works. Popup-style and CPS
// defaults are owned by the prompter package (the single source of truth) so
// config defaulting and the popup driver can't drift.
const (
	defMonitor = "eDP-1"
	defFPS     = 30
	defOut     = "recordings"
	defCPS     = prompter.DefaultCPS
	defTerm    = prompter.DefaultTerm

	defPopupFontSize = prompter.DefaultFontSize
	defPopupTitle    = prompter.DefaultTitle
	defPopupHeader   = prompter.DefaultHeader
	defPopupChrome   = prompter.DefaultChrome
	defPopupClass    = prompter.DefaultClass
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
	// Validate the raw fontSize before defaulting: applyDefaults coerces 0 → the
	// default, so a negative value is the only invalid raw input to reject here.
	if p.Popup.Style.FontSize < 0 {
		return nil, fmt.Errorf("popup.style.fontSize must not be negative")
	}
	p.applyDefaults()
	if err := p.ValidateConfig(); err != nil {
		return nil, err
	}
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
	if p.Popup.Style.FontSize == 0 {
		p.Popup.Style.FontSize = defPopupFontSize
	}
	if p.Popup.Style.Title == "" {
		p.Popup.Style.Title = defPopupTitle
	}
	if p.Popup.Style.Header == "" {
		p.Popup.Style.Header = defPopupHeader
	}
	if p.Popup.Style.Chrome == "" {
		p.Popup.Style.Chrome = defPopupChrome
	}
	if p.Popup.Style.Class == "" {
		p.Popup.Style.Class = defPopupClass
	}
	if p.Term == "" {
		p.Term = defTerm
	}
	// render fps falls back to the recorder fps; w/h stay 0 (monitor native).
	if p.Render.FPS == 0 {
		p.Render.FPS = p.Record.FPS
	}
}

// PopupClassFor returns the configured popup window class for a project config,
// tolerating an otherwise-invalid config. The kill/close path uses it so a popup
// opened with a custom class is reliably dismissible even when the rest of the
// config no longer validates. Returns the default class if the config can't be
// read, names no class, or names a class that fails the same validation
// ValidateConfig applies (popupClassRE) — so a class with spaces or shell
// metacharacters is never handed to hyprctl, even on this tolerant path.
func PopupClassFor(cfgPath string) string {
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		return defPopupClass
	}
	var p Project
	if err := json.Unmarshal(b, &p); err != nil {
		return defPopupClass
	}
	class := p.Popup.Style.Class
	if class == "" || !popupClassRE.MatchString(class) {
		return defPopupClass
	}
	return class
}

// Expand replaces ${PROJECT} and $PROJECT with the project root directory.
func (p *Project) Expand(value string) string {
	r := strings.ReplaceAll(value, "${PROJECT}", p.Dir)
	return strings.ReplaceAll(r, "$PROJECT", p.Dir)
}
