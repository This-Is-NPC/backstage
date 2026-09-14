package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

const configName = "backstage.json"

type projectFault struct {
	Dir   string
	Rel   string
	Error string
}

func workspaceRoot(dir string) (string, error) {
	cfg, err := nearestConfig(dir)
	if err != nil {
		return "", err
	}
	p, err := scene.LoadProject(cfg)
	if err != nil {
		return "", err
	}
	return absResolved(p.WorkspaceRoot())
}

func nearestConfig(dir string) (string, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		c := filepath.Join(d, configName)
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("no %s found above %s", configName, dir)
		}
		d = parent
	}
}

func discoverProjects(root string) ([]*scene.Project, []projectFault, []Warning, error) {
	root = filepath.Clean(root)
	skip := map[string]bool{}
	var projects []*scene.Project
	var faults []projectFault
	var warnings []Warning
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root && d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() || isDir(path) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != root {
			if strings.HasPrefix(d.Name(), ".") || skip[filepath.Clean(path)] {
				return fs.SkipDir
			}
		}
		cfg := filepath.Join(path, configName)
		st, err := os.Stat(cfg)
		if err != nil || st.IsDir() {
			return nil
		}
		p, err := scene.LoadProject(cfg)
		if err != nil {
			chainRoot, chainErr := scene.ExtendsRoot(cfg)
			if chainErr != nil || !sameResolved(chainRoot, root) {
				warnings = append(warnings, Warning{Path: cfg, Error: err.Error()})
				return nil
			}
			faults = append(faults, projectFault{
				Dir:   path,
				Rel:   projectRel(root, path),
				Error: err.Error(),
			})
			return nil
		}
		if !sameResolved(p.WorkspaceRoot(), root) {
			return nil
		}
		projects = append(projects, p)
		registerRecordOut(p, skip)
		return nil
	})
	if err != nil {
		return nil, faults, warnings, err
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Dir < projects[j].Dir })
	sort.Slice(faults, func(i, j int) bool { return faults[i].Rel < faults[j].Rel })
	sort.Slice(warnings, func(i, j int) bool { return warnings[i].Path < warnings[j].Path })
	return projects, faults, warnings, nil
}

func registerRecordOut(p *scene.Project, skip map[string]bool) {
	out := p.Record.Out
	if out == "" {
		out = "recordings"
	}
	abs, err := p.OutputPath(out)
	if err != nil {
		return
	}
	skip[filepath.Clean(abs)] = true
}

func isDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

func loadRecordingScenes(root string, projects []*scene.Project) ([]node, error) {
	var nodes []node
	for _, p := range projects {
		files, err := filepath.Glob(filepath.Join(p.Dir, "scenes", "*.json"))
		if err != nil {
			return nil, err
		}
		sort.Strings(files)
		var batch []node
		for _, path := range files {
			s, err := scene.LoadScene(path)
			if err != nil {
				name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
				batch = append(batch, errorNode(root, p, &scene.Scene{Name: name}, path, err))
				continue
			}
			if s.Type == "visual" {
				continue
			}
			if err := s.Validate(p); err != nil {
				batch = append(batch, errorNode(root, p, s, path, err))
				continue
			}
			batch = append(batch, newNode(root, p, s, path))
		}
		nodes = append(nodes, markDuplicateSceneNames(root, p, batch)...)
	}
	return nodes, nil
}

func markDuplicateSceneNames(root string, p *scene.Project, batch []node) []node {
	byName := map[string][]node{}
	for _, n := range batch {
		if n.scene == nil || n.scene.Name == "" {
			continue
		}
		byName[n.scene.Name] = append(byName[n.scene.Name], n)
	}
	var names []string
	for name, group := range byName {
		if len(group) > 1 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	msg := map[string]string{}
	for _, name := range names {
		var files []string
		for _, n := range byName[name] {
			files = append(files, sceneFileLabel(p, n.path))
		}
		sort.Strings(files)
		msg[name] = fmt.Sprintf("duplicate scene name %s: %s", name, strings.Join(files, ", "))
	}
	out := make([]node, 0, len(batch))
	for _, n := range batch {
		if n.scene != nil {
			if errText, ok := msg[n.scene.Name]; ok {
				dup := errors.New(errText)
				if n.loadErr != nil {
					n.loadErr = fmt.Errorf("%s; %s", n.loadErr.Error(), errText)
				} else {
					n.loadErr = dup
				}
				n.id = n.id + "@" + filepath.Base(n.path)
			}
		}
		out = append(out, n)
	}
	return out
}

func sceneFileLabel(p *scene.Project, path string) string {
	if p != nil {
		if rel, err := filepath.Rel(p.Dir, path); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(path)
}

func errorNode(root string, p *scene.Project, s *scene.Scene, path string, err error) node {
	n := newNode(root, p, s, path)
	n.loadErr = err
	return n
}

func newNode(root string, p *scene.Project, s *scene.Scene, path string) node {
	rel := projectRel(root, p.Dir)
	id := rel + "/" + s.Name
	return node{id: id, project: p, scene: s, projectRel: rel, path: path}
}

func projectRel(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return filepath.ToSlash(dir)
	}
	return filepath.ToSlash(rel)
}

func absResolved(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func resolvePath(path string) string {
	resolved, err := absResolved(path)
	if err != nil {
		abs, aerr := filepath.Abs(path)
		if aerr != nil {
			return filepath.Clean(path)
		}
		return abs
	}
	return resolved
}

func sameResolved(a, b string) bool {
	return filepath.Clean(resolvePath(a)) == filepath.Clean(resolvePath(b))
}
