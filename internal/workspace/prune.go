package workspace

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

// Reasons a prune-states decision keeps or removes a snapshot.
const (
	PruneUndeclared     = "undeclared"
	PruneManual         = "manual"
	PruneOutside        = "outside-workspace"
	PruneMissingProject = "missing-project"
	PruneConsumed       = "consumed"
	PruneInUse          = "in-use"
	PruneInitial        = "initial"
)

// PruneOptions select how PlanPrune treats a rule-(b) origin.
type PruneOptions struct {
	IncludeMissingProjects bool
}

// PruneDecision is one snapshot on the stage under prune-states.
type PruneDecision struct {
	Snapshot string `json:"snapshot"`
	Reason   string `json:"reason"`
}

// PrunePlan is what prune-states would remove or keep. Still-declared
// producers are omitted: they are current, not a prune decision.
type PrunePlan struct {
	Remove []PruneDecision `json:"removed"`
	Keep   []PruneDecision `json:"kept"`
}

// PlanPrune classifies every snapshot on the stage. DIR does not hide
// sibling projects: any valid scene in the workspace can declare or
// consume a state. The caller must refuse the plan when Result.HasError
// or Result.HasWarning is true; an unreadable scene or config may still
// name the snapshot.
func (e *Evaluation) PlanPrune(stage string, rec *machine.Record, opts PruneOptions) PrunePlan {
	plan := PrunePlan{Remove: []PruneDecision{}, Keep: []PruneDecision{}}
	if e == nil || rec == nil || rec.Snapshots == nil {
		return plan
	}
	declared := map[string]bool{}
	consumed := map[string]bool{}
	for _, n := range e.nodes {
		if n.loadErr != nil || n.stage() != stage {
			continue
		}
		if snap := n.endSnapshot(); snap != "" {
			declared[snap] = true
		}
		if snap := n.startSnapshot(); snap != "" {
			consumed[snap] = true
		}
	}
	names := make([]string, 0, len(rec.Snapshots))
	for name := range rec.Snapshots {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == "initial" {
			plan.Keep = append(plan.Keep, PruneDecision{Snapshot: name, Reason: PruneInitial})
			continue
		}
		if declared[name] {
			continue
		}
		origin, ok := rec.Origin(name)
		readable, discovered, missing := e.originKind(origin, ok)
		switch {
		case !readable:
			plan.Keep = append(plan.Keep, PruneDecision{Snapshot: name, Reason: PruneManual})
		case !discovered && !missing:
			plan.Keep = append(plan.Keep, PruneDecision{Snapshot: name, Reason: PruneOutside})
		case consumed[name]:
			plan.Keep = append(plan.Keep, PruneDecision{Snapshot: name, Reason: PruneConsumed})
		case rec.Source.Image != "" && rec.Snapshots[name] == rec.Source.Image:
			plan.Keep = append(plan.Keep, PruneDecision{Snapshot: name, Reason: PruneInUse})
		case missing && !opts.IncludeMissingProjects:
			plan.Keep = append(plan.Keep, PruneDecision{Snapshot: name, Reason: PruneMissingProject})
		default:
			plan.Remove = append(plan.Remove, PruneDecision{Snapshot: name, Reason: PruneUndeclared})
		}
	}
	return plan
}

func (e *Evaluation) originKind(origin machine.SnapshotOrigin, ok bool) (readable, discovered, missing bool) {
	if !ok {
		return false, false, false
	}
	project := strings.TrimSpace(origin.Project)
	if project == "" || !filepath.IsAbs(project) || filepath.Clean(project) != project {
		return false, false, false
	}
	if e != nil {
		for _, p := range e.projects {
			if sameProject(project, p.Dir) {
				return true, true, false
			}
		}
		if e.deletedProjectOfWorkspace(project) {
			return true, false, true
		}
	}
	return true, false, false
}

// deletedProjectOfWorkspace is origin rule (b): the directory no longer
// has backstage.json, discovery would have walked every component from
// the root to it, and the nearest config above still loads as this root.
// A hidden directory, a symlink, a record.out, a nested workspace, or
// an unreadable config above is outside, even when the path sits under
// the root. The walk uses Abs, not EvalSymlinks, so a symlink component
// stays visible.
func (e *Evaluation) deletedProjectOfWorkspace(project string) bool {
	if e == nil || e.root == "" {
		return false
	}
	root := absPath(e.root)
	dir := absPath(project)
	if !contained(root, dir) {
		return false
	}
	cfg := filepath.Join(dir, configName)
	if st, err := os.Stat(cfg); err == nil && !st.IsDir() {
		return false
	}
	if !discoverableBetween(root, dir, e.recordOutSkip()) {
		return false
	}
	above, err := nearestConfig(dir)
	if err != nil {
		return false
	}
	p, err := scene.LoadProject(above)
	if err != nil {
		return false
	}
	return sameResolved(p.WorkspaceRoot(), e.root)
}

func (e *Evaluation) recordOutSkip() map[string]bool {
	skip := map[string]bool{}
	if e == nil {
		return skip
	}
	for _, p := range e.projects {
		registerRecordOut(p, skip)
	}
	return skip
}

func discoverableBetween(root, target string, skipped map[string]bool) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		if strings.HasPrefix(part, ".") {
			return false
		}
		cur = filepath.Join(cur, part)
		if skipped[filepath.Clean(cur)] {
			return false
		}
		info, err := os.Lstat(cur)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}

func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}
