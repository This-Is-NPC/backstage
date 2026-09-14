package workspace

import (
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
		for _, path := range files {
			s, err := scene.LoadScene(path)
			if err != nil {
				name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
				nodes = append(nodes, errorNode(root, p, &scene.Scene{Name: name}, err))
				continue
			}
			if s.Type == "visual" {
				continue
			}
			if err := s.Validate(p); err != nil {
				nodes = append(nodes, errorNode(root, p, s, err))
				continue
			}
			nodes = append(nodes, newNode(root, p, s))
		}
	}
	return nodes, nil
}

func errorNode(root string, p *scene.Project, s *scene.Scene, err error) node {
	n := newNode(root, p, s)
	n.loadErr = err
	return n
}

func newNode(root string, p *scene.Project, s *scene.Scene) node {
	rel := projectRel(root, p.Dir)
	id := rel + "/" + s.Name
	return node{id: id, project: p, scene: s, projectRel: rel}
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
