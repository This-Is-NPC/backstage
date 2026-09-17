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
	ResultOK            = "ok"
	ResultStepsFailed   = "steps-failed"
	ResultShort         = "short"
	ResultCaptureFailed = "capture-failed"
)

// StartState names the snapshot restored at the beginning of a clean start.
type StartState struct {
	Snapshot string `json:"snapshot,omitempty"`
}

// EndState records a vm-end snapshot. A failed capture sets Status to "failed".
type EndState struct {
	Snapshot string `json:"snapshot,omitempty"`
	Image    string `json:"image,omitempty"`
	Status   string `json:"status,omitempty"`
}

// Facts records what a take was made against, beside the clip.
type Facts struct {
	Stage        string        `json:"stage,omitempty"`
	Origin       string        `json:"origin,omitempty"`
	StartMode    string        `json:"vm-start,omitempty"`
	Snapshot     string        `json:"snapshot,omitempty"`
	ISOVersion   string        `json:"iso-version,omitempty"`
	ISOChecksum  string        `json:"iso-sha256,omitempty"`
	Recipe       string        `json:"recipe,omitempty"`
	Domain       string        `json:"domain,omitempty"`
	User         string        `json:"user,omitempty"`
	Omarchy      string        `json:"omarchy,omitempty"`
	Address      string        `json:"address,omitempty"`
	Made         string        `json:"made,omitempty"`
	Backstage    string        `json:"backstage,omitempty"`
	StartImage   string        `json:"start-image,omitempty"`
	StartState   *StartState   `json:"start-state,omitempty"`
	InputsSHA256 string        `json:"inputs-sha256,omitempty"`
	EndState     *EndState     `json:"end-state,omitempty"`
	GroupMembers []GroupMember `json:"group-members,omitempty"`
	Result       string        `json:"result,omitempty"`
	Timings      *Timings      `json:"timings,omitempty"`
}

// GroupMember is one stage restored for a group start.
type GroupMember struct {
	Stage          string `json:"stage"`
	Snapshot       string `json:"snapshot"`
	Image          string `json:"image"`
	Generation     string `json:"generation"`
	RestoreSkipped bool   `json:"restore-skipped,omitempty"`
}

// Timings is the cost of a guest take. Missing pointers were not measured:
// the phase did not run, or it failed or was interrupted.
type Timings struct {
	ShutdownSeconds        *float64           `json:"shutdown-seconds,omitempty"`
	CaptureSeconds         *float64           `json:"capture-seconds,omitempty"`
	CaptureBytes           *int64             `json:"capture-bytes,omitempty"`
	CaptureApparentBytes   *int64             `json:"capture-apparent-bytes,omitempty"`
	RestoreStopSeconds     *float64           `json:"restore-stop-seconds,omitempty"`
	RestoreActivateSeconds *float64           `json:"restore-activate-seconds,omitempty"`
	BootSeconds            *float64           `json:"boot-seconds,omitempty"`
	SessionSeconds         *float64           `json:"session-seconds,omitempty"`
	StagePhases            map[string]float64 `json:"stage-phases,omitempty"`
	CaptureMode            *string            `json:"capture-mode,omitempty"`
	ImageDepth             *int               `json:"image-depth,omitempty"`
	CaptureFallback        *string            `json:"capture-fallback,omitempty"`
	CatalogWaitSeconds     *float64           `json:"catalog-wait-seconds,omitempty"`
	RestoreSkipped         *bool              `json:"restore-skipped,omitempty"`
}

// Empty reports that no phase completed.
func (t Timings) Empty() bool {
	return t.ShutdownSeconds == nil && t.CaptureSeconds == nil && t.CaptureBytes == nil &&
		t.CaptureApparentBytes == nil && t.RestoreStopSeconds == nil &&
		t.RestoreActivateSeconds == nil && t.BootSeconds == nil &&
		t.SessionSeconds == nil && len(t.StagePhases) == 0 &&
		t.CaptureMode == nil && t.ImageDepth == nil && t.CaptureFallback == nil &&
		t.CatalogWaitSeconds == nil && t.RestoreSkipped == nil
}

// Path is the sidecar next to clip, named <clip>.facts.json.
func Path(clip string) string {
	return strings.TrimSuffix(clip, filepath.Ext(clip)) + ".facts.json"
}

// Read loads a sidecar written by Write.
func Read(path string) (Facts, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Facts{}, err
	}
	var f Facts
	if err := json.Unmarshal(body, &f); err != nil {
		return Facts{}, err
	}
	return f, nil
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
