package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/This-Is-NPC/backstage/internal/facts"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/scene"
	"github.com/This-Is-NPC/backstage/internal/take"
)

type stageCache struct {
	rec *machine.Record
	err error
}

type evaluator struct {
	store  *machine.Store
	nodes  []node
	faults []projectFault
	stages map[string]stageCache
	status map[string]SceneStatus
	made   map[string]node // stage\0snapshot → producer
}

func newEvaluator(store *machine.Store, nodes []node, faults []projectFault) *evaluator {
	e := &evaluator{
		store:  store,
		nodes:  nodes,
		faults: faults,
		stages: map[string]stageCache{},
		status: map[string]SceneStatus{},
		made:   map[string]node{},
	}
	for _, n := range nodes {
		if n.loadErr != nil {
			continue
		}
		if n.endSnapshot() != "" && n.stage() != "" {
			e.made[producerKey(n.stage(), n.endSnapshot())] = n
		}
	}
	for _, n := range nodes {
		if n.loadErr == nil {
			continue
		}
		snap := n.graphEndSnapshot()
		if snap == "" || n.stage() == "" {
			continue
		}
		k := producerKey(n.stage(), snap)
		if _, ok := e.made[k]; !ok {
			e.made[k] = n
		}
	}
	return e
}

func (e *evaluator) loadStage(name string) (*machine.Record, error) {
	if name == "" {
		return nil, nil
	}
	if cached, ok := e.stages[name]; ok {
		return cached.rec, cached.err
	}
	rec, err := e.store.Load(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			e.stages[name] = stageCache{}
			return nil, nil
		}
		e.stages[name] = stageCache{err: err}
		return nil, err
	}
	e.stages[name] = stageCache{rec: rec}
	return rec, nil
}

func (e *evaluator) scene(n node) (SceneStatus, error) {
	st := SceneStatus{
		Scene:         n.scene.Name,
		Project:       n.project.Dir,
		ProjectRel:    n.projectRel,
		Stage:         n.stage(),
		StartSnapshot: n.startSnapshot(),
		EndSnapshot:   n.endSnapshot(),
	}
	if n.loadErr != nil {
		st.Error = n.loadErr.Error()
		st.Reasons = []string{Error, st.Error}
		st.Status = Error
		st.Detail = st.Error
		if extra := e.alsoDeclaresEnd(n); extra != "" {
			st.Detail = st.Detail + "; " + extra
		}
		e.status[n.id] = st
		return st, nil
	}
	if paths, err := take.ForScene(n.project, n.scene.Name); err == nil {
		st.StableClip = paths.StableClip()
		st.StableFacts = paths.StableFacts()
	}
	rec, err := e.loadStage(n.stage())
	if err != nil {
		return SceneStatus{}, err
	}
	reasons, err := e.localReasons(n, rec, &st)
	if err != nil {
		return SceneStatus{}, err
	}
	if name, status, ok := e.upstreamStale(n, rec); ok {
		reasons[StaleUpstream] = true
		st.upstreamName = name
		st.upstreamStat = status
	}
	st.Reasons = selectedReasons(reasons)
	if reasons[Error] && st.Error != "" {
		st.Reasons = append(st.Reasons, st.Error)
	}
	st.Status = firstStatus(st.Reasons)
	st.Detail = e.statusDetail(n, st)
	e.status[n.id] = st
	return st, nil
}

func (e *evaluator) localReasons(n node, rec *machine.Record, st *SceneStatus) (map[string]bool, error) {
	out := map[string]bool{}
	mode := n.scene.VMStartMode()
	if n.scene.VM != "" && mode == "clean" {
		snap := n.startSnapshot()
		exists := rec != nil && rec.Snapshots[snap] != ""
		if !exists {
			if _, made := e.made[producerKey(n.stage(), snap)]; made {
				out[BlockedStateMissing] = true
			} else {
				out[BlockedNoProducer] = true
			}
		} else if origin, ok := rec.Origin(snap); ok && origin.Take == machine.TakeRehearsal {
			out[BlockedRehearsalState] = true
		}
	}

	h, err := take.Open(n.project, n.scene.Name)
	if err != nil {
		if errors.Is(err, take.ErrNotPublished) {
			out[Missing] = true
			return out, nil
		}
		out[Error] = true
		st.Error = err.Error()
		st.Detail = err.Error()
		return out, nil
	}
	defer h.Close()
	st.Clip = h.Clip
	st.Facts = h.Facts

	f, ferr := facts.Read(h.Facts)
	cur, derr := digestFromFacts(n, f)
	digestOK := ferr == nil && derr == nil && f.InputsSHA256 != "" && f.InputsSHA256 == cur
	if ferr != nil || derr != nil || f.InputsSHA256 == "" || f.InputsSHA256 != cur {
		out[StaleInputs] = true
	}
	if n.scene.VMEnd != nil {
		want := snapshotImage(rec, n.endSnapshot())
		if f.EndState == nil || f.EndState.Image != want {
			out[StaleStateMismatch] = true
		}
	}
	if n.scene.VM != "" && mode == "clean" {
		if f.StartImage != snapshotImage(rec, n.startSnapshot()) {
			out[StaleStartState] = true
		}
	}
	if n.scene.VM != "" && mode == "reuse" && digestOK {
		out[Unverifiable] = true
	}
	reuseVM := n.scene.VM != "" && mode == "reuse"
	if digestOK && !out[StaleStateMismatch] && !out[StaleStartState] && !reuseVM {
		out[OK] = true
	}
	return out, nil
}

func (e *evaluator) upstreamStale(n node, rec *machine.Record) (name, status string, ok bool) {
	if snap := n.startSnapshot(); snap != "" && n.stage() != "" && rec != nil {
		origin, hasOrigin := rec.Origin(snap)
		p, exists := e.made[producerKey(n.stage(), snap)]
		if hasOrigin && exists && sameOrigin(origin, p) {
			if p.loadErr != nil {
				return p.scene.Name, Error, true
			}
			st := e.status[p.id]
			if st.Status != "" && st.Status != OK {
				return p.scene.Name, st.Status, true
			}
		}
		if hasOrigin {
			if errName, found := e.originError(origin); found {
				return errName, Error, true
			}
		}
	}
	if after := n.continueAfter(); after != "" {
		for _, p := range e.nodes {
			if p.project.Dir == n.project.Dir && p.scene.Name == after {
				if p.loadErr != nil {
					return p.scene.Name, Error, true
				}
				st := e.status[p.id]
				if st.Status != OK {
					return p.scene.Name, st.Status, true
				}
			}
		}
	}
	return "", "", false
}

func (e *evaluator) alsoDeclaresEnd(n node) string {
	if n.scene == nil || n.scene.VMEnd == nil {
		return ""
	}
	snap := n.scene.VMEnd.Snapshot
	stage := n.stage()
	if snap == "" || stage == "" {
		return ""
	}
	p, ok := e.made[producerKey(stage, snap)]
	if !ok || p.loadErr != nil || p.id == n.id {
		return ""
	}
	return "also declares vm-end " + snap
}

func (e *evaluator) originError(o machine.SnapshotOrigin) (string, bool) {
	for _, n := range e.nodes {
		if n.scene == nil || n.loadErr == nil {
			continue
		}
		if n.scene.Name == o.Scene && sameProject(o.Project, n.project.Dir) {
			return n.scene.Name, true
		}
	}
	for _, f := range e.faults {
		if sameProject(o.Project, f.Dir) {
			if f.Rel == "" || f.Rel == "." {
				return filepath.Base(f.Dir), true
			}
			return f.Rel, true
		}
	}
	return "", false
}

func sameOrigin(o machine.SnapshotOrigin, p node) bool {
	if o.Scene != p.scene.Name {
		return false
	}
	return sameProject(o.Project, p.project.Dir)
}

func sameProject(originProject, nodeDir string) bool {
	if originProject == "" || nodeDir == "" {
		return false
	}
	a, err := filepath.Abs(originProject)
	if err != nil {
		return false
	}
	b, err := filepath.Abs(nodeDir)
	if err != nil {
		return false
	}
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func (e *evaluator) labels() ([]StateLabel, error) {
	seen := map[string]StateLabel{}
	for _, n := range e.nodes {
		stage := n.stage()
		if stage == "" {
			continue
		}
		rec, err := e.loadStage(stage)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			continue
		}
		for snap := range rec.Snapshots {
			k := producerKey(stage, snap)
			lab := StateLabel{Stage: stage, Snapshot: snap, Label: ManualLabel}
			if origin, ok := rec.Origin(snap); ok && origin.Scene != "" {
				lab.Label = origin.Scene
			}
			seen[k] = lab
		}
	}
	out := []StateLabel{}
	for _, v := range seen {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Stage != out[j].Stage {
			return out[i].Stage < out[j].Stage
		}
		return out[i].Snapshot < out[j].Snapshot
	})
	return out, nil
}

func digestFromFacts(n node, f facts.Facts) (string, error) {
	img := ""
	if n.scene.VMStartMode() == "clean" {
		img = f.StartImage
	}
	return scene.InputsDigest(n.project, n.scene, scene.DigestOptions{
		Speed: 1, ShowStaging: false, StartState: scene.StartStateToken(n.scene, img),
	})
}

func snapshotImage(rec *machine.Record, snap string) string {
	if rec == nil || snap == "" {
		return ""
	}
	return rec.Snapshots[snap]
}

func selectedReasons(m map[string]bool) []string {
	var out []string
	for _, name := range precedence {
		if name == OK {
			continue
		}
		if m[name] {
			out = append(out, name)
		}
	}
	if len(out) == 0 && m[OK] {
		return []string{OK}
	}
	return out
}

func firstStatus(reasons []string) string {
	if len(reasons) == 0 {
		return Missing
	}
	return reasons[0]
}

func (e *evaluator) statusDetail(n node, st SceneStatus) string {
	switch st.Status {
	case OK:
		var parts []string
		if n.stage() != "" {
			parts = append(parts, "stage "+n.stage())
		}
		if n.endSnapshot() != "" {
			parts = append(parts, "makes "+n.endSnapshot())
		}
		return joinDetail(parts)
	case Error:
		if st.Detail != "" {
			return st.Detail
		}
		return st.Error
	case Missing:
		return ""
	case StaleInputs:
		return "inputs changed"
	case StaleStartState:
		return "start image is not the current snapshot"
	case StaleStateMismatch:
		return "end-state does not match the current snapshot"
	case StaleUpstream:
		if st.upstreamName != "" {
			status := st.upstreamStat
			if status == "" {
				status = Missing
			}
			return fmt.Sprintf("%s is %s", st.upstreamName, status)
		}
		return "upstream take is not ok"
	case BlockedStateMissing:
		return n.startSnapshot() + " is missing"
	case BlockedNoProducer:
		return n.startSnapshot() + " does not exist"
	case BlockedRehearsalState:
		return n.startSnapshot() + " came from a rehearsal"
	case Unverifiable:
		return "reuse start state is unknown"
	default:
		return st.Detail
	}
}

func joinDetail(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " · "
		}
		out += p
	}
	return out
}
