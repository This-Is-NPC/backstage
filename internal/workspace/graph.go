package workspace

import (
	"sort"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

type node struct {
	id         string
	project    *scene.Project
	scene      *scene.Scene
	projectRel string
	path       string
	loadErr    error
}

func (n node) stage() string {
	if n.scene == nil || n.scene.VM == "" || n.project == nil {
		return ""
	}
	vm, ok := n.project.VMs[n.scene.VM]
	if !ok {
		return ""
	}
	return vm.Stage
}

func (n node) startSnapshot() string {
	if n.scene == nil || n.scene.VM == "" || n.scene.VMStartMode() != "clean" {
		return ""
	}
	if n.scene.VMStart != nil && n.scene.VMStart.Snapshot != "" {
		return n.scene.VMStart.Snapshot
	}
	return "initial"
}

func (n node) endSnapshot() string {
	if n.scene == nil || n.scene.VMEnd == nil {
		return ""
	}
	return n.scene.VMEnd.Snapshot
}

func (n node) continueAfter() string {
	if n.scene == nil || n.scene.VMStartMode() != "continue" || n.scene.VMStart == nil {
		return ""
	}
	return n.scene.VMStart.After
}

func (n node) graphStartSnapshot() string {
	if n.scene == nil || n.project == nil || n.scene.VM == "" || n.scene.VMStartMode() != "clean" {
		return ""
	}
	if err := n.scene.ValidateVMStart(n.project); err != nil {
		return ""
	}
	return n.startSnapshot()
}

func (n node) graphEndSnapshot() string {
	if n.scene == nil || n.scene.VMEnd == nil || n.project == nil {
		return ""
	}
	if err := n.scene.ValidateVMEnd(n.project); err != nil {
		return ""
	}
	return n.scene.VMEnd.Snapshot
}

func (n node) graphContinueAfter() string {
	if n.scene == nil || n.project == nil || n.scene.VMStartMode() != "continue" || n.scene.VMStart == nil {
		return ""
	}
	if n.scene.VMStart.After == n.scene.Name {
		return ""
	}
	if err := n.scene.ValidateVMStart(n.project); err != nil {
		return ""
	}
	return n.scene.VMStart.After
}

func validNodes(nodes []node) []node {
	out := make([]node, 0, len(nodes))
	for _, n := range nodes {
		if n.loadErr == nil {
			out = append(out, n)
		}
	}
	return out
}

func producerKey(stage, snapshot string) string {
	return stage + "\x00" + snapshot
}

func validateGraph(nodes []node) error {
	cerr := &ConflictError{}
	producers := map[string][]string{}
	for _, n := range validNodes(nodes) {
		snap := n.graphEndSnapshot()
		if snap == "" || n.stage() == "" {
			continue
		}
		k := producerKey(n.stage(), snap)
		producers[k] = append(producers[k], n.id)
	}
	var keys []string
	for k := range producers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ids := producers[k]
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		stage, snap, _ := splitKey(k)
		cerr.Duplicates = append(cerr.Duplicates, DuplicateProducer{Stage: stage, Snapshot: snap, Scenes: ids})
	}
	if cycles := findCycles(validNodes(nodes)); len(cycles) > 0 {
		cerr.Cycles = cycles
	}
	if len(cerr.Duplicates) > 0 || len(cerr.Cycles) > 0 {
		return cerr
	}
	return nil
}

func splitKey(k string) (stage, snap string, ok bool) {
	i := 0
	for i < len(k) && k[i] != 0 {
		i++
	}
	if i >= len(k) {
		return k, "", false
	}
	return k[:i], k[i+1:], true
}

// edges: dependency from consumer to producer (consumer needs producer).
func dependents(nodes []node) map[string][]string {
	nodes = validNodes(nodes)
	byID := map[string]node{}
	byScene := map[string][]node{}
	producers := map[string]node{}
	for _, n := range nodes {
		byID[n.id] = n
		byScene[n.project.Dir+"\x00"+n.scene.Name] = append(byScene[n.project.Dir+"\x00"+n.scene.Name], n)
		if snap := n.graphEndSnapshot(); snap != "" && n.stage() != "" {
			producers[producerKey(n.stage(), snap)] = n
		}
	}
	edges := map[string][]string{}
	add := func(from, to string) {
		if from == "" || to == "" {
			return
		}
		edges[from] = append(edges[from], to)
	}
	for _, n := range nodes {
		if snap := n.graphStartSnapshot(); snap != "" && n.stage() != "" {
			if p, ok := producers[producerKey(n.stage(), snap)]; ok {
				add(n.id, p.id)
			}
		}
		if after := n.graphContinueAfter(); after != "" {
			key := n.project.Dir + "\x00" + after
			for _, p := range byScene[key] {
				add(n.id, p.id)
			}
		}
	}
	return edges
}

func topoScenes(nodes []node) ([]node, error) {
	edges := dependents(nodes)
	incoming := map[string]int{}
	for _, n := range nodes {
		incoming[n.id] = 0
	}
	for from, tos := range edges {
		incoming[from] += len(tos)
	}
	byID := map[string]node{}
	for _, n := range nodes {
		byID[n.id] = n
	}
	var ready []string
	for _, n := range nodes {
		if incoming[n.id] == 0 {
			ready = append(ready, n.id)
		}
	}
	sort.Strings(ready)
	rev := reverseEdges(edges)
	var order []node
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, byID[id])
		for _, consumer := range rev[id] {
			incoming[consumer]--
			if incoming[consumer] == 0 {
				ready = append(ready, consumer)
				sort.Strings(ready)
			}
		}
	}
	if len(order) != len(nodes) {
		return nil, &ConflictError{Cycles: findCycles(nodes)}
	}
	return order, nil
}

func reverseEdges(edges map[string][]string) map[string][]string {
	rev := map[string][]string{}
	for from, tos := range edges {
		for _, to := range tos {
			rev[to] = append(rev[to], from)
		}
	}
	return rev
}

func findCycles(nodes []node) [][]string {
	edges := dependents(nodes)
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string
	var cycles [][]string
	var visit func(string)
	visit = func(id string) {
		color[id] = gray
		stack = append(stack, id)
		for _, dep := range edges[id] {
			switch color[dep] {
			case white:
				visit(dep)
			case gray:
				cyc := cycleFrom(stack, dep)
				if len(cyc) > 0 {
					cycles = append(cycles, cyc)
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
	}
	var ids []string
	for _, n := range nodes {
		ids = append(ids, n.id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if color[id] == white {
			visit(id)
		}
	}
	return cycles
}

func cycleFrom(stack []string, start string) []string {
	i := 0
	for i < len(stack) && stack[i] != start {
		i++
	}
	if i == len(stack) {
		return nil
	}
	cyc := append(append([]string{}, stack[i:]...), start)
	return cyc
}
