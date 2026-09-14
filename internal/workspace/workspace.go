// Package workspace discovers projects that share an extends root and
// reports the freshness of their recording takes. It is read-only: it
// never starts a stage, never takes a catalog lock, and never reads
// unpublished attempts.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

// Status names, in evaluation precedence. The first that applies wins.
const (
	Error                  = "error"
	BlockedNoProducer      = "blocked:no-producer"
	BlockedStateMissing    = "blocked:state-missing"
	BlockedRehearsalState  = "blocked:rehearsal-state"
	BlockedGroupIncomplete = "blocked:group-incomplete"
	Missing                = "missing"
	StaleInputs            = "stale:inputs"
	StaleStateMismatch     = "stale:state-mismatch"
	StaleStartState        = "stale:start-state"
	StaleUpstream          = "stale:upstream"
	Unverifiable           = "unverifiable"
	OK                     = "ok"
	ManualLabel            = "manual"
	KindSceneError         = "scene-error"
	KindConfigError        = "config-error"
)

var precedence = []string{
	Error,
	BlockedNoProducer,
	BlockedStateMissing,
	BlockedRehearsalState,
	BlockedGroupIncomplete,
	Missing,
	StaleInputs,
	StaleStateMismatch,
	StaleStartState,
	StaleUpstream,
	Unverifiable,
	OK,
}

// Options select the starting directory and the stage registry.
type Options struct {
	// Dir is the starting directory: it finds the workspace and limits
	// which scenes appear in the report. Empty means the process cwd.
	Dir string
	// Store is the stage registry. Nil uses machine.DefaultStore.
	// Status only calls Load.
	Store *machine.Store
}

// Warning is a discover problem that does not abort the report.
type Warning struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// Result is the status report for one workspace.
type Result struct {
	Scenes   []SceneStatus `json:"scenes"`
	Errors   []SceneStatus `json:"errors"`
	States   []StateLabel  `json:"states"`
	Warnings []Warning     `json:"warnings"`
}

// HasError reports that a configuration or scene error exists anywhere
// in the workspace, including rows hidden from the scene list by DIR.
func (r *Result) HasError() bool {
	return r != nil && len(r.Errors) > 0
}

// HasWarning reports a discover problem anywhere in the workspace. A
// leaf whose backstage.json does not parse is a warning, not an error;
// prune-states still refuses to plan, because that leaf may declare a state.
func (r *Result) HasWarning() bool {
	return r != nil && len(r.Warnings) > 0
}

// SceneStatus is one recording scene in the report.
type SceneStatus struct {
	Kind           string   `json:"kind,omitempty"`
	Scene          string   `json:"scene,omitempty"`
	Project        string   `json:"project"`
	ProjectRel     string   `json:"-"`
	Status         string   `json:"status"`
	Reasons        []string `json:"reasons"`
	Error          string   `json:"error,omitempty"`
	Clip           string   `json:"clip,omitempty"`
	Facts          string   `json:"facts,omitempty"`
	StableClip     string   `json:"stable-clip,omitempty"`
	StableFacts    string   `json:"stable-facts,omitempty"`
	Stage          string   `json:"stage,omitempty"`
	StartSnapshot  string   `json:"start-snapshot,omitempty"`
	EndSnapshot    string   `json:"end-snapshot,omitempty"`
	Group          string   `json:"group,omitempty"`
	Generation     string   `json:"generation,omitempty"`
	GroupMember    string   `json:"group-member,omitempty"`
	Detail         string   `json:"-"`
	upstreamName   string
	upstreamStat   string
	memberStage    string
	memberKind     string
	memberProblems []memberProblem
}

type memberProblem struct {
	alias, stage, kind string
}

// StateLabel names a snapshot on a stage as manual or as its producer.
type StateLabel struct {
	Stage    string `json:"stage"`
	Snapshot string `json:"snapshot"`
	Label    string `json:"label"`
}

// DuplicateProducer is two or more scenes that save the same snapshot.
type DuplicateProducer struct {
	Stage    string   `json:"stage"`
	Snapshot string   `json:"snapshot"`
	Scenes   []string `json:"scenes"`
}

// ConflictError is a graph that cannot be reported: duplicate producers
// or a cycle. The command exits non-zero and lists the conflicts.
type ConflictError struct {
	Duplicates []DuplicateProducer `json:"duplicate-producers,omitempty"`
	Cycles     [][]string          `json:"cycles,omitempty"`
}

func (e *ConflictError) Error() string {
	if e == nil {
		return "workspace conflict"
	}
	var b strings.Builder
	for _, d := range e.Duplicates {
		fmt.Fprintf(&b, "two scenes make snapshot %s on stage %s: %s\n", d.Snapshot, d.Stage, strings.Join(d.Scenes, ", "))
	}
	for _, cyc := range e.Cycles {
		fmt.Fprintf(&b, "cycle: %s\n", strings.Join(cyc, " -> "))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// Evaluation is the discovered workspace and every recording scene's
// status. Report, --with-deps and prune-states share it.
type Evaluation struct {
	filter   string
	root     string
	projects []*scene.Project
	faults   []projectFault
	warnings []Warning
	nodes    []node
	order    []node
	eval     *evaluator
	status   map[string]SceneStatus
}

// Evaluate discovers the workspace at Dir, loads every project whose
// chain reaches that root, and evaluates recording scenes. Visual
// scenes are omitted. Cycles and duplicate producers fail the call.
func Evaluate(opts Options) (*Evaluation, error) {
	dir := opts.Dir
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		dir = wd
	}
	dir, err := absResolved(dir)
	if err != nil {
		return nil, err
	}
	store := opts.Store
	if store == nil {
		store, err = machine.DefaultStore()
		if err != nil {
			return nil, err
		}
	}
	root, err := workspaceRoot(dir)
	if err != nil {
		return nil, err
	}
	projects, faults, warnings, err := discoverProjects(root)
	if err != nil {
		return nil, err
	}
	nodes, err := loadRecordingScenes(root, projects)
	if err != nil {
		return nil, err
	}
	if err := validateGraph(nodes); err != nil {
		return nil, err
	}
	order, err := topoScenes(nodes)
	if err != nil {
		return nil, err
	}
	eval := newEvaluator(store, nodes, faults)
	byID := map[string]SceneStatus{}
	for _, n := range order {
		st, err := eval.scene(n)
		if err != nil {
			return nil, err
		}
		byID[n.id] = st
	}
	if warnings == nil {
		warnings = []Warning{}
	}
	return &Evaluation{
		filter:   dir,
		root:     root,
		projects: projects,
		faults:   faults,
		warnings: warnings,
		nodes:    nodes,
		order:    order,
		eval:     eval,
		status:   byID,
	}, nil
}

// Report is the DIR-filtered status view of Evaluate.
func Report(opts Options) (*Result, error) {
	ev, err := Evaluate(opts)
	if err != nil {
		return nil, err
	}
	return ev.Result()
}

// Result applies the starting-directory scene filter. Errors from the
// whole workspace stay in the list.
func (e *Evaluation) Result() (*Result, error) {
	var projectDirs []string
	for _, p := range e.projects {
		projectDirs = append(projectDirs, p.Dir)
	}
	for _, f := range e.faults {
		projectDirs = append(projectDirs, f.Dir)
	}
	errs := []SceneStatus{}
	for _, f := range e.faults {
		errs = append(errs, f.status())
	}
	for _, n := range e.order {
		st := e.status[n.id]
		if st.Status != Error {
			continue
		}
		st.Kind = KindSceneError
		errs = append(errs, st)
	}
	scenes := []SceneStatus{}
	for _, n := range e.order {
		if !inScope(n.project.Dir, e.filter, projectDirs) {
			continue
		}
		st := e.status[n.id]
		if st.Status == Error {
			continue
		}
		scenes = append(scenes, st)
	}
	labels, err := e.eval.labels()
	if err != nil {
		return nil, err
	}
	if labels == nil {
		labels = []StateLabel{}
	}
	return &Result{Scenes: scenes, Errors: errs, States: labels, Warnings: e.warnings}, nil
}

func (f projectFault) status() SceneStatus {
	return SceneStatus{
		Kind:       KindConfigError,
		Project:    f.Dir,
		ProjectRel: f.Rel,
		Status:     Error,
		Reasons:    []string{Error, f.Error},
		Error:      f.Error,
		Detail:     f.Error,
	}
}

func inScope(projectDir, filterDir string, projects []string) bool {
	projectDir = resolvePath(projectDir)
	filterDir = resolvePath(filterDir)
	if contained(filterDir, projectDir) {
		return true
	}
	return sameResolved(projectDir, closestContaining(filterDir, projects))
}

func contained(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && (rel == "." || !strings.HasPrefix(rel, ".."))
}

func closestContaining(filter string, projects []string) string {
	best := ""
	bestN := -1
	for _, p := range projects {
		p = resolvePath(p)
		if !contained(p, filter) {
			continue
		}
		if n := len(p); n > bestN {
			best = p
			bestN = n
		}
	}
	return best
}
