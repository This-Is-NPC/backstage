package scene

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	safeNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	envKeyRE   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// ValidateName rejects names that could become paths or shell syntax when used
// in recordings, productions, or transition placeholders.
func ValidateName(kind, name string) error {
	if !safeNameRE.MatchString(name) {
		return fmt.Errorf("%s name %q must match %s", kind, name, safeNameRE.String())
	}
	return nil
}

// ValidateEnvKey rejects keys that are not valid shell environment identifiers.
func ValidateEnvKey(key string) error {
	if !envKeyRE.MatchString(key) {
		return fmt.Errorf("env key %q must match %s", key, envKeyRE.String())
	}
	return nil
}

// SafePath joins project-relative path parts and rejects absolute paths or ..
// escapes from the project root.
func (p *Project) SafePath(parts ...string) (string, error) {
	root, err := filepath.Abs(p.Dir)
	if err != nil {
		return "", err
	}
	joined := root
	for _, part := range parts {
		if filepath.IsAbs(part) {
			return "", fmt.Errorf("path %q must be relative to the project", part)
		}
		joined = filepath.Join(joined, part)
	}
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes project root %q", abs, root)
	}
	evalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		evalRoot = root
	}
	check := abs
	if _, err := os.Lstat(check); err != nil {
		check = filepath.Dir(check)
	}
	if evalCheck, err := filepath.EvalSymlinks(check); err == nil {
		rel, err := filepath.Rel(evalRoot, evalCheck)
		if err != nil {
			return "", err
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("path %q escapes project root %q through symlink", abs, root)
		}
	}
	return abs, nil
}

// ScenePathSafe returns the project-contained path for a named scene.
func (p *Project) ScenePathSafe(name string) (string, error) {
	if err := ValidateName("scene", name); err != nil {
		return "", err
	}
	return p.SafePath("scenes", name+".json")
}
