package scene

import (
	"fmt"
	"sort"
)

// GroupMember is one VM alias in a state group and the stage it names.
type GroupMember struct {
	Alias string
	Stage string
}

// GroupMembers returns the members of a declared state group, in alias order.
func (p *Project) GroupMembers(name string) ([]GroupMember, error) {
	if p == nil || name == "" {
		return nil, fmt.Errorf("unknown state-group %q", name)
	}
	aliases, ok := p.StateGroups[name]
	if !ok {
		return nil, fmt.Errorf("unknown state-group %q", name)
	}
	out := make([]GroupMember, 0, len(aliases))
	for _, alias := range aliases {
		vm, ok := p.VMs[alias]
		if !ok || vm.Stage == "" {
			return nil, fmt.Errorf("state-group %q: %q is not a managed vm", name, alias)
		}
		out = append(out, GroupMember{Alias: alias, Stage: vm.Stage})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out, nil
}

// StartGroup is the group a clean start restores with, if any.
func (s *Scene) StartGroup() string {
	if s == nil || s.VMStart == nil || s.VMStartMode() != "clean" {
		return ""
	}
	return s.VMStart.Group
}

// EndGroup is the group a vm-end records, if any.
func (s *Scene) EndGroup() string {
	if s == nil || s.VMEnd == nil {
		return ""
	}
	return s.VMEnd.Group
}

// StartGroupStages is every stage a group start must lock, sorted.
// A scene without a group start returns nil.
func StartGroupStages(p *Project, s *Scene) []string {
	if p == nil || s == nil || s.StartGroup() == "" {
		return nil
	}
	members, err := p.GroupMembers(s.StartGroup())
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range members {
		if seen[m.Stage] {
			continue
		}
		seen[m.Stage] = true
		out = append(out, m.Stage)
	}
	sort.Strings(out)
	return out
}

func memberAlias(members []GroupMember, alias string) bool {
	for _, m := range members {
		if m.Alias == alias {
			return true
		}
	}
	return false
}
