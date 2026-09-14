// Package facts writes the provenance sidecar beside a recorded clip.
//
// A film is evidence and evidence has a provenance. `pacman -Q omarchy` today
// and `pacman -Q omarchy` in six months are the difference between a take that
// can be reproduced and one that can only be re-shot. Host takes write the
// same sidecar without guest fields.
package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	ResultOK          = "ok"
	ResultStepsFailed = "steps-failed"
	ResultShort       = "short"
)

// StartState names the snapshot restored at the beginning of a clean start.
type StartState struct {
	Snapshot string `json:"snapshot,omitempty"`
}

// Facts records what a take was made against, beside the clip.
type Facts struct {
	Stage        string      `json:"stage,omitempty"`
	Origin       string      `json:"origin,omitempty"`
	StartMode    string      `json:"vm-start,omitempty"`
	Snapshot     string      `json:"snapshot,omitempty"`
	ISOVersion   string      `json:"iso-version,omitempty"`
	ISOChecksum  string      `json:"iso-sha256,omitempty"`
	Recipe       string      `json:"recipe,omitempty"`
	Domain       string      `json:"domain,omitempty"`
	User         string      `json:"user,omitempty"`
	Omarchy      string      `json:"omarchy,omitempty"`
	Address      string      `json:"address,omitempty"`
	Made         string      `json:"made,omitempty"`
	Backstage    string      `json:"backstage,omitempty"`
	StartImage   string      `json:"start-image,omitempty"`
	StartState   *StartState `json:"start-state,omitempty"`
	InputsSHA256 string      `json:"inputs-sha256,omitempty"`
	Result       string      `json:"result,omitempty"`
}

// Path is the sidecar next to clip, named <clip>.facts.json.
func Path(clip string) string {
	return strings.TrimSuffix(clip, filepath.Ext(clip)) + ".facts.json"
}

// Write replaces path atomically: a temporary file in the same directory, then
// rename. A reader sees the previous file or the new file, never a mix.
func Write(path string, f Facts) error {
	body, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".facts-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
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
