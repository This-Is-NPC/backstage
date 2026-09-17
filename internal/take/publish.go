package take

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

const (
	beforeRenameGeneration    = "before-rename-generation"
	afterRenameBeforeManifest = "after-rename-before-manifest"
	afterRenameGeneration     = "after-rename-generation"
	beforeManifest            = "before-manifest"
	afterManifest             = "after-manifest"
	beforeProjectClip         = "before-project-clip"
	afterProjectClip          = "after-project-clip"
	beforeProjectFacts        = "before-project-facts"
	afterProjectFacts         = "after-project-facts"
)

// Session is an in-progress recording under .pending-<id>.
type Session struct {
	Paths Paths
	ID    string

	mu        sync.Mutex
	dir       string
	lease     *os.File
	finished  bool
	committed bool
	// pairReady is true when Interrupt may keep the directory as an attempt.
	// Play sessions start ready. Import sets it after both copies finish, so
	// a Ctrl-C mid-copy discards a truncated .creating- directory.
	pairReady bool
	temps     []string
	hook      func(string) error
}

// Published is a committed generation and its stable projection paths.
type Published struct {
	Generation  string
	Clip        string
	Facts       string
	StableClip  string
	StableFacts string
}

// Begin creates a pending directory and holds an exclusive flock on its lease.
// The directory is created as .creating-<id>, leased, then renamed to
// .pending-<id> so play does not wait for the scene lock.
func Begin(p *scene.Project, name string) (*Session, error) {
	return BeginContext(context.Background(), p, name)
}

// BeginContext is Begin with a cancellable start.
func BeginContext(ctx context.Context, p *scene.Project, name string) (*Session, error) {
	paths, err := ForScene(p, name)
	if err != nil {
		return nil, err
	}
	return beginPaths(ctx, paths, time.Now())
}

func beginPaths(ctx context.Context, paths Paths, now time.Time) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for i := 0; i < 8; i++ {
		id := newID(now.Add(time.Duration(i)))
		creating := paths.creatingDir(id)
		pending := paths.pendingDir(id)
		if err := os.MkdirAll(filepath.Dir(creating), 0o700); err != nil {
			return nil, err
		}
		if err := os.Mkdir(creating, 0o700); err != nil {
			if os.IsExist(err) {
				continue
			}
			return nil, err
		}
		lease, err := os.OpenFile(filepath.Join(creating, leaseName), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			_ = os.RemoveAll(creating)
			return nil, err
		}
		if err := flock(lease, syscall.LOCK_EX); err != nil {
			_ = lease.Close()
			_ = os.RemoveAll(creating)
			return nil, err
		}
		if err := os.Rename(creating, pending); err != nil {
			_ = lease.Close()
			_ = os.RemoveAll(creating)
			if os.IsExist(err) {
				continue
			}
			return nil, err
		}
		return &Session{Paths: paths, ID: id, dir: pending, lease: lease, pairReady: true}, nil
	}
	return nil, fmt.Errorf("could not create a pending take directory")
}

// Clip is the recorder path inside the pending (or later generation/attempt) dir.
func (s *Session) Clip() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return filepath.Join(s.dir, clipName)
}

// FactsFile is the sidecar path next to Clip.
func (s *Session) FactsFile() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return filepath.Join(s.dir, factsName)
}

// Finished reports that the session published or moved to attempts.
func (s *Session) Finished() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finished
}

// HasClip reports a non-empty recorded file.
func (s *Session) HasClip() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hasClipLocked()
}

func (s *Session) hasClipLocked() bool {
	st, err := os.Stat(filepath.Join(s.dir, clipName))
	return err == nil && st.Size() > 0
}

// HasFacts reports that the sidecar was written.
func (s *Session) HasFacts() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := os.Stat(filepath.Join(s.dir, factsName))
	return err == nil && st.Size() > 0
}

// Discard removes an empty pending directory. The exclusive lease is released.
func (s *Session) Discard() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.discardLocked()
}

func (s *Session) discardLocked() error {
	if s.finished {
		return nil
	}
	s.removeTempsLocked()
	closeFile(s.lease)
	s.lease = nil
	s.finished = true
	return os.RemoveAll(s.dir)
}

// KeepAttempt moves the pending or orphan generation to attempts/ and
// releases the writer lease. It returns the attempt clip path.
func (s *Session) KeepAttempt() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keepAttemptLocked()
}

func (s *Session) keepAttemptLocked() (string, error) {
	if s.finished {
		return filepath.Join(s.dir, clipName), nil
	}
	s.removeTempsLocked()
	if err := os.MkdirAll(s.Paths.attemptsDir(), 0o700); err != nil {
		return "", err
	}
	dest := s.Paths.attemptDir(s.ID)
	if err := os.Rename(s.dir, dest); err != nil {
		return "", err
	}
	s.dir = dest
	closeFile(s.lease)
	s.lease = nil
	s.finished = true
	return filepath.Join(s.dir, clipName), nil
}

// Interrupt keeps a complete take as an attempt if the manifest is not yet
// committed, and always removes leftover projection or manifest temps.
// A truncated import still inside .creating- is discarded.
func (s *Session) Interrupt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeTempsLocked()
	if s.committed || s.finished {
		return
	}
	if s.pairReady && s.hasClipLocked() {
		_, _ = s.keepAttemptLocked()
		return
	}
	_ = s.discardLocked()
}

func (s *Session) trackTemp(path string) {
	s.mu.Lock()
	s.temps = append(s.temps, path)
	s.mu.Unlock()
}

func (s *Session) removeTempsLocked() {
	for _, path := range s.temps {
		_ = os.Remove(path)
	}
	s.temps = nil
}

// Publish commits a successful take: rename to a generation, replace the
// manifest, then project. A failure before the manifest commit moves the
// take to attempts.
func (s *Session) Publish(ctx context.Context) (Published, error) {
	pub, err := s.commit(ctx)
	s.mu.Lock()
	finished, committed := s.finished, s.committed
	s.mu.Unlock()
	if err != nil && !finished && !committed {
		path, moveErr := s.KeepAttempt()
		if moveErr != nil {
			return Published{}, fmt.Errorf("%v; also keeping attempt: %w", err, moveErr)
		}
		fmt.Fprintf(os.Stderr, "   !! publishing take: %v\n", err)
		fmt.Printf(">> unpublished take kept at %s\n", path)
		return Published{}, fmt.Errorf("publishing take: %w (kept at %s)", err, path)
	}
	return pub, err
}

func (s *Session) commit(ctx context.Context) (Published, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.step(beforeRenameGeneration); err != nil {
		return Published{}, err
	}
	lock, err := lockExclusive(ctx, s.Paths.lockPath(), ">> waiting for scene lock")
	if err != nil {
		return Published{}, err
	}
	defer closeFile(lock)

	pub, err := s.commitGeneration()
	if err != nil {
		return Published{}, err
	}
	_ = s.step(afterRenameGeneration)
	_ = s.step(beforeManifest)
	_ = s.step(afterManifest)

	if err := s.projectStable(); err != nil {
		fmt.Fprintf(os.Stderr, "   !! projecting take: %v\n", err)
	}

	s.mu.Lock()
	closeFile(s.lease)
	s.lease = nil
	s.finished = true
	s.temps = nil
	s.mu.Unlock()
	return pub, nil
}

// commitGeneration renames the pending directory and writes the manifest
// under the Session mutex so Interrupt cannot observe a torn publish.
func (s *Session) commitGeneration() (Published, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return Published{}, fmt.Errorf("take session already finished")
	}
	dir := s.dir
	id := s.ID
	gen := s.Paths.generationDir(id)
	if err := os.Rename(dir, gen); err != nil {
		return Published{}, err
	}
	s.dir = gen
	if err := s.step(afterRenameBeforeManifest); err != nil {
		return Published{}, err
	}
	man := Manifest{
		Version:    manifestVer,
		Generation: id,
		Clip:       clipName,
		Facts:      factsName,
	}
	if err := writeJSONTracked(s.Paths.Manifest(), man, func(path string) {
		s.temps = append(s.temps, path)
	}); err != nil {
		return Published{}, err
	}
	s.committed = true
	return Published{
		Generation:  id,
		Clip:        filepath.Join(gen, clipName),
		Facts:       filepath.Join(gen, factsName),
		StableClip:  s.Paths.StableClip(),
		StableFacts: s.Paths.StableFacts(),
	}, nil
}

func (s *Session) projectStable() error {
	s.mu.Lock()
	srcClip := filepath.Join(s.dir, clipName)
	srcFacts := filepath.Join(s.dir, factsName)
	s.mu.Unlock()
	_ = s.step(beforeProjectClip)
	if err := projectFileTracked(srcClip, s.Paths.StableClip(), s.trackTemp); err != nil {
		return err
	}
	_ = s.step(afterProjectClip)
	_ = s.step(beforeProjectFacts)
	if err := projectFileTracked(srcFacts, s.Paths.StableFacts(), s.trackTemp); err != nil {
		return err
	}
	_ = s.step(afterProjectFacts)
	return nil
}

func (s *Session) step(name string) error {
	if s.hook == nil {
		return nil
	}
	return s.hook(name)
}
