package take

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// linux FICLONE: copy-on-write clone when the filesystem supports it.
const fiClone = 0x40049409

func projectFile(src, dst string) error {
	return projectFileTracked(src, dst, nil)
}

func projectFileTracked(src, dst string, track func(string)) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if track != nil {
		track(tmpName)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := cloneOrCopy(src, tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// cloneFile copies src into dst with FICLONE or a full copy. Tests replace it
// to force the copy path and to prove import never hardlinks or renames.
var cloneFile = cloneOrCopy

func cloneOrCopy(src string, dst *os.File) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, dst.Fd(), uintptr(fiClone), in.Fd())
	if errno == 0 {
		return nil
	}
	if _, err := dst.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := dst.Truncate(0); err != nil {
		return err
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err = io.Copy(dst, in)
	return err
}

func filesMatch(a, b string) error {
	sa, err := os.Stat(a)
	if err != nil {
		return err
	}
	sb, err := os.Stat(b)
	if err != nil {
		return err
	}
	if sa.Size() != sb.Size() {
		return fmt.Errorf("%s does not match %s", b, a)
	}
	fa, err := os.Open(a)
	if err != nil {
		return err
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return err
	}
	defer fb.Close()
	remain := sa.Size()
	bufa := make([]byte, 64*1024)
	bufb := make([]byte, 64*1024)
	for remain > 0 {
		n := int(remain)
		if n > len(bufa) {
			n = len(bufa)
		}
		if _, err := io.ReadFull(fa, bufa[:n]); err != nil {
			return err
		}
		if _, err := io.ReadFull(fb, bufb[:n]); err != nil {
			return err
		}
		if !bytes.Equal(bufa[:n], bufb[:n]) {
			return fmt.Errorf("%s does not match %s", b, a)
		}
		remain -= int64(n)
	}
	return nil
}

func projectGeneration(p Paths, gen string) error {
	dir := p.generationDir(gen)
	if err := projectFile(filepath.Join(dir, clipName), p.StableClip()); err != nil {
		return err
	}
	return projectFile(filepath.Join(dir, factsName), p.StableFacts())
}

func projectionDiverges(p Paths, gen string) bool {
	dir := p.generationDir(gen)
	return filesMatch(filepath.Join(dir, clipName), p.StableClip()) != nil ||
		filesMatch(filepath.Join(dir, factsName), p.StableFacts()) != nil
}
