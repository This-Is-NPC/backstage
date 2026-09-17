package take

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

// PruneOptions select abandoned pendings, and optionally old or large takes.
type PruneOptions struct {
	OlderThan time.Duration
	MaxSize   int64
	DryRun    bool
	Now       time.Time
}

// PruneResult lists what prune removed, moved, skipped or repaired.
type PruneResult struct {
	Removed  []string
	Moved    []string
	Skipped  []string
	Repaired []string
}

// Prune cleans record.out/.takes. With no age or size, it only handles
// abandoned pending directories: no clip deletes, a non-empty clip moves
// to attempts.
func Prune(ctx context.Context, p *scene.Project, opts PruneOptions) (PruneResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	out := p.Record.Out
	if out == "" {
		out = "recordings"
	}
	root, err := p.OutputPath(out)
	if err != nil {
		return PruneResult{}, err
	}
	takesRoot := filepath.Join(root, takesDir)
	entries, err := os.ReadDir(takesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return PruneResult{}, nil
		}
		return PruneResult{}, err
	}
	var scenes []string
	var skippedScenes []string
	var skippedSize int64
	var result PruneResult
	for _, e := range entries {
		if err := scene.ValidateName("scene", e.Name()); err != nil {
			continue
		}
		if _, err := scene.ConfinedPath(root, root, takesDir, e.Name()); err != nil {
			skippedScenes = append(skippedScenes, e.Name())
			result.Skipped = append(result.Skipped, filepath.Join(takesRoot, e.Name()))
			skippedSize += dirSize(filepath.Join(takesRoot, e.Name()))
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		scenes = append(scenes, e.Name())
	}
	sort.Strings(scenes)

	var aged []agedItem
	var deletedPending int64
	type work struct {
		paths Paths
	}
	var jobs []work
	for _, name := range scenes {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		paths := Paths{OutDir: root, Scene: name}
		if err := requireReadableManifest(paths); err != nil {
			skippedScenes = append(skippedScenes, name)
			result.Skipped = append(result.Skipped, paths.Manifest())
			skippedSize += dirSize(paths.sceneDir())
			continue
		}
		lock, err := acquireSceneLock(ctx, paths, opts)
		if err != nil {
			return result, err
		}
		err = func() error {
			defer closeFile(lock)
			if afterSceneLockForTest != nil {
				afterSceneLockForTest(name)
			}
			if err := pruneSceneTemps(paths, opts, &result); err != nil {
				return err
			}
			pendingAged, deleted, err := prunePendings(paths, opts, &result)
			if err != nil {
				return err
			}
			deletedPending += deleted
			current, err := currentGeneration(paths)
			if err != nil {
				return err
			}
			if opts.OlderThan > 0 || opts.MaxSize > 0 {
				items, skipped, err := collectAged(paths, current)
				if err != nil {
					return err
				}
				result.Skipped = append(result.Skipped, skipped...)
				aged = append(aged, items...)
				if opts.DryRun {
					aged = append(aged, pendingAged...)
				}
			}
			jobs = append(jobs, work{paths: paths})
			return nil
		}()
		if err != nil {
			return result, err
		}
	}

	if afterCollectForTest != nil {
		afterCollectForTest()
	}

	if opts.OlderThan > 0 || opts.MaxSize > 0 {
		total := dirSize(takesRoot) - skippedSize
		if opts.DryRun {
			total -= deletedPending
		}
		drop := selectAged(aged, opts, total)
		for _, it := range drop {
			if opts.DryRun {
				result.Removed = append(result.Removed, it.path)
				continue
			}
			if err := removeAged(ctx, it, opts, &result); err != nil {
				return result, err
			}
		}
	}

	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		lock, err := acquireSceneLock(ctx, job.paths, opts)
		if err != nil {
			return result, err
		}
		err = func() error {
			defer closeFile(lock)
			current, err := currentGeneration(job.paths)
			if err != nil {
				skippedScenes = append(skippedScenes, job.paths.Scene)
				result.Skipped = append(result.Skipped, job.paths.Manifest())
				return nil
			}
			return repairProjection(job.paths, current, opts, &result)
		}()
		if err != nil {
			return result, err
		}
	}
	if len(skippedScenes) > 0 {
		return result, fmt.Errorf("cannot prune %s: scene skipped", strings.Join(skippedScenes, ", "))
	}
	return result, nil
}

// afterCollectForTest runs between candidate collection and projection repair.
var afterCollectForTest func()

// afterSceneLockForTest runs while the collect loop still holds that scene's lock.
var afterSceneLockForTest func(name string)

func requireReadableManifest(p Paths) error {
	st, err := os.Stat(p.Manifest())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("cannot prune %s: take manifest is not a file", p.Scene)
	}
	if _, err := readManifest(p.Manifest()); err != nil {
		return fmt.Errorf("cannot prune %s: %w", p.Scene, err)
	}
	return nil
}

func acquireSceneLock(ctx context.Context, p Paths, opts PruneOptions) (*os.File, error) {
	if opts.DryRun {
		return lockExclusiveExisting(ctx, p.lockPath(), ">> waiting for scene lock")
	}
	return lockExclusive(ctx, p.lockPath(), ">> waiting for scene lock")
}

func pruneSceneTemps(p Paths, opts PruneOptions, result *PruneResult) error {
	patterns := []string{
		"." + p.Scene + ".mp4.*.tmp",
		"." + p.Scene + ".facts.json.*.tmp",
		"." + p.Scene + ".take.json.*.tmp",
	}
	for _, pat := range patterns {
		matches, err := filepath.Glob(filepath.Join(p.OutDir, pat))
		if err != nil {
			return err
		}
		for _, path := range matches {
			if opts.DryRun {
				result.Removed = append(result.Removed, path)
				continue
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			result.Removed = append(result.Removed, path)
		}
	}
	return nil
}

func prunePendings(p Paths, opts PruneOptions, result *PruneResult) ([]agedItem, int64, error) {
	entries, err := os.ReadDir(p.sceneDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	var moved []agedItem
	var deleted int64
	for _, e := range entries {
		name := e.Name()
		prefix := ""
		switch {
		case strings.HasPrefix(name, pendingPrefix):
			prefix = pendingPrefix
		case strings.HasPrefix(name, creatingPrefix):
			prefix = creatingPrefix
		default:
			continue
		}
		id := strings.TrimPrefix(name, prefix)
		if prefix == creatingPrefix {
			at, err := parseIDTime(id)
			if err != nil {
				continue
			}
			if opts.Now.Sub(at) < creatingMaxAge {
				continue
			}
		}
		dir, err := confinedNoLink(p, name)
		if err != nil {
			continue
		}
		lease := filepath.Join(dir, leaseName)
		if _, err := os.Lstat(lease); err == nil {
			held, err := tryLockExclusive(lease)
			if err != nil {
				result.Skipped = append(result.Skipped, dir)
				continue
			}
			closeFile(held)
		}
		clip := filepath.Join(dir, clipName)
		if st, err := os.Stat(clip); err == nil && st.Size() > 0 {
			dest, destErr := confinedAttemptDest(p, id)
			item := agedItem{path: dest, at: idTime(id), size: dirSize(dir), paths: p, id: id, kind: "attempt"}
			if destErr != nil {
				result.Skipped = append(result.Skipped, dir)
				continue
			}
			if opts.DryRun {
				result.Moved = append(result.Moved, dest)
				moved = append(moved, item)
				continue
			}
			if err := os.MkdirAll(p.attemptsDir(), 0o700); err != nil {
				return nil, deleted, err
			}
			if err := os.Rename(dir, dest); err != nil {
				return nil, deleted, err
			}
			result.Moved = append(result.Moved, dest)
			moved = append(moved, agedItem{path: dest, at: idTime(id), size: dirSize(dest), paths: p, id: id, kind: "attempt"})
			continue
		}
		deleted += dirSize(dir)
		if opts.DryRun {
			result.Removed = append(result.Removed, dir)
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return nil, deleted, err
		}
		result.Removed = append(result.Removed, dir)
	}
	return moved, deleted, nil
}

type agedItem struct {
	path  string
	at    time.Time
	size  int64
	paths Paths
	id    string
	kind  string
}

func collectAged(p Paths, current string) ([]agedItem, []string, error) {
	var items []agedItem
	var skipped []string
	entries, err := os.ReadDir(p.sceneDir())
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || e.Name() == attemptsDir {
			continue
		}
		if e.Name() == current {
			continue
		}
		at, err := parseIDTime(e.Name())
		if err != nil {
			continue
		}
		dir, err := confinedNoLink(p, e.Name())
		if err != nil {
			continue
		}
		if leased(filepath.Join(dir, leaseName)) {
			skipped = append(skipped, dir)
			continue
		}
		items = append(items, agedItem{path: dir, at: at, size: dirSize(dir), paths: p, id: e.Name(), kind: "generation"})
	}
	attRoot, err := confinedAttemptsDir(p)
	if err != nil {
		return items, skipped, nil
	}
	att, err := os.ReadDir(attRoot)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	for _, e := range att {
		at, err := parseIDTime(e.Name())
		if err != nil {
			continue
		}
		dir, err := confinedNoLink(p, attemptsDir, e.Name())
		if err != nil {
			continue
		}
		if leased(filepath.Join(dir, leaseName)) {
			skipped = append(skipped, dir)
			continue
		}
		items = append(items, agedItem{path: dir, at: at, size: dirSize(dir), paths: p, id: e.Name(), kind: "attempt"})
	}
	return items, skipped, nil
}

func selectAged(items []agedItem, opts PruneOptions, total int64) []agedItem {
	sort.Slice(items, func(i, j int) bool {
		if items[i].at.Equal(items[j].at) {
			return items[i].path < items[j].path
		}
		return items[i].at.Before(items[j].at)
	})
	var drop []agedItem
	kept := map[string]bool{}
	if opts.OlderThan > 0 {
		for _, it := range items {
			if opts.Now.Sub(it.at) >= opts.OlderThan {
				drop = append(drop, it)
				kept[it.path] = true
				total -= it.size
			}
		}
	}
	if opts.MaxSize > 0 {
		for _, it := range items {
			if total <= opts.MaxSize {
				break
			}
			if kept[it.path] {
				continue
			}
			drop = append(drop, it)
			kept[it.path] = true
			total -= it.size
		}
	}
	return drop
}

func removeAged(ctx context.Context, it agedItem, opts PruneOptions, result *PruneResult) error {
	lock, err := acquireSceneLock(ctx, it.paths, opts)
	if err != nil {
		return err
	}
	defer closeFile(lock)
	current, err := currentGeneration(it.paths)
	if err != nil {
		return err
	}
	if it.id == current {
		return nil
	}
	path, err := it.reconfine()
	if err != nil {
		return nil
	}
	if leased(filepath.Join(path, leaseName)) {
		result.Skipped = append(result.Skipped, path)
		return nil
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	result.Removed = append(result.Removed, it.path)
	return nil
}

func (it agedItem) reconfine() (string, error) {
	switch it.kind {
	case "attempt":
		return confinedNoLink(it.paths, attemptsDir, it.id)
	default:
		return confinedNoLink(it.paths, it.id)
	}
}

func confinedNoLink(p Paths, parts ...string) (string, error) {
	path, err := scene.ConfinedPath(p.OutDir, p.OutDir, append([]string{takesDir, p.Scene}, parts...)...)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is a symlink", path)
	}
	return path, nil
}

func confinedAttemptsDir(p Paths) (string, error) {
	return confinedNoLink(p, attemptsDir)
}

func confinedAttemptDest(p Paths, id string) (string, error) {
	if _, err := scene.ConfinedPath(p.OutDir, p.OutDir, takesDir, p.Scene, attemptsDir); err != nil {
		return "", err
	}
	if info, err := os.Lstat(p.attemptsDir()); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%s is a symlink", p.attemptsDir())
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return scene.ConfinedPath(p.OutDir, p.OutDir, takesDir, p.Scene, attemptsDir, id)
}

func currentGeneration(p Paths) (string, error) {
	_, err := os.Stat(p.Manifest())
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	m, err := readManifest(p.Manifest())
	if err != nil {
		return "", fmt.Errorf("cannot prune %s: %w", p.Scene, err)
	}
	return m.Generation, nil
}

func idTime(id string) time.Time {
	at, err := parseIDTime(id)
	if err != nil {
		return time.Time{}
	}
	return at
}

func leased(path string) bool {
	if _, err := os.Lstat(path); err != nil {
		return false
	}
	held, err := tryLockExclusive(path)
	if err != nil {
		return true
	}
	closeFile(held)
	return false
}

func dirSize(root string) int64 {
	var n int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		n += info.Size()
		return nil
	})
	return n
}

func repairProjection(p Paths, gen string, opts PruneOptions, result *PruneResult) error {
	if gen == "" {
		return nil
	}
	if !projectionDiverges(p, gen) {
		return nil
	}
	if opts.DryRun {
		result.Repaired = append(result.Repaired, p.StableClip())
		return nil
	}
	if err := projectGeneration(p, gen); err != nil {
		fmt.Fprintf(os.Stderr, "   !! repairing projection for %s: %v\n", p.Scene, err)
		return nil
	}
	result.Repaired = append(result.Repaired, p.StableClip())
	return nil
}
