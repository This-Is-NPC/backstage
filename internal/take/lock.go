package take

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func createLock(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
}

func flock(f *os.File, how int) error {
	return syscall.Flock(int(f.Fd()), how)
}

func lockBusy(err error) bool {
	return errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)
}

// lockExclusive creates path if needed and takes LOCK_EX. A failed non-blocking
// try prints waitNote, then waits until ctx is cancelled or the lock is free.
func lockExclusive(ctx context.Context, path, waitNote string) (*os.File, error) {
	return waitExclusive(ctx, path, waitNote, true)
}

// lockExclusiveExisting flocks an existing lock file without creating it.
// missing is (nil, nil).
func lockExclusiveExisting(ctx context.Context, path, waitNote string) (*os.File, error) {
	return waitExclusive(ctx, path, waitNote, false)
}

func waitExclusive(ctx context.Context, path, waitNote string, create bool) (*os.File, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var f *os.File
	var err error
	if create {
		f, err = createLock(path)
	} else {
		f, err = os.Open(path)
		if os.IsNotExist(err) {
			return nil, nil
		}
	}
	if err != nil {
		return nil, err
	}
	printed := false
	for {
		err := flock(f, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !lockBusy(err) {
			_ = f.Close()
			return nil, err
		}
		if waitNote != "" && !printed {
			fmt.Println(waitNote)
			printed = true
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("waiting for scene lock: %w", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func lockShared(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if err := flock(f, syscall.LOCK_SH); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func tryLockExclusive(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if err := flock(f, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func closeFile(f *os.File) {
	if f != nil {
		_ = f.Close()
	}
}
