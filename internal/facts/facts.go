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
	Stage            string            `json:"stage,omitempty"`
	Origin           string            `json:"origin,omitempty"`
	StartMode        string            `json:"vm-start,omitempty"`
	Snapshot         string            `json:"snapshot,omitempty"`
	ISOVersion       string            `json:"iso-version,omitempty"`
	ISOChecksum      string            `json:"iso-sha256,omitempty"`
	Recipe           string            `json:"recipe,omitempty"`
	Domain           string            `json:"domain,omitempty"`
	User             string            `json:"user,omitempty"`
	Omarchy          string            `json:"omarchy,omitempty"`
	Address          string            `json:"address,omitempty"`
	Made             string            `json:"made,omitempty"`
	Backstage        string            `json:"backstage,omitempty"`
	StartImage       string            `json:"start-image,omitempty"`
	StartState       *StartState       `json:"start-state,omitempty"`
	InputsSHA256     string            `json:"inputs-sha256,omitempty"`
	EndState         *EndState         `json:"end-state,omitempty"`
	GroupMembers     []GroupMember     `json:"group-members,omitempty"`
	Result           string            `json:"result,omitempty"`
	Timings          *Timings          `json:"timings,omitempty"`
	ProductionPhases *ProductionPhases `json:"production-phases,omitempty"`
	Steps            []Step            `json:"steps,omitempty"`
	PointerTrace     []PointerSample   `json:"pointer-trace,omitempty"`
}

// PointerSample uses the recording clock. A nil position marks an observation gap.
type PointerSample struct {
	At      float64  `json:"at"`
	Pointer *Pointer `json:"pointer"`
}

// Step records the action interval on the captured video's clock.
type Step struct {
	ID      string   `json:"step,omitempty"`
	Action  string   `json:"action"`
	Target  string   `json:"target,omitempty"`
	Start   float64  `json:"start"`
	End     float64  `json:"end"`
	Keys    []string `json:"keys,omitempty"`
	Region  *Region  `json:"region,omitempty"`
	Pointer *Pointer `json:"pointer,omitempty"`
}

// Pointer is an observed source position, rather than an editorial guess.
type Pointer struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Click bool    `json:"click,omitempty"`
}

type VisualState struct {
	Region  *Region
	Pointer *Pointer
}

// Region is a rectangle in normalized source-video coordinates.
type Region struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// ProductionPhases measures completed work in a production, in wall seconds.
type ProductionPhases struct {
	Record      float64 `json:"record-seconds"`
	Retime      float64 `json:"retime-seconds"`
	Transitions float64 `json:"transition-seconds"`
	Normalize   float64 `json:"normalize-seconds"`
	Concat      float64 `json:"concat-seconds"`
	Total       float64 `json:"total-seconds"`
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
