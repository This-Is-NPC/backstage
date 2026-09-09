package production

import (
	"fmt"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

// managedStages validates the complete continuity chain before recording any
// clip, and returns the shared stages the production must reserve.
func managedStages(p *scene.Project, prod scene.Production) (map[string]bool, error) {
	reserved := map[string]bool{}
	last := map[string]string{}
	for _, ref := range prod.Scenes {
		path, err := p.ScenePathSafe(ref.Scene)
		if err != nil {
			return nil, err
		}
		s, err := scene.LoadScene(path)
		if err != nil {
			return nil, err
		}
		if err := s.ValidateVMStart(p); err != nil {
			return nil, err
		}
		cfg := p.VMs[s.VM]
		if cfg.Stage == "" {
			continue
		}
		reserved[cfg.Stage] = true
		if s.VMStartMode() == "continue" && last[cfg.Stage] != s.VMStart.After {
			return nil, fmt.Errorf("scene %q must follow %q on stage %q within this production", s.Name, s.VMStart.After, cfg.Stage)
		}
		last[cfg.Stage] = s.Name
	}
	return reserved, nil
}
