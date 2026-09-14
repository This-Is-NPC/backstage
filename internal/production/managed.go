package production

import (
	"fmt"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

// managedStages validates the complete continuity chain before recording any
// clip, and returns the shared stages the production must reserve.
func managedStages(p *scene.Project, prod scene.Production) (map[string]bool, error) {
	reserved := map[string]bool{}
	type lastScene struct {
		name  string
		ended bool
	}
	last := map[string]lastScene{}
	type producer struct {
		name  string
		index int
	}
	producers := map[string]producer{}
	type loaded struct {
		s     *scene.Scene
		stage string
	}
	scenes := make([]loaded, 0, len(prod.Scenes))
	for i, ref := range prod.Scenes {
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
		if err := s.ValidateVMEnd(p); err != nil {
			return nil, err
		}
		cfg := p.VMs[s.VM]
		if cfg.Stage == "" {
			scenes = append(scenes, loaded{s: s})
			continue
		}
		reserved[cfg.Stage] = true
		for _, st := range scene.StartGroupStages(p, s) {
			reserved[st] = true
		}
		if s.EndGroup() != "" {
			if members, err := p.GroupMembers(s.EndGroup()); err == nil {
				for _, m := range members {
					reserved[m.Stage] = true
				}
			}
		}
		if s.VMStartMode() == "continue" && last[cfg.Stage].name != s.VMStart.After {
			return nil, fmt.Errorf("scene %q must follow %q on stage %q within this production", s.Name, s.VMStart.After, cfg.Stage)
		}
		if s.VMStartMode() == "continue" && last[cfg.Stage].ended {
			return nil, fmt.Errorf("cannot continue %q: scene %q ends the guest", s.VMStart.After, last[cfg.Stage].name)
		}
		if s.VMEnd != nil {
			key := cfg.Stage + "\x00" + s.VMEnd.Snapshot
			if prev, ok := producers[key]; ok && prev.name != s.Name {
				return nil, fmt.Errorf("scenes %q and %q both end on snapshot %q of stage %q", prev.name, s.Name, s.VMEnd.Snapshot, cfg.Stage)
			}
			producers[key] = producer{name: s.Name, index: i}
			last[cfg.Stage] = lastScene{name: s.Name, ended: true}
		} else {
			last[cfg.Stage] = lastScene{name: s.Name}
		}
		scenes = append(scenes, loaded{s: s, stage: cfg.Stage})
	}
	for i, item := range scenes {
		if item.stage == "" || item.s.VMStartMode() != "clean" || item.s.VMStart == nil || item.s.VMStart.Snapshot == "" {
			continue
		}
		key := item.stage + "\x00" + item.s.VMStart.Snapshot
		made, ok := producers[key]
		if !ok || made.name == item.s.Name {
			continue
		}
		if made.index >= i {
			return nil, fmt.Errorf("scene %q must follow %q on stage %q within this production", item.s.Name, made.name, item.stage)
		}
	}
	return reserved, nil
}
