package scene

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
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

// SafePath joins project-relative path parts and rejects absolute paths, ..
// escapes, or symlink ancestors that escape the project root.
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
		return "", fmt.Errorf("project root %q: %w", root, err)
	}
	if rel == "." {
		return evalRoot, nil
	}
	check := root
	// resolved tracks the canonical real path: the symlink-resolved prefix for
	// components that exist, plus any not-yet-existing tail components appended
	// verbatim. Returning this instead of the raw abs path closes the TOCTOU gap
	// where a checked component is swapped to an escaping symlink before use.
	resolved := evalRoot
	appending := false
	for _, elem := range strings.Split(rel, string(filepath.Separator)) {
		if elem == "" || elem == "." {
			continue
		}
		if appending {
			resolved = filepath.Join(resolved, elem)
			continue
		}
		check = filepath.Join(check, elem)
		if _, err := os.Lstat(check); err != nil {
			if os.IsNotExist(err) {
				// This and all remaining components do not exist yet; append
				// them verbatim to the resolved prefix.
				resolved = filepath.Join(resolved, elem)
				appending = true
				continue
			}
			return "", err
		}
		evalCheck, err := filepath.EvalSymlinks(check)
		if err != nil {
			return "", err
		}
		realRel, err := filepath.Rel(evalRoot, evalCheck)
		if err != nil {
			return "", err
		}
		if realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("path %q escapes project root %q through symlink", abs, root)
		}
		resolved = evalCheck
	}
	return resolved, nil
}

// SetProcessGroup starts cmd in a new process group so cleanup can kill the
// command and any children it spawned.
func SetProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// PropEnv is the process environment plus the project's exported env block. It is
// the single source of truth for the environment props/hooks run with.
func (p *Project) PropEnv() []string {
	env := os.Environ()
	for k, v := range p.Env {
		env = append(env, k+"="+v)
	}
	return env
}

// PropCommand builds an *exec.Cmd for a project-relative executable: it resolves
// rel through SafePath, sets the project dir + env, and inherits stdio. Callers
// that need lifecycle control (start, kill, wait separately) use this; callers
// that just want to block use RunProp. Centralizing the build keeps env/cwd/arg
// handling from drifting between the engine and the production pipeline.
func (p *Project) PropCommand(rel string, args []string) (*exec.Cmd, error) {
	path, err := p.SafePath(rel)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = p.Dir
	cmd.Env = p.PropEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	SetProcessGroup(cmd)
	return cmd, nil
}

// KillProcessGroup kills a prop command and any children it spawned. PropCommand
// starts commands in a new process group, so interrupt cleanup must target the
// group rather than only the direct child.
func KillProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if killErr := cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			return errors.Join(err, killErr)
		}
		return err
	}
	return nil
}

// RunProp builds and runs a project-relative executable to completion, wrapping a
// failure with label. It is the shared "run project prop" path for the engine's
// prop/transition steps.
func (p *Project) RunProp(label, rel string, args []string) error {
	cmd, err := p.PropCommand(rel, args)
	if err != nil {
		return err
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

// ScenePathSafe returns the project-contained path for a named scene.
func (p *Project) ScenePathSafe(name string) (string, error) {
	if err := ValidateName("scene", name); err != nil {
		return "", err
	}
	return p.SafePath("scenes", name+".json")
}
