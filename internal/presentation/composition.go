package presentation

import "github.com/This-Is-NPC/backstage/internal/scene"

// Resolve resource URLs on a copy: the authored document stays portable.
func compositionAssets(p *scene.Project, base string, source *scene.Composition) (*scene.Composition, error) {
	resolve := func(path string) (string, error) {
		abs, err := p.InputPath(path)
		if err != nil {
			return "", err
		}
		return workspaceAssetURL(p, base, abs)
	}
	c := *source
	c.Stylesheets = make([]string, len(source.Stylesheets))
	for i, path := range source.Stylesheets {
		url, err := resolve(path)
		if err != nil {
			return nil, err
		}
		c.Stylesheets[i] = url
	}
	var nodes func([]scene.VisualNode) ([]scene.VisualNode, error)
	nodes = func(input []scene.VisualNode) ([]scene.VisualNode, error) {
		result := make([]scene.VisualNode, len(input))
		for i, node := range input {
			if path, ok := node.Attributes["src"]; ok {
				node.Attributes = make(map[string]string, len(node.Attributes))
				for key, value := range input[i].Attributes {
					node.Attributes[key] = value
				}
				url, err := resolve(path)
				if err != nil {
					return nil, err
				}
				node.Attributes["src"] = url
			}
			children, err := nodes(node.Children)
			if err != nil {
				return nil, err
			}
			node.Children = children
			result[i] = node
		}
		return result, nil
	}
	var err error
	c.Nodes, err = nodes(source.Nodes)
	return &c, err
}
