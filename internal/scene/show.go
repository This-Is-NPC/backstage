package scene

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// WriteConfigShow prints the merged configuration and the file that supplied
// each key. Keys filled only by defaults are marked [default].
func WriteConfigShow(w io.Writer, p *Project) error {
	if p == nil {
		return fmt.Errorf("no project")
	}
	fmt.Fprintf(w, "workspace: %s\n", p.WorkspaceRoot())
	fmt.Fprintf(w, "project: %s\n", p.Dir)
	if p.Extends != "" {
		fmt.Fprintf(w, "extends: %s\n", p.Extends)
	}
	fmt.Fprintln(w)
	writeShowValue(w, p, "term", p.Term)
	writeShowValue(w, p, "record.monitor", p.Record.Monitor)
	writeShowValue(w, p, "record.fps", p.Record.FPS)
	writeShowValue(w, p, "record.out", p.Record.Out)
	writeShowValue(w, p, "popup.size", p.Popup.Size)
	writeShowValue(w, p, "popup.cps", p.Popup.CPS)
	writeShowValue(w, p, "popup.style.fontSize", p.Popup.Style.FontSize)
	writeShowValue(w, p, "popup.style.title", p.Popup.Style.Title)
	writeShowValue(w, p, "popup.style.header", p.Popup.Style.Header)
	writeShowValue(w, p, "popup.style.chrome", p.Popup.Style.Chrome)
	writeShowValue(w, p, "popup.style.class", p.Popup.Style.Class)
	writeShowValue(w, p, "render.w", p.Render.W)
	writeShowValue(w, p, "render.h", p.Render.H)
	writeShowValue(w, p, "render.fps", p.Render.FPS)
	writeShowValue(w, p, "hooks.setup", p.Hooks.Setup)
	writeShowValue(w, p, "hooks.reset", p.Hooks.Reset)
	writeShowMap(w, p, "env", stringMap(p.Env))
	writeShowMap(w, p, "aliases", anyMap(p.Aliases))
	writeShowMap(w, p, "layouts", anyMap(p.Layouts))
	writeShowMap(w, p, "vms", anyMap(p.VMs))
	writeShowMap(w, p, "templates", anyMap(p.Templates))
	writeShowMap(w, p, "presentations", anyMap(p.Presentations))
	writeShowMap(w, p, "transitions", anyMap(p.Transitions))
	writeShowMap(w, p, "productions", anyMap(p.Productions))
	return nil
}

func writeShowValue(w io.Writer, p *Project, key string, value any) {
	fmt.Fprintf(w, "%s = %s  [%s]\n", key, compactJSON(value), showOrigin(p, key))
}

func writeShowMap[T any](w io.Writer, p *Project, prefix string, values map[string]T) {
	if len(values) == 0 {
		return
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		key := prefix + "." + name
		fmt.Fprintf(w, "%s = %s  [%s]\n", key, compactJSON(values[name]), showOrigin(p, key))
	}
}

func showOrigin(p *Project, key string) string {
	file, ok := p.Origins[key]
	if !ok || file == "" {
		return "default"
	}
	rel, err := filepath.Rel(p.WorkspaceRoot(), file)
	if err != nil || strings.HasPrefix(rel, "..") {
		return file
	}
	return filepath.ToSlash(rel)
}

func compactJSON(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(b)
}

func stringMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func anyMap[T any](m map[string]T) map[string]T {
	if m == nil {
		return map[string]T{}
	}
	return m
}
