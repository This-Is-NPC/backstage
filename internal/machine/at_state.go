package machine

import (
	"context"
	"fmt"
	"sort"
	"syscall"
)

func filePrint(path string) (FilePrint, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return FilePrint{}, err
	}
	return FilePrint{
		Path:    path,
		Inode:   st.Ino,
		Size:    st.Size,
		MtimeNs: st.Mtim.Nano(),
		CtimeNs: st.Ctim.Nano(),
	}, nil
}

func filePrintEqual(a, b FilePrint) bool {
	return a.Path == b.Path && a.Inode == b.Inode && a.Size == b.Size && a.MtimeNs == b.MtimeNs && a.CtimeNs == b.CtimeNs
}

func readAtState(r *Record, snapshot, image string) (*AtState, error) {
	disk, err := filePrint(r.Disk)
	if err != nil {
		return nil, err
	}
	nvram, err := filePrint(r.NVRAM)
	if err != nil {
		return nil, err
	}
	return &AtState{Snapshot: snapshot, Image: image, Disk: disk, NVRAM: nvram}, nil
}

func (a *AtState) match(r *Record) bool {
	if a == nil || r == nil {
		return false
	}
	disk, err := filePrint(r.Disk)
	if err != nil {
		return false
	}
	nvram, err := filePrint(r.NVRAM)
	if err != nil {
		return false
	}
	return filePrintEqual(a.Disk, disk) && filePrintEqual(a.NVRAM, nvram)
}

func (m *Manager) clearAtState(r *Record) error {
	if r == nil || r.AtState == nil {
		return nil
	}
	r.AtState = nil
	return m.Store.Save(r)
}

func (m *Manager) canSkipRestore(ctx context.Context, r *Record, snapshot string) bool {
	if r == nil || r.AtState == nil {
		return false
	}
	if snapshot != r.AtState.Snapshot || r.Snapshots[snapshot] != r.AtState.Image {
		return false
	}
	state, err := m.State(ctx, r)
	if err != nil || state != "shut off" {
		return false
	}
	return r.AtState.match(r)
}

func (m *Manager) atStateChecks(ctx context.Context) []Check {
	list, err := m.Store.List()
	if err != nil {
		return nil
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	var out []Check
	for _, r := range list {
		if r.AtState == nil {
			continue
		}
		status := "fingerprint stale"
		if r.AtState.match(r) {
			status = "fingerprint ok"
		}
		state, err := m.State(ctx, r)
		if err != nil {
			status = "state unknown"
		} else if state != "shut off" {
			status = "domain running; next clean start restores"
		}
		out = append(out, Check{
			Name:   "at-state",
			OK:     true,
			Detail: fmt.Sprintf("%s (%s)", r.AtState.Snapshot, status),
		})
	}
	return out
}
