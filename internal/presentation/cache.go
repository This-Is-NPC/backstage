package presentation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	cacheKeyVersion     = "v1"
	cacheMemoVersion    = 1
	DefaultCacheMaxSize = 10 << 30
)

// renderCacheRoot, when set, replaces UserCacheDir()/backstage/render. Tests only.
var renderCacheRoot string

// contentHashReads counts bytes hashed from disk, not memo hits. Tests only.
var contentHashReads atomic.Int64

// publishCacheFile installs a complete temp file as a cache entry.
var publishCacheFile = publishCacheFileAtomic

// linkCacheFile installs a cache entry into the work dir. Tests replace it.
var linkCacheFile = linkOrCopy

// cacheFillExclusive makes lockForFill take LOCK_EX even when dest is valid. Tests only.
var cacheFillExclusive bool

type cacheCounts struct {
	Tracks   int `json:"tracks"`
	Audio    int `json:"audio"`
	Segments int `json:"segments"`
}

type cacheKind struct {
	name, ext string
}

var cacheKinds = []cacheKind{
	{"tracks", ".mkv"},
	{"audio", ".wav"},
	{"segments", ".mp4"},
}

type renderCache struct {
	root     string
	ffmpeg   string
	progress io.Writer
	mu       sync.Mutex
	held     []*os.File
	hits     cacheCounts
	misses   cacheCounts
}

type trackKeyV1 struct {
	V        string       `json:"v"`
	Kind     string       `json:"kind"`
	Src      string       `json:"src"`
	Segments []keySegment `json:"segments"`
	FPS      int          `json:"fps"`
	FFmpeg   string       `json:"ffmpeg"`
	Encode   []string     `json:"encode"`
}

type keySegment struct {
	From string `json:"from"`
	To   string `json:"to"`
	Rate string `json:"rate"`
}

type audioKeyV1 struct {
	V        string         `json:"v"`
	Kind     string         `json:"kind"`
	Parts    []audioKeyPart `json:"parts"`
	Duration string         `json:"duration"`
	FFmpeg   string         `json:"ffmpeg"`
	Encode   []string       `json:"encode"`
}

type audioKeyPart struct {
	ID       string `json:"id"`
	Src      string `json:"src"`
	At       string `json:"at"`
	From     string `json:"from"`
	To       string `json:"to"`
	Rate     string `json:"rate"`
	Duration string `json:"duration"`
	Volume   string `json:"volume"`
	FadeIn   string `json:"fade-in"`
	FadeOut  string `json:"fade-out"`
	Loop     bool   `json:"loop"`
}

type memoFile struct {
	Version int               `json:"version"`
	Entries map[string]string `json:"entries"`
}

type entryMeta struct {
	LastUsed int64              `json:"last-used"`
	Size     int64              `json:"size"`
	Files    *map[string]string `json:"files,omitempty"`
}

func DefaultRenderCacheRoot() (string, error) {
	if renderCacheRoot != "" {
		return renderCacheRoot, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "backstage", "render"), nil
}

func openRenderCache(ctx context.Context, progress io.Writer) (*renderCache, error) {
	root, err := DefaultRenderCacheRoot()
	if err != nil {
		return nil, err
	}
	dirs := []string{root}
	for _, k := range cacheKinds {
		dirs = append(dirs, filepath.Join(root, k.name))
	}
	for _, dir := range dirs {
		if err = os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	ver, err := toolVersion(ctx, "ffmpeg", "-version")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg version: %w", err)
	}
	if progress == nil {
		progress = io.Discard
	}
	return &renderCache{root: root, ffmpeg: ver, progress: progress}, nil
}

func (c *renderCache) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var err error
	for _, f := range c.held {
		err = errors.Join(err, f.Close())
	}
	c.held = nil
	return err
}

func toolVersion(ctx context.Context, name, flag string) (string, error) {
	b, err := command(ctx, name, flag).Output()
	if err != nil {
		return "", err
	}
	return strings.SplitN(string(b), "\n", 2)[0], nil
}

func hashJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func trackEncodeArgs(gop int) []string {
	args := []string{"-c:v", "ffv1"}
	if gop > 0 {
		args = append(args, "-g", fmt.Sprintf("%d", gop))
	}
	return args
}

func trackCacheKey(srcHash string, segs []Segment, fps, gop int, ffmpeg string) string {
	keys := make([]keySegment, 0, len(segs))
	for _, s := range segs {
		keys = append(keys, keySegment{From: num(s.From), To: num(s.To), Rate: num(s.Rate)})
	}
	return hashJSON(trackKeyV1{
		V: cacheKeyVersion, Kind: "track", Src: srcHash,
		Segments: keys, FPS: fps, FFmpeg: ffmpeg, Encode: trackEncodeArgs(gop),
	})
}

func audioEncodeArgs() []string {
	return []string{"-ar", "48000", "-ac", "2", "-c:a", "pcm_s16le", "amix=normalize=0", "alimiter=limit=0.95:level=0:latency=1"}
}

func audioCacheKey(parts []audioKeyPart, duration, ffmpeg string) string {
	return hashJSON(audioKeyV1{
		V: cacheKeyVersion, Kind: "audio", Parts: parts,
		Duration: duration, FFmpeg: ffmpeg, Encode: audioEncodeArgs(),
	})
}

func fingerprint(path string, fi os.FileInfo) (string, error) {
	abs, err := resolvedPath(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\x1f%d\x1f%d\x1f%d", abs, fi.Size(), fi.ModTime().UnixNano(), fileInode(fi)), nil
}

func resolvedPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real, nil
	}
	return abs, nil
}

func fileInode(fi os.FileInfo) uint64 {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return st.Ino
}

func (c *renderCache) warn(msg string) {
	fmt.Fprintf(c.progress, ">> cache: %s\n", msg)
}

func (c *renderCache) hashFile(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	fp, err := fingerprint(path, fi)
	if err != nil {
		return "", err
	}
	if sum, ok, err := c.memoGet(fp); err != nil {
		return "", err
	} else if ok {
		return sum, nil
	}
	sum, err := hashContents(path)
	if err != nil {
		return "", err
	}
	if err = c.memoPut(fp, sum); err != nil {
		return "", err
	}
	return sum, nil
}

func hashContents(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	contentHashReads.Add(1)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (c *renderCache) memoLock() (*os.File, error) {
	path := filepath.Join(c.root, "memo.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func (c *renderCache) loadMemo() memoFile {
	b, err := os.ReadFile(filepath.Join(c.root, "memo.json"))
	if err != nil {
		if !os.IsNotExist(err) {
			c.warn("memo unreadable; starting empty")
		}
		return memoFile{Version: cacheMemoVersion, Entries: map[string]string{}}
	}
	var m memoFile
	if json.Unmarshal(b, &m) != nil || m.Version != cacheMemoVersion || m.Entries == nil {
		c.warn("memo unreadable; starting empty")
		return memoFile{Version: cacheMemoVersion, Entries: map[string]string{}}
	}
	return m
}

func (c *renderCache) saveMemo(m memoFile) error {
	path := filepath.Join(c.root, "memo.json")
	tmp := path + ".tmp"
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err = os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (c *renderCache) memoGet(fp string) (string, bool, error) {
	lk, err := c.memoLock()
	if err != nil {
		return "", false, err
	}
	defer lk.Close()
	m := c.loadMemo()
	sum, ok := m.Entries[fp]
	return sum, ok, nil
}

func (c *renderCache) memoPut(fp, sum string) error {
	lk, err := c.memoLock()
	if err != nil {
		return err
	}
	defer lk.Close()
	m := c.loadMemo()
	m.Entries[fp] = sum
	return c.saveMemo(m)
}

func entryMetaPath(dest string) string {
	return strings.TrimSuffix(dest, filepath.Ext(dest)) + ".meta.json"
}

func readEntryMeta(dest string) (m entryMeta, size int64, ok bool) {
	fi, err := os.Stat(dest)
	if err != nil || fi.Size() == 0 {
		return entryMeta{}, 0, false
	}
	size = fi.Size()
	b, err := os.ReadFile(entryMetaPath(dest))
	if err != nil {
		return entryMeta{}, size, false
	}
	if json.Unmarshal(b, &m) != nil {
		return entryMeta{}, size, false
	}
	return m, size, true
}

func cacheEntryValid(dest string) bool {
	m, size, ok := readEntryMeta(dest)
	if size == 0 {
		return false
	}
	if !ok || m.Size <= 0 {
		return true
	}
	return m.Size == size
}

func lockShared(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func lockForFill(ctx context.Context, lockPath, dest string, valid func(string) bool) (*os.File, bool, error) {
	if valid == nil {
		valid = cacheEntryValid
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	for {
		if !cacheFillExclusive && valid(dest) {
			if err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err == nil {
				return f, true, nil
			}
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, false, nil
		}
		if !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, false, err
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, false, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (c *renderCache) holdShared(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		_ = f.Close()
		return err
	}
	c.mu.Lock()
	c.held = append(c.held, f)
	c.mu.Unlock()
	return nil
}

func (c *renderCache) releaseLock(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.held[:0]
	for _, f := range c.held {
		if f.Name() == path {
			_ = f.Close()
			continue
		}
		kept = append(kept, f)
	}
	c.held = kept
}

func copyRegular(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return copyErr
	}
	if syncErr != nil {
		_ = os.Remove(dest)
		return syncErr
	}
	if closeErr != nil {
		_ = os.Remove(dest)
	}
	return closeErr
}

func publishCacheFileAtomic(tmp, dest string) error {
	f, err := os.Open(tmp)
	if err != nil {
		return err
	}
	err = f.Sync()
	_ = f.Close()
	if err != nil {
		return err
	}
	if err = os.Chmod(tmp, 0o444); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

func linkOrCopy(src, dest string) error {
	if err := os.Link(src, dest); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o444)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return copyErr
	}
	if syncErr != nil {
		_ = os.Remove(dest)
		return syncErr
	}
	return closeErr
}

func writeMeta(dir, key string, size int64, files *map[string]string) error {
	path := filepath.Join(dir, key+".meta.json")
	m := entryMeta{LastUsed: time.Now().UnixNano(), Size: size}
	if files != nil {
		cp := map[string]string{}
		if *files != nil {
			for rel, sum := range *files {
				cp[rel] = sum
			}
		}
		m.Files = &cp
	} else if b, err := os.ReadFile(path); err == nil {
		var old entryMeta
		if json.Unmarshal(b, &old) == nil {
			m.Files = old.Files
		}
	}
	tmp := filepath.Join(dir, ".tmp-"+key+"-"+randomHex(8)+".meta.json")
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err = os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (c *renderCache) note(hit bool, kind string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	counts := &c.misses
	if hit {
		counts = &c.hits
	}
	if kind == "audio" {
		counts.Audio++
		return
	}
	if kind == "segments" {
		counts.Segments++
		return
	}
	counts.Tracks++
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (c *renderCache) computeWithoutCache(label, workPath, src string, cause error, fill func(string) (map[string]string, error)) error {
	c.warn(label + ": " + cause.Error() + "; recomputing without cache")
	if src != "" {
		if fi, err := os.Stat(src); err == nil && fi.Size() > 0 {
			_ = os.Remove(workPath)
			if err := linkOrCopy(src, workPath); err != nil {
				return err
			}
			if src != workPath && strings.HasPrefix(filepath.Base(src), ".tmp-") {
				_ = os.Remove(src)
			}
			return nil
		}
	}
	_, err := fill(workPath)
	return err
}

func (c *renderCache) getOrFill(ctx context.Context, kind, key, destExt, workPath, label string, valid func(string) bool, fill func(tmp string) (map[string]string, error)) (hit bool, err error) {
	if valid == nil {
		valid = cacheEntryValid
	}
	dir := filepath.Join(c.root, kind)
	dest := filepath.Join(dir, key+destExt)
	lockPath := filepath.Join(dir, key+".lock")
	use := func(lock *os.File, alreadyShared bool, files *map[string]string) error {
		_ = os.Remove(workPath)
		if err := linkCacheFile(dest, workPath); err != nil {
			return err
		}
		if fi, err := os.Stat(dest); err == nil {
			_ = writeMeta(dir, key, fi.Size(), files)
		}
		if alreadyShared {
			c.mu.Lock()
			c.held = append(c.held, lock)
			c.mu.Unlock()
			return nil
		}
		return c.holdShared(lock)
	}
	if valid(dest) {
		lock, lockErr := lockShared(lockPath)
		if lockErr == nil {
			if valid(dest) {
				if useErr := use(lock, true, nil); useErr == nil {
					return true, nil
				} else {
					lockErr = useErr
				}
			} else {
				lockErr = errors.New("cache entry disappeared")
			}
			_ = lock.Close()
		}
		if fill == nil {
			return false, nil
		}
		c.warn(label + ": " + lockErr.Error() + "; recomputing")
	}
	if fill == nil {
		return false, nil
	}
	c.releaseLock(lockPath)
	lock, shared, err := lockForFill(ctx, lockPath, dest, valid)
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return false, err
		}
		return false, c.computeWithoutCache(label, workPath, "", err, fill)
	}
	held := false
	defer func() {
		if !held {
			_ = lock.Close()
		}
	}()
	if valid(dest) {
		if err = use(lock, shared, nil); err == nil {
			held = true
			return true, nil
		}
		return false, c.computeWithoutCache(label, workPath, dest, err, fill)
	}
	if shared {
		return false, c.computeWithoutCache(label, workPath, "", errors.New("cache entry invalid"), fill)
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		_ = os.Remove(dest)
	}
	tmp := filepath.Join(dir, ".tmp-"+key+"-"+randomHex(8)+destExt)
	files, err := fill(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	var metaFiles *map[string]string
	if files != nil {
		metaFiles = &files
	}
	if err = publishCacheFile(tmp, dest); err != nil {
		return false, c.computeWithoutCache(label, workPath, tmp, err, fill)
	}
	if err = use(lock, false, metaFiles); err != nil {
		return false, c.computeWithoutCache(label, workPath, dest, err, fill)
	}
	held = true
	return false, nil
}

func (c *renderCache) prepareTrack(ctx context.Context, t CompiledTrack, work, id string, fps, threads, filterThreads, gop int) (string, []string, error) {
	src, err := c.hashFile(t.Media.Path)
	if err != nil {
		return "", nil, err
	}
	key := trackCacheKey(src, t.Segments, fps, gop, c.ffmpeg)
	workPath := filepath.Join(work, id+".mkv")
	hit, err := c.getOrFill(ctx, "tracks", key, ".mkv", workPath, id, nil, func(tmp string) (map[string]string, error) {
		_, _, err := prepareOne(ctx, t, filepath.Dir(tmp), strings.TrimSuffix(filepath.Base(tmp), ".mkv"), fps, threads, filterThreads, gop)
		return nil, err
	})
	if err != nil {
		return "", nil, err
	}
	c.note(hit, "tracks")
	args := []string{"-v", "error", "-y", "-threads", fmt.Sprintf("%d", threads), "-i", t.Media.Path, "-filter_complex_threads", fmt.Sprintf("%d", filterThreads), "-c:v", "ffv1"}
	if gop > 0 {
		args = append(args, "-g", fmt.Sprintf("%d", gop))
	}
	return workPath, args, nil
}

func (c *renderCache) mix(ctx context.Context, p *Plan, dir string) (string, []namedSeconds, error) {
	parts := make([]audioKeyPart, 0, len(p.AudioParts))
	for _, a := range p.AudioParts {
		src, err := c.hashFile(a.Path)
		if err != nil {
			return "", nil, err
		}
		parts = append(parts, audioKeyPart{
			ID: a.ID, Src: src, At: num(a.At), From: num(a.From), To: num(a.To),
			Rate: num(a.Rate), Duration: num(a.Duration), Volume: num(a.Volume),
			FadeIn: num(a.FadeIn), FadeOut: num(a.FadeOut), Loop: a.Loop,
		})
	}
	key := audioCacheKey(parts, num(p.Document.Duration), c.ffmpeg)
	workPath := filepath.Join(dir, "mix.wav")
	var partSecs []namedSeconds
	hit, err := c.getOrFill(ctx, "audio", key, ".wav", workPath, "mix", nil, func(tmp string) (map[string]string, error) {
		path, secs, err := p.mixDirect(ctx, dir)
		if err != nil {
			return nil, err
		}
		partSecs = secs
		if filepath.Clean(path) == filepath.Clean(tmp) {
			return nil, nil
		}
		if err = copyRegular(path, tmp, 0o600); err != nil {
			return nil, err
		}
		return nil, os.Remove(path)
	})
	if err != nil {
		return "", nil, err
	}
	c.note(hit, "audio")
	if hit {
		return workPath, nil, nil
	}
	return workPath, partSecs, nil
}
