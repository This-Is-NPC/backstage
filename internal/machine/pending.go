package machine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// pendingCapture is a capture in progress. Collect treats it as used.
type pendingCapture struct {
	ID      string    `json:"id"`
	Stage   string    `json:"stage"`
	Parent  string    `json:"parent,omitempty"`
	Disk    string    `json:"disk"`
	NVRAM   string    `json:"nvram"`
	Keys    string    `json:"keys"`
	Created time.Time `json:"created"`
}

type pendingScan struct {
	Found  []pendingCapture
	Unread []string
}

func (s *Store) pendingDir() string {
	return filepath.Join(s.Root, "pending")
}

func (s *Store) pendingJSON(id string) string {
	return filepath.Join(s.pendingDir(), id+".json")
}

func (m *Manager) writePending(p pendingCapture) error {
	if err := os.MkdirAll(m.Store.pendingDir(), 0o700); err != nil {
		return err
	}
	return atomicJSON(m.Store.pendingJSON(p.ID), p)
}

func (m *Manager) removePending(id string) error {
	if id == "" {
		return nil
	}
	if err := os.Remove(m.Store.pendingJSON(id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *Manager) scanPending() (pendingScan, error) {
	files, err := filepath.Glob(filepath.Join(m.Store.pendingDir(), "*.json"))
	if err != nil {
		return pendingScan{}, err
	}
	var scan pendingScan
	for _, path := range files {
		var p pendingCapture
		if err := readJSON(path, &p); err != nil || p.ID == "" {
			scan.Unread = append(scan.Unread, filepath.Base(path))
			continue
		}
		scan.Found = append(scan.Found, p)
	}
	return scan, nil
}

func (m *Manager) listPending() ([]pendingCapture, error) {
	scan, err := m.scanPending()
	if err != nil {
		return nil, err
	}
	if len(scan.Unread) > 0 {
		return nil, fmt.Errorf("pending %s: unreadable", scan.Unread[0])
	}
	return scan.Found, nil
}

func (m *Manager) pendingProtects(p pendingCapture) []string {
	return []string{p.Disk, p.NVRAM, p.Keys, m.Store.imageJSON(p.ID), m.Store.pendingJSON(p.ID)}
}

func (m *Manager) pendingManagedPaths(p pendingCapture) error {
	name := p.ID + ".json"
	if !validImageID(p.ID) {
		return fmt.Errorf("pending %s: refusing to collect paths outside managed storage", name)
	}
	if p.Disk != m.diskPath(p.ID, "-image.qcow2") || p.NVRAM != m.diskPath(p.ID, "-image.fd") || p.Keys != filepath.Join(m.Store.Root, "images", p.ID) {
		return fmt.Errorf("pending %s: refusing to collect paths outside managed storage", name)
	}
	return nil
}

var removePendingMarker = func(m *Manager, id string) error {
	return m.removePending(id)
}

func (m *Manager) pendingStageMissing(p pendingCapture) (bool, error) {
	if err := ValidateName(p.Stage); err != nil {
		return false, err
	}
	_, err := m.Store.Load(p.Stage)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, os.ErrNotExist) || os.IsNotExist(err) {
		return true, nil
	}
	return false, err
}

func mappingPointsAt(r *Record, id string) bool {
	if r == nil || id == "" {
		return false
	}
	if r.Source.Image == id {
		return true
	}
	for _, got := range r.Snapshots {
		if got == id {
			return true
		}
	}
	return false
}

// recoverPending applies the three Recover cases for this stage's markers.
// An unreadable file has no stage name, so it is unknown: warn and skip.
func (m *Manager) recoverPending(r *Record) error {
	scan, err := m.scanPending()
	if err != nil {
		return err
	}
	for _, name := range scan.Unread {
		m.warn("ignoring unreadable pending marker " + name)
	}
	for _, p := range scan.Found {
		if p.Stage != r.Name {
			continue
		}
		if err := m.pendingManagedPaths(p); err != nil {
			m.warn("ignoring pending marker with unmanaged paths " + p.ID + ".json")
			continue
		}
		_, jsonErr := os.Stat(m.Store.imageJSON(p.ID))
		if os.IsNotExist(jsonErr) {
			removePendingFiles(p)
			if err := m.removePending(p.ID); err != nil {
				return err
			}
			continue
		}
		if jsonErr != nil {
			return jsonErr
		}
		if err := m.removePending(p.ID); err != nil {
			return err
		}
	}
	return nil
}

func removePendingFiles(p pendingCapture) {
	for _, path := range []string{p.Disk, p.NVRAM} {
		_ = os.Remove(path)
	}
	if p.Keys != "" {
		_ = os.RemoveAll(p.Keys)
	}
}

func (m *Manager) failPending(ctx context.Context, p pendingCapture, extra []string) (time.Duration, error) {
	if err := m.pendingManagedPaths(p); err != nil {
		return 0, err
	}
	for n := len(extra) - 1; n >= 0; n-- {
		_ = os.RemoveAll(extra[n])
	}
	removePendingFiles(p)

	tryRemove := func() error {
		rel, err := m.Store.LockMany("image-catalog")
		if err != nil {
			return err
		}
		defer rel()
		return m.removePending(p.ID)
	}

	if err := ctx.Err(); err != nil {
		_ = tryRemove()
		return 0, err
	}
	began := m.now()
	release, err := m.Store.LockWait(ctx, "image-catalog")
	if err != nil {
		if ctx.Err() != nil {
			_ = tryRemove()
			return m.since(began), ctx.Err()
		}
		return m.since(began), err
	}
	defer release()
	return m.since(began), m.removePending(p.ID)
}

func (m *Manager) lockCatalog(ctx context.Context) (time.Duration, func(), error) {
	began := m.now()
	release, err := m.Store.LockWait(ctx, "image-catalog")
	return m.since(began), release, err
}

func (m *Manager) noteCatalogWait(r *Record, waited time.Duration) *float64 {
	secs := seconds(waited)
	m.logTiming(r, "catalog-wait-seconds", secs)
	return &secs
}

func (m *Manager) pendingMarkerCheck() Check {
	scan, err := m.scanPending()
	if err != nil {
		return Check{Name: "pending-markers", OK: false, Detail: err.Error()}
	}
	var bad []string
	bad = append(bad, scan.Unread...)
	for _, p := range scan.Found {
		if err := m.pendingManagedPaths(p); err != nil {
			bad = append(bad, p.ID+".json")
		}
	}
	if len(bad) == 0 {
		return Check{Name: "pending-markers", OK: true, Detail: "no unreadable pending markers"}
	}
	return Check{Name: "pending-markers", OK: false, Detail: "unreadable or unmanaged: " + strings.Join(bad, ", ")}
}
