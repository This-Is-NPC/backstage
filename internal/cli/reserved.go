package cli

import (
	"fmt"
	"os"
	"syscall"

	"github.com/This-Is-NPC/backstage/internal/machine"
)

// heldInternalFiles keep inherited lock and progress fds alive for the
// child process. Closing them would drop the flock the parent handed over.
var heldInternalFiles []*os.File

func verifyReservedStage(store *machine.Store, name string, fd int) error {
	if name == "" {
		return fmt.Errorf("reserved stage: missing name")
	}
	if fd < 0 {
		return fmt.Errorf("reserved stage %s: missing lock fd", name)
	}
	if err := machine.ValidateName(name); err != nil {
		return err
	}
	if store == nil {
		var err error
		store, err = machine.DefaultStore()
		if err != nil {
			return err
		}
	}
	if err := store.Init(); err != nil {
		return err
	}
	path := store.LockPath(name)
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("reserved stage %s: %w", name, err)
	}
	sys, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("reserved stage %s: no inode for %s", name, path)
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return fmt.Errorf("reserved stage %s: fstat: %w", name, err)
	}
	if st.Dev != sys.Dev || st.Ino != sys.Ino {
		return fmt.Errorf("reserved stage %s: fd is not %s", name, path)
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("reserved stage %s: lock not held: %w", name, err)
	}
	return nil
}

func holdInternalFD(fd int, name string) *os.File {
	if fd < 0 {
		return nil
	}
	syscall.CloseOnExec(fd)
	f := os.NewFile(uintptr(fd), name)
	heldInternalFiles = append(heldInternalFiles, f)
	return f
}
