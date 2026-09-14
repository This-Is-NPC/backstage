package presentation

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// CachePruneOptions controls backstage cache prune.
type CachePruneOptions struct {
	MaxSize int64
	DryRun  bool
	Now     time.Time
}

// CachePruneReport is the prune result, including JSON output.
type CachePruneReport struct {
	MaxSize     int64    `json:"max-size"`
	BytesBefore int64    `json:"bytes-before"`
	BytesAfter  int64    `json:"bytes-after"`
	MemoPruned  int      `json:"memo-pruned"`
	DryRun      bool     `json:"dry-run"`
	Removed     []string `json:"removed"`
	Orphans     []string `json:"orphans"`
	Skipped     []string `json:"skipped"`
}

type pruneItem struct {
	kind     string
	key      string
	data     string
	meta     string
	lock     string
	rel      string
	size     int64
	lastUsed int64
}

// PruneRenderCache removes least-recently-used entries until the cache is
// under MaxSize. It never unlinks *.lock or memo.lock.
func PruneRenderCache(ctx context.Context, root string, opts CachePruneOptions) (CachePruneReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.MaxSize <= 0 {
		opts.MaxSize = DefaultCacheMaxSize
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	rep := CachePruneReport{DryRun: opts.DryRun, MaxSize: opts.MaxSize}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return rep, err
	}
	for _, kind := range []string{"tracks", "audio"} {
		if err := os.MkdirAll(filepath.Join(root, kind), 0o700); err != nil {
			return rep, err
		}
		orphans, err := pruneOrphans(root, kind, opts.DryRun)
		if err != nil {
			return rep, err
		}
		rep.Orphans = append(rep.Orphans, orphans...)
	}
	items, before, err := listCacheEntries(root)
	if err != nil {
		return rep, err
	}
	rep.BytesBefore = before
	sortPruneItems(items)
	total := before
	for _, it := range items {
		if err = ctx.Err(); err != nil {
			return rep, err
		}
		if total <= opts.MaxSize {
			break
		}
		lk, err := tryLockExclusive(it.lock)
		if err != nil {
			rep.Skipped = append(rep.Skipped, it.rel)
			continue
		}
		if opts.DryRun {
			_ = lk.Close()
			rep.Removed = append(rep.Removed, it.rel)
			total -= it.size
			continue
		}
		if err = os.Remove(it.data); err != nil && !os.IsNotExist(err) {
			_ = lk.Close()
			return rep, err
		}
		if err = os.Remove(it.meta); err != nil && !os.IsNotExist(err) {
			_ = lk.Close()
			return rep, err
		}
		_ = lk.Close()
		rep.Removed = append(rep.Removed, it.rel)
		total -= it.size
	}
	if total < 0 {
		total = 0
	}
	rep.BytesAfter = total
	n, err := pruneMemo(root, opts.DryRun)
	if err != nil {
		return rep, err
	}
	rep.MemoPruned = n
	return rep, nil
}

func tryLockExclusive(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func pruneOrphans(root, kind string, dry bool) ([]string, error) {
	dir := filepath.Join(root, kind)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), ".tmp-") {
			continue
		}
		rel := filepath.Join(kind, e.Name())
		if key, ok := tmpNameKey(e.Name()); ok {
			lk, err := tryLockExclusive(filepath.Join(dir, key+".lock"))
			if err != nil {
				continue
			}
			if !dry {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
			_ = lk.Close()
			out = append(out, rel)
			continue
		}
		if !dry {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
		out = append(out, rel)
	}
	return out, nil
}

func tmpNameKey(name string) (string, bool) {
	base := strings.TrimPrefix(name, ".tmp-")
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if i := strings.LastIndex(base, "-"); i > 0 {
		key := base[:i]
		if len(key) == 64 {
			return key, true
		}
	}
	return "", false
}

func listCacheEntries(root string) ([]pruneItem, int64, error) {
	var items []pruneItem
	var total int64
	for _, kind := range []string{"tracks", "audio"} {
		dir := filepath.Join(root, kind)
		ents, err := os.ReadDir(dir)
		if err != nil {
			return nil, 0, err
		}
		ext := ".mkv"
		if kind == "audio" {
			ext = ".wav"
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ext) || strings.HasPrefix(e.Name(), ".tmp-") {
				continue
			}
			key := strings.TrimSuffix(e.Name(), ext)
			it := pruneItem{
				kind: kind, key: key,
				data: filepath.Join(dir, e.Name()),
				meta: filepath.Join(dir, key+".meta.json"),
				lock: filepath.Join(dir, key+".lock"),
				rel:  filepath.Join(kind, e.Name()),
			}
			fi, err := os.Stat(it.data)
			if err != nil {
				continue
			}
			it.size = fi.Size()
			it.lastUsed = 0
			if b, err := os.ReadFile(it.meta); err == nil {
				var m entryMeta
				if json.Unmarshal(b, &m) == nil && m.LastUsed > 0 {
					it.lastUsed = m.LastUsed
				}
			}
			total += it.size
			items = append(items, it)
		}
	}
	return items, total, nil
}

func sortPruneItems(items []pruneItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].lastUsed != items[j].lastUsed {
			return items[i].lastUsed < items[j].lastUsed
		}
		return items[i].rel < items[j].rel
	})
}

func pruneMemo(root string, dry bool) (int, error) {
	c := &renderCache{root: root, progress: io.Discard}
	lk, err := c.memoLock()
	if err != nil {
		return 0, err
	}
	defer lk.Close()
	m := c.loadMemo()
	n := 0
	for fp := range m.Entries {
		if memoStale(fp) {
			delete(m.Entries, fp)
			n++
		}
	}
	if n == 0 || dry {
		return n, nil
	}
	return n, c.saveMemo(m)
}

func memoStale(fp string) bool {
	parts := strings.Split(fp, "\x1f")
	if len(parts) != 4 {
		return true
	}
	fi, err := os.Stat(parts[0])
	if err != nil {
		return true
	}
	cur, err := fingerprint(parts[0], fi)
	if err != nil {
		return true
	}
	return cur != fp
}
