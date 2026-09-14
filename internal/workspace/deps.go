package workspace

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

// Kind selects play versus rehearse rules for a dependency plan.
type Kind int

const (
	KindPlay Kind = iota
	KindRehearse
)

const (
	ReasonRequested       = "requested"
	ReasonUpstreamRerun   = "upstream re-run"
	ReasonContinueSession = "continue session"
)

// DepsOptions name the scene to play or rehearse and the command flags.
type DepsOptions struct {
	Options
	ScenePath    string
	Kind         Kind
	Adopt        bool
	ReplaceState bool
}

// PlanStep is one take in a --with-deps run.
type PlanStep struct {
	ID         string
	Scene      string
	Project    string
	ProjectRel string
	Stage      string
	Reason     string
	Path       string
	Requested  bool
}

// Plan is the ordered set of takes --with-deps will run.
type Plan struct {
	Steps         []PlanStep
	Stages        []string
	Warnings      []Warning
	AdoptSnapshot string
}

// ChainError is a scene or project error on the requested scene's chain.
type ChainError struct {
	Errors []SceneStatus
}

func (e *ChainError) Error() string {
	if e == nil || len(e.Errors) == 0 {
		return "dependency chain has errors"
	}
	var b strings.Builder
	b.WriteString("dependency chain has errors:")
	for _, st := range e.Errors {
		name := st.Scene
		if name == "" {
			name = st.ProjectRel
		}
		fmt.Fprintf(&b, "\n  %s: %s", name, st.Error)
	}
	return b.String()
}

// PlanDeps walks producers of ScenePath and chooses which takes to run.
func PlanDeps(opts DepsOptions) (*Plan, error) {
	if opts.ScenePath == "" {
		return nil, fmt.Errorf("with-deps needs a scene path")
	}
	scenePath, err := filepath.Abs(opts.ScenePath)
	if err != nil {
		return nil, err
	}
	s, err := scene.LoadScene(scenePath)
	if err != nil {
		return nil, err
	}
	if s.Type == "visual" {
		return nil, fmt.Errorf("with-deps does not run visual scenes")
	}
	cfg, _, err := scene.FindConfig(scenePath)
	if err != nil {
		return nil, err
	}
	p, err := scene.LoadProject(cfg)
	if err != nil {
		return nil, err
	}
	if opts.Dir == "" {
		opts.Dir = p.Dir
	}
	ev, err := Evaluate(opts.Options)
	if err != nil {
		return nil, err
	}
	target, ok := ev.findNode(scenePath)
	if !ok {
		scenesDir := filepath.Join(p.Dir, "scenes")
		if !underDir(scenesDir, scenePath) {
			return nil, fmt.Errorf("--with-deps needs a scene under %s", scenesDir)
		}
		return nil, fmt.Errorf("scene %q is not a recording scene in this workspace", s.Name)
	}
	return ev.plan(target, opts)
}

func (e *Evaluation) findNode(scenePath string) (node, bool) {
	for _, n := range e.nodes {
		if n.path != "" && sameResolved(n.path, scenePath) {
			return n, true
		}
	}
	return node{}, false
}

func underDir(parent, path string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "..")
}

func (e *Evaluation) plan(target node, opts DepsOptions) (*Plan, error) {
	chain, chainIDs := e.walkChain(target)
	if errs := e.chainErrors(chainIDs, chain); len(errs) > 0 {
		return nil, &ChainError{Errors: errs}
	}
	for _, n := range chain {
		st := e.status[n.id]
		if st.Status == BlockedNoProducer {
			snap := n.startSnapshot()
			if snap == "" {
				snap = "start state"
			}
			return nil, fmt.Errorf("%s: %s does not exist and no scene makes it", n.id, snap)
		}
	}

	selected := map[string]string{}
	for _, n := range chain {
		if n.id == target.id {
			continue
		}
		if reason := e.seedReason(n, opts.Kind); reason != "" {
			selected[n.id] = reason
		}
	}
	changed := true
	for changed {
		changed = false
		for _, n := range chain {
			if n.id == target.id || selected[n.id] != "" {
				continue
			}
			for _, pred := range e.predecessors(n) {
				if selected[pred.id] != "" {
					selected[n.id] = ReasonUpstreamRerun
					changed = true
					break
				}
			}
		}
	}
	changed = true
	for changed {
		changed = false
		for _, n := range append(append([]node{}, chain...), target) {
			if n.id != target.id && selected[n.id] == "" {
				continue
			}
			pred, ok := e.continuePred(n)
			if !ok || pred.id == target.id {
				continue
			}
			if selected[pred.id] == "" {
				selected[pred.id] = ReasonContinueSession
				changed = true
			}
		}
	}

	var picked []node
	for _, n := range e.order {
		if n.id == target.id || selected[n.id] != "" {
			picked = append(picked, n)
		}
	}
	ordered, err := arrangeContinues(picked, e.nodes)
	if err != nil {
		return nil, err
	}
	adoptSnap, err := e.flagGates(ordered, target, opts)
	if err != nil {
		return nil, err
	}

	steps := make([]PlanStep, 0, len(ordered))
	seenStage := map[string]bool{}
	var stages []string
	for _, n := range ordered {
		reason := selected[n.id]
		req := n.id == target.id
		if req {
			reason = ReasonRequested
		}
		steps = append(steps, PlanStep{
			ID:         n.id,
			Scene:      n.scene.Name,
			Project:    n.project.Dir,
			ProjectRel: n.projectRel,
			Stage:      n.stage(),
			Reason:     reason,
			Path:       n.path,
			Requested:  req,
		})
		if st := n.stage(); st != "" && !seenStage[st] {
			seenStage[st] = true
			stages = append(stages, st)
		}
	}
	sort.Strings(stages)
	return &Plan{Steps: steps, Stages: stages, Warnings: e.offChainWarnings(chainIDs), AdoptSnapshot: adoptSnap}, nil
}

func (e *Evaluation) walkChain(target node) ([]node, map[string]bool) {
	seen := map[string]bool{}
	var out []node
	var walk func(node)
	walk = func(n node) {
		if seen[n.id] {
			return
		}
		seen[n.id] = true
		for _, p := range e.predecessors(n) {
			walk(p)
		}
		out = append(out, n)
	}
	walk(target)
	return out, seen
}

func (e *Evaluation) predecessors(n node) []node {
	var out []node
	seen := map[string]bool{}
	add := func(p node) {
		if p.id == "" || p.id == n.id || seen[p.id] {
			return
		}
		seen[p.id] = true
		out = append(out, p)
	}
	if snap := n.startSnapshot(); snap != "" && n.stage() != "" {
		if p, ok := e.eval.made[producerKey(n.stage(), snap)]; ok {
			add(p)
		}
	}
	if pred, ok := e.continuePred(n); ok {
		add(pred)
	}
	return out
}

func (e *Evaluation) originLinked(n node) []node {
	rec, err := e.eval.loadStage(n.stage())
	if err != nil || rec == nil {
		return nil
	}
	origin, ok := rec.Origin(n.startSnapshot())
	if !ok || origin.Scene == "" {
		return nil
	}
	var out []node
	for _, p := range e.nodes {
		if p.scene != nil && p.scene.Name == origin.Scene && sameProject(origin.Project, p.project.Dir) {
			out = append(out, p)
		}
	}
	return out
}

func (e *Evaluation) continuePred(n node) (node, bool) {
	after := n.continueAfter()
	if after == "" || n.project == nil {
		return node{}, false
	}
	for _, p := range e.nodes {
		if p.project != nil && p.scene != nil && p.project.Dir == n.project.Dir && p.scene.Name == after {
			return p, true
		}
	}
	return node{}, false
}

func (e *Evaluation) seedReason(n node, kind Kind) string {
	st := e.status[n.id]
	if kind == KindPlay && e.rehearsalMade(n) && (st.Status == OK || st.Status == StaleUpstream) {
		return BlockedRehearsalState
	}
	switch st.Status {
	case OK, Error, BlockedNoProducer, StaleUpstream, Unverifiable:
		return ""
	default:
		return st.Status
	}
}

func (e *Evaluation) rehearsalMade(n node) bool {
	end := n.endSnapshot()
	if end == "" || n.stage() == "" {
		return false
	}
	rec, err := e.eval.loadStage(n.stage())
	if err != nil || rec == nil {
		return false
	}
	origin, ok := rec.Origin(end)
	return ok && origin.Take == machine.TakeRehearsal
}

func (e *Evaluation) chainErrors(chainIDs map[string]bool, chain []node) []SceneStatus {
	var out []SceneStatus
	seenFault := map[string]bool{}
	for _, n := range chain {
		st := e.status[n.id]
		if st.Status == Error {
			st.Kind = KindSceneError
			out = append(out, st)
		}
		for _, p := range e.originLinked(n) {
			st := e.status[p.id]
			if st.Status == Error {
				st.Kind = KindSceneError
				out = append(out, st)
			}
		}
		for _, f := range e.originFaults(n) {
			if seenFault[f.Dir] {
				continue
			}
			seenFault[f.Dir] = true
			out = append(out, f.status())
		}
	}
	var blocked []node
	for _, n := range chain {
		if e.status[n.id].Status == BlockedNoProducer {
			blocked = append(blocked, n)
		}
	}
	if len(blocked) > 0 && len(e.faults) > 0 {
		for _, n := range blocked {
			st := e.status[n.id]
			snap := n.startSnapshot()
			if snap == "" {
				snap = "start state"
			}
			st.Error = snap + " does not exist and no scene makes it"
			out = append(out, st)
		}
		for _, f := range e.faults {
			if seenFault[f.Dir] {
				continue
			}
			seenFault[f.Dir] = true
			out = append(out, f.status())
		}
	}
	return out
}

func (e *Evaluation) originFaults(n node) []projectFault {
	rec, err := e.eval.loadStage(n.stage())
	if err != nil || rec == nil {
		return nil
	}
	origin, ok := rec.Origin(n.startSnapshot())
	if !ok || origin.Project == "" {
		return nil
	}
	var out []projectFault
	for _, f := range e.faults {
		if sameProject(origin.Project, f.Dir) {
			out = append(out, f)
		}
	}
	return out
}

func (e *Evaluation) offChainWarnings(chainIDs map[string]bool) []Warning {
	out := append([]Warning{}, e.warnings...)
	for _, f := range e.faults {
		on := false
		for _, n := range e.nodes {
			if chainIDs[n.id] && n.project != nil && sameResolved(n.project.Dir, f.Dir) {
				on = true
				break
			}
		}
		if on {
			continue
		}
		out = append(out, Warning{Path: f.Rel, Error: f.Error})
	}
	for _, n := range e.order {
		if chainIDs[n.id] {
			continue
		}
		st := e.status[n.id]
		if st.Status != Error {
			continue
		}
		path := n.id
		if n.path != "" {
			path = n.path
		}
		out = append(out, Warning{Path: path, Error: st.Error})
	}
	return out
}

func (e *Evaluation) flagGates(steps []node, target node, opts DepsOptions) (string, error) {
	adoptSnap := ""
	for _, n := range steps {
		end := n.endSnapshot()
		if end == "" || n.stage() == "" {
			continue
		}
		rec, err := e.eval.loadStage(n.stage())
		if err != nil {
			return "", err
		}
		if rec == nil {
			continue
		}
		if _, exists := rec.Snapshots[end]; !exists {
			continue
		}
		project, err := nodeProjectPath(n)
		if err != nil {
			return "", err
		}
		adopt := opts.Adopt && n.id == target.id
		origin := machine.SnapshotOrigin{Project: project, Scene: n.scene.Name}
		if err := machine.CheckReplace(rec, end, origin, adopt); err != nil {
			if n.id != target.id {
				if strings.Contains(err.Error(), "no origin") {
					return "", fmt.Errorf("play %s --adopt: %s would replace snapshot %q with no origin", n.path, n.id, end)
				}
				return "", fmt.Errorf("%s would replace snapshot %q: %w", n.id, end, err)
			}
			return "", err
		}
		if _, hasOrigin := rec.Origin(end); !hasOrigin && n.id == target.id {
			adoptSnap = end
		}
		if current, ok := rec.Origin(end); ok && opts.Kind == KindRehearse && !opts.ReplaceState && current.Take == machine.TakeRecording {
			return "", fmt.Errorf("rehearse --replace-state: %s would replace recording snapshot %q", n.id, end)
		}
	}
	return adoptSnap, nil
}

func nodeProjectPath(n node) (string, error) {
	if n.project == nil {
		return "", fmt.Errorf("scene %s has no project", n.id)
	}
	project, err := filepath.Abs(n.project.Dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(project); err == nil {
		project = resolved
	}
	return project, nil
}

func arrangeContinues(steps []node, all []node) ([]node, error) {
	if err := uniqueContinueParents(steps); err != nil {
		return nil, err
	}
	pairs := continuePairs(steps)
	if len(pairs) == 0 {
		return steps, nil
	}
	edges := dependents(all)
	order := append([]node{}, steps...)
	for round := 0; round < len(order)*len(order)+2; round++ {
		moved := false
		for _, pair := range pairs {
			i := nodeIndex(order, pair.pred)
			j := nodeIndex(order, pair.cont)
			if i < 0 || j < 0 {
				continue
			}
			if i > j {
				return nil, fmt.Errorf("cannot keep continue session for %s after %s: topological order puts the predecessor later", pair.cont, pair.pred)
			}
			stage := ""
			if n, ok := lookupNode(order, pair.cont); ok {
				stage = n.stage()
			}
			var between []int
			for k := i + 1; k < j; k++ {
				if order[k].stage() == stage {
					between = append(between, k)
				}
			}
			if len(between) == 0 {
				continue
			}
			k := between[0]
			x := order[k]
			if reaches(edges, pair.cont, x.id) {
				order = moveNode(order, k, i)
			} else {
				order = moveNode(order, k, j+1)
			}
			moved = true
			break
		}
		if !moved {
			return order, nil
		}
	}
	return nil, fmt.Errorf("cannot keep continue session: no order keeps each predecessor immediately before its consumer on the same stage")
}

type continuePair struct {
	pred, cont string
}

func continuePairs(steps []node) []continuePair {
	var out []continuePair
	for _, n := range steps {
		after := n.continueAfter()
		if after == "" || n.project == nil {
			continue
		}
		for _, p := range steps {
			if p.project != nil && p.scene != nil && p.project.Dir == n.project.Dir && p.scene.Name == after {
				out = append(out, continuePair{pred: p.id, cont: n.id})
			}
		}
	}
	return out
}

func uniqueContinueParents(steps []node) error {
	kids := map[string][]string{}
	stageOf := map[string]string{}
	for _, n := range steps {
		stageOf[n.id] = n.stage()
		after := n.continueAfter()
		if after == "" || n.project == nil {
			continue
		}
		for _, p := range steps {
			if p.project != nil && p.scene != nil && p.project.Dir == n.project.Dir && p.scene.Name == after {
				kids[p.id] = append(kids[p.id], n.id)
			}
		}
	}
	for pred, cs := range kids {
		if len(cs) < 2 {
			continue
		}
		sort.Strings(cs)
		return fmt.Errorf("cannot keep continue session: %s and %s both continue %s on stage %s", cs[0], cs[1], pred, stageOf[pred])
	}
	return nil
}

func reaches(edges map[string][]string, from, to string) bool {
	if from == to {
		return true
	}
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(id string) bool {
		if id == to {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		for _, next := range edges[id] {
			if walk(next) {
				return true
			}
		}
		return false
	}
	return walk(from)
}

func nodeIndex(steps []node, id string) int {
	for i, n := range steps {
		if n.id == id {
			return i
		}
	}
	return -1
}

func lookupNode(steps []node, id string) (node, bool) {
	for _, n := range steps {
		if n.id == id {
			return n, true
		}
	}
	return node{}, false
}

func moveNode(order []node, from, to int) []node {
	if from < 0 || from >= len(order) {
		return order
	}
	n := order[from]
	out := append([]node{}, order[:from]...)
	out = append(out, order[from+1:]...)
	if to > from {
		to--
	}
	if to < 0 {
		to = 0
	}
	if to > len(out) {
		to = len(out)
	}
	return append(out[:to], append([]node{n}, out[to:]...)...)
}
