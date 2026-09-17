package take

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/This-Is-NPC/backstage/internal/scene"
)

const (
	afterImportClip  = "after-import-clip"
	afterImportCopy  = "after-import-copy"
	afterImportLease = "after-import-lease"
)

// Import copies an existing clip and facts into a pending session under
// record.out. The sources may live on another filesystem: the files are
// cloned or copied, never hardlinked and never renamed across devices.
func Import(p *scene.Project, name, clip, facts string) (*Session, error) {
	return ImportContext(context.Background(), p, name, clip, facts, nil)
}

// ImportContext is Import with a cancellable start. bind, if set, receives
// the session as soon as the creating directory exists, so an interrupt
// during the copy can apply Session.Interrupt.
func ImportContext(ctx context.Context, p *scene.Project, name, clip, facts string, bind func(*Session)) (*Session, error) {
	paths, err := ForScene(p, name)
	if err != nil {
		return nil, err
	}
	return importPaths(ctx, paths, clip, facts, time.Now(), bind)
}

func importPaths(ctx context.Context, paths Paths, clip, facts string, now time.Time, bind func(*Session)) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := checkImportSource(clip, "clip"); err != nil {
		return nil, err
	}
	if err := checkImportSource(facts, "facts"); err != nil {
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
		s := &Session{Paths: paths, ID: id, dir: creating}
		if bind != nil {
			bind(s)
		}
		if err := copyInto(clip, filepath.Join(creating, clipName)); err != nil {
			return nil, abandonImport(s, creating, err)
		}
		if err := s.step(afterImportClip); err != nil {
			return nil, abandonImport(s, creating, err)
		}
		if s.Finished() {
			return s, fmt.Errorf("import interrupted")
		}
		if err := copyInto(facts, filepath.Join(creating, factsName)); err != nil {
			return nil, abandonImport(s, creating, err)
		}
		s.mu.Lock()
		s.pairReady = true
		s.mu.Unlock()
		if err := s.step(afterImportCopy); err != nil {
			return nil, abandonImport(s, creating, err)
		}
		if s.Finished() {
			return s, fmt.Errorf("import interrupted")
		}
		if err := ctx.Err(); err != nil {
			return nil, abandonImport(s, creating, err)
		}
		lease, err := os.OpenFile(filepath.Join(creating, leaseName), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, abandonImport(s, creating, err)
		}
		if err := flock(lease, syscall.LOCK_EX); err != nil {
			_ = lease.Close()
			return nil, abandonImport(s, creating, err)
		}
		s.mu.Lock()
		s.lease = lease
		s.mu.Unlock()
		if err := s.step(afterImportLease); err != nil {
			return nil, abandonImport(s, creating, err)
		}
		if s.Finished() {
			return s, fmt.Errorf("import interrupted")
		}
		if err := os.Rename(creating, pending); err != nil {
			if os.IsExist(err) {
				_ = abandonImport(s, creating, err)
				continue
			}
			return nil, abandonImport(s, creating, err)
		}
		s.mu.Lock()
		s.dir = pending
		s.mu.Unlock()
		return s, nil
	}
	return nil, fmt.Errorf("could not create a pending take directory")
}

func checkImportSource(path, kind string) error {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("import %s: missing file", kind)
		}
		return fmt.Errorf("import %s: %w", kind, err)
	}
	if !st.Mode().IsRegular() || st.Size() == 0 {
		return fmt.Errorf("import %s: not a non-empty file", kind)
	}
	return nil
}

func copyInto(src, dst string) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if err := cloneFile(src, out); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

func abandonImport(s *Session, creating string, err error) error {
	if s != nil && s.Finished() {
		return err
	}
	if s != nil {
		s.mu.Lock()
		closeFile(s.lease)
		s.lease = nil
		s.finished = true
		s.mu.Unlock()
	}
	_ = os.RemoveAll(creating)
	return err
}
