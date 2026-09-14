package scene

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	namedMapKeys = []string{"vms", "layouts", "aliases", "templates", "presentations", "transitions", "productions", "env", "state-groups"}
	settingsKeys = []string{"record", "popup", "render", "hooks"}
	scalarKeys   = []string{"term"}
)

type configFile struct {
	path    string
	extends string
	raw     map[string]json.RawMessage
}

func loadConfigChain(leaf string) ([]configFile, error) {
	abs, err := filepath.Abs(leaf)
	if err != nil {
		return nil, err
	}
	var stack []configFile
	seen := map[string]bool{}
	cur := abs
	for {
		if seen[cur] {
			return nil, fmt.Errorf("extends cycle at %s", cur)
		}
		seen[cur] = true
		file, err := readConfigFile(cur)
		if err != nil {
			return nil, err
		}
		stack = append(stack, file)
		if file.extends == "" {
			break
		}
		next, err := resolveExtends(cur, file.extends)
		if err != nil {
			return nil, err
		}
		cur = next
	}
	for i, j := 0, len(stack)-1; i < j; i, j = i+1, j-1 {
		stack[i], stack[j] = stack[j], stack[i]
	}
	return stack, nil
}

func readConfigFile(path string) (configFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return configFile{}, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return configFile{}, fmt.Errorf("config %s: %w", path, err)
	}
	file := configFile{path: path, raw: raw}
	if ext, ok := raw["extends"]; ok {
		if err := json.Unmarshal(ext, &file.extends); err != nil {
			return configFile{}, fmt.Errorf("%s: extends: %w", path, err)
		}
	}
	return file, nil
}

func resolveExtends(childFile, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("%s: extends is empty", childFile)
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%s: extends %q: must be relative to the declaring file", childFile, rel)
	}
	childDir, err := filepath.Abs(filepath.Dir(childFile))
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(childDir, rel)
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%s: extends %q: file not found", childFile, rel)
		}
		return "", fmt.Errorf("%s: extends %q: %w", childFile, rel, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s: extends %q: names a directory, want %s", childFile, rel, configName)
	}
	if filepath.Base(abs) != configName {
		return "", fmt.Errorf("%s: extends %q: must name %s", childFile, rel, configName)
	}
	parentDir := filepath.Dir(abs)
	inside, err := filepath.Rel(parentDir, childDir)
	if err != nil {
		return "", err
	}
	if inside == "." || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s: extends %q: %s is not a strict ancestor of %s", childFile, rel, parentDir, childDir)
	}
	return abs, nil
}

func mergeConfigFiles(files []configFile) (map[string]json.RawMessage, map[string]string, error) {
	merged := map[string]json.RawMessage{}
	origins := map[string]string{}
	for _, file := range files {
		if err := applyConfigFile(merged, origins, file); err != nil {
			return nil, nil, err
		}
	}
	return merged, origins, nil
}

func applyConfigFile(merged map[string]json.RawMessage, origins map[string]string, file configFile) error {
	for key, val := range file.raw {
		if key == "extends" {
			continue
		}
		switch {
		case contains(namedMapKeys, key):
			if err := applyNamedMap(merged, origins, key, val, file.path); err != nil {
				return fmt.Errorf("%s: %s: %w", file.path, key, err)
			}
		case contains(settingsKeys, key):
			if err := applySettings(merged, origins, key, val, file.path, key == "popup" || key == "render"); err != nil {
				return fmt.Errorf("%s: %s: %w", file.path, key, err)
			}
		case contains(scalarKeys, key):
			if isClearValue(val) {
				delete(merged, key)
				delete(origins, key)
				continue
			}
			merged[key] = cloneRaw(val)
			origins[key] = file.path
		default:
			if isClearValue(val) {
				delete(merged, key)
				delete(origins, key)
				continue
			}
			merged[key] = cloneRaw(val)
			origins[key] = file.path
		}
	}
	return nil
}

func applyNamedMap(merged map[string]json.RawMessage, origins map[string]string, key string, val json.RawMessage, file string) error {
	if isNull(val) {
		delete(merged, key)
		deletePrefixed(origins, key+".")
		return nil
	}
	var incoming map[string]json.RawMessage
	if err := json.Unmarshal(val, &incoming); err != nil {
		return err
	}
	current := map[string]json.RawMessage{}
	if prev, ok := merged[key]; ok {
		if err := json.Unmarshal(prev, &current); err != nil {
			return err
		}
	}
	for name, entry := range incoming {
		okey := key + "." + name
		if isNull(entry) {
			delete(current, name)
			delete(origins, okey)
			continue
		}
		current[name] = cloneRaw(entry)
		origins[okey] = file
	}
	if len(current) == 0 {
		delete(merged, key)
		return nil
	}
	b, err := json.Marshal(current)
	if err != nil {
		return err
	}
	merged[key] = b
	return nil
}

func applySettings(merged map[string]json.RawMessage, origins map[string]string, key string, val json.RawMessage, file string, mergeStyle bool) error {
	if isNull(val) {
		delete(merged, key)
		deletePrefixed(origins, key+".")
		return nil
	}
	var incoming map[string]json.RawMessage
	if err := json.Unmarshal(val, &incoming); err != nil {
		return err
	}
	current := map[string]json.RawMessage{}
	if prev, ok := merged[key]; ok {
		if err := json.Unmarshal(prev, &current); err != nil {
			return err
		}
	}
	if err := mergeSettingsFields(current, incoming, origins, key+".", file, mergeStyle); err != nil {
		return err
	}
	if len(current) == 0 {
		delete(merged, key)
		return nil
	}
	b, err := json.Marshal(current)
	if err != nil {
		return err
	}
	merged[key] = b
	return nil
}

func mergeSettingsFields(dst, src map[string]json.RawMessage, origins map[string]string, prefix, file string, mergeStyle bool) error {
	for k, v := range src {
		key := prefix + k
		if mergeStyle && (k == "style" || k == "threads") && isJSONObject(v) {
			child := map[string]json.RawMessage{}
			if prev, ok := dst[k]; ok {
				if err := json.Unmarshal(prev, &child); err != nil {
					return err
				}
			}
			var incoming map[string]json.RawMessage
			if err := json.Unmarshal(v, &incoming); err != nil {
				return err
			}
			if err := mergeSettingsFields(child, incoming, origins, key+".", file, false); err != nil {
				return err
			}
			if len(child) == 0 {
				delete(dst, k)
				continue
			}
			b, err := json.Marshal(child)
			if err != nil {
				return err
			}
			dst[k] = b
			continue
		}
		if isClearValue(v) {
			delete(dst, k)
			delete(origins, key)
			continue
		}
		dst[k] = cloneRaw(v)
		origins[key] = file
	}
	return nil
}

func rewriteInheritedRefs(merged map[string]json.RawMessage, origins map[string]string, leafDir string) error {
	if hooks, ok := rawObject(merged["hooks"]); ok {
		if err := rewriteRawString(hooks, "setup", origins["hooks.setup"], leafDir); err != nil {
			return err
		}
		if err := rewriteRawString(hooks, "reset", origins["hooks.reset"], leafDir); err != nil {
			return err
		}
		b, err := json.Marshal(hooks)
		if err != nil {
			return err
		}
		merged["hooks"] = b
	}
	if err := rewriteNamedObjectField(merged, origins, "templates", "entry", leafDir); err != nil {
		return err
	}
	if err := rewriteNamedObjectField(merged, origins, "presentations", "file", leafDir); err != nil {
		return err
	}
	return rewriteLiveProps(merged, origins, leafDir)
}

func rewriteNamedObjectField(merged map[string]json.RawMessage, origins map[string]string, mapKey, field, leafDir string) error {
	obj, ok := rawObject(merged[mapKey])
	if !ok {
		return nil
	}
	for name, entryRaw := range obj {
		entry, ok := rawObject(entryRaw)
		if !ok {
			continue
		}
		if err := rewriteRawString(entry, field, origins[mapKey+"."+name], leafDir); err != nil {
			return err
		}
		b, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		obj[name] = b
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	merged[mapKey] = b
	return nil
}

func rewriteLiveProps(merged map[string]json.RawMessage, origins map[string]string, leafDir string) error {
	obj, ok := rawObject(merged["transitions"])
	if !ok {
		return nil
	}
	for name, entryRaw := range obj {
		entry, ok := rawObject(entryRaw)
		if !ok {
			continue
		}
		live, ok := rawObject(entry["live"])
		if !ok {
			continue
		}
		if err := rewriteRawString(live, "prop", origins["transitions."+name], leafDir); err != nil {
			return err
		}
		b, err := json.Marshal(live)
		if err != nil {
			return err
		}
		entry["live"] = b
		b, err = json.Marshal(entry)
		if err != nil {
			return err
		}
		obj[name] = b
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	merged["transitions"] = b
	return nil
}

func rewriteRawString(obj map[string]json.RawMessage, key, originFile, leafDir string) error {
	raw, ok := obj[key]
	if !ok || originFile == "" {
		return nil
	}
	var rel string
	if err := json.Unmarshal(raw, &rel); err != nil {
		return err
	}
	if rel == "" {
		return nil
	}
	if filepath.IsAbs(rel) {
		return nil
	}
	originDir, err := filepath.Abs(filepath.Dir(originFile))
	if err != nil {
		return err
	}
	leafAbs, err := filepath.Abs(leafDir)
	if err != nil {
		return err
	}
	if originDir == leafAbs {
		return nil
	}
	abs := filepath.Join(originDir, filepath.FromSlash(rel))
	rewritten, err := filepath.Rel(leafAbs, abs)
	if err != nil {
		return err
	}
	b, err := json.Marshal(filepath.ToSlash(rewritten))
	if err != nil {
		return err
	}
	obj[key] = b
	return nil
}

func rawObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	return obj, true
}

func isNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func isJSONObject(raw json.RawMessage) bool {
	s := bytes.TrimSpace(raw)
	return len(s) > 0 && s[0] == '{'
}

func isClearValue(raw json.RawMessage) bool {
	s := bytes.TrimSpace(raw)
	if bytes.Equal(s, []byte("null")) || bytes.Equal(s, []byte(`""`)) {
		return true
	}
	if len(s) == 0 {
		return false
	}
	if s[0] != '-' && (s[0] < '0' || s[0] > '9') {
		return false
	}
	var n float64
	return json.Unmarshal(s, &n) == nil && n == 0
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}

func deletePrefixed(origins map[string]string, prefix string) {
	for key := range origins {
		if strings.HasPrefix(key, prefix) {
			delete(origins, key)
		}
	}
}

func contains(list []string, key string) bool {
	for _, item := range list {
		if item == key {
			return true
		}
	}
	return false
}
