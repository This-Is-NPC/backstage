package stage

import "github.com/This-Is-NPC/backstage/internal/scene"

// Stager prepares the stage — a display plus the windows a scene runs in — and
// tears it down. Setup returns a Manifest mapping pane names to live ids.
type Stager interface {
	Setup(layout scene.Layout, p *scene.Project) (*scene.Manifest, error)
	Teardown() error
}
