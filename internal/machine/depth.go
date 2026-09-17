package machine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	depthOriginSet      = "set"
	depthOriginEnv      = "env"
	depthOriginSettings = "settings"
	depthOriginDefault  = "default"
)

func (m *Manager) imageDepthLimit() (int, string, error) {
	if m != nil && m.MaxImageDepth != nil {
		if *m.MaxImageDepth < 0 {
			return 0, depthOriginSet, fmt.Errorf("max-image-depth must be an integer >= 0")
		}
		return *m.MaxImageDepth, depthOriginSet, nil
	}
	if v := strings.TrimSpace(os.Getenv("BACKSTAGE_IMAGE_DEPTH")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return 0, depthOriginEnv, fmt.Errorf("BACKSTAGE_IMAGE_DEPTH must be an integer >= 0")
		}
		return n, depthOriginEnv, nil
	}
	if m == nil || m.Store == nil {
		return DefaultMaxImageDepth, depthOriginDefault, nil
	}
	path := filepath.Join(m.Store.Root, "settings.json")
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultMaxImageDepth, depthOriginDefault, nil
		}
		return 0, depthOriginSettings, fmt.Errorf("machines/settings.json: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var cfg struct {
		MaxImageDepth *int `json:"max-image-depth"`
	}
	if err := dec.Decode(&cfg); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "unknown field") {
			return 0, depthOriginSettings, fmt.Errorf("machines/settings.json: %s", msg)
		}
		if strings.Contains(msg, "cannot unmarshal") {
			return 0, depthOriginSettings, fmt.Errorf("machines/settings.json: max-image-depth must be an integer >= 0")
		}
		return 0, depthOriginSettings, fmt.Errorf("machines/settings.json: %w", err)
	}
	if cfg.MaxImageDepth == nil {
		return DefaultMaxImageDepth, depthOriginDefault, nil
	}
	if *cfg.MaxImageDepth < 0 {
		return 0, depthOriginSettings, fmt.Errorf("machines/settings.json: max-image-depth must be an integer >= 0")
	}
	return *cfg.MaxImageDepth, depthOriginSettings, nil
}

func (m *Manager) imageDepthCheck() Check {
	n, origin, err := m.imageDepthLimit()
	if err != nil {
		return Check{"image-depth", false, err.Error()}
	}
	return Check{"image-depth", true, fmt.Sprintf("%d (%s)", n, origin)}
}
