package machine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/This-Is-NPC/backstage/internal/guest"
)

func Session(g *guest.Guest) (string, error) {
	out, err := g.InSession(`test -n "$HYPRLAND_INSTANCE_SIGNATURE" && hyprctl -j monitors >/dev/null && cat /proc/sys/kernel/random/boot_id && printf '%s\n' "$HYPRLAND_INSTANCE_SIGNATURE"`)
	if err != nil {
		return "", err
	}
	if len(strings.Fields(out)) != 2 {
		return "", errors.New("guest has no identifiable live graphical session")
	}
	return strings.TrimSpace(out), nil
}

func CheckContinuity(last *Continuity, project, after string, recording bool, session string) error {
	if last == nil || last.Project != project || last.Scene != after || last.Recording != recording || last.Session != session || session == "" {
		return fmt.Errorf("cannot continue %q: its successful %s in this project and live session is unavailable", after, map[bool]string{true: "recording", false: "rehearsal"}[recording])
	}
	return nil
}

// Begin is called under the stage lock, before project hooks. Continuity is
// consumed before any action so a failed or interrupted take cannot certify it.
func (m *Manager) Begin(ctx context.Context, r *Record, mode, snapshot, after, project string, recording bool) (*guest.Guest, error) {
	if err := m.Recover(ctx, r); err != nil {
		return nil, err
	}
	if r.Status != "ready" {
		return nil, fmt.Errorf("stage %s is not ready (%s)", r.Name, r.Status)
	}
	if mode == "clean" {
		if snapshot == "" {
			snapshot = "initial"
		}
		release, err := m.Store.LockMany("image-catalog")
		if err != nil {
			return nil, err
		}
		err = m.Restore(ctx, r, snapshot)
		release()
		if err != nil {
			return nil, err
		}
	}
	last := r.Continuity
	r.Continuity = nil
	if err := m.Store.Save(r); err != nil {
		return nil, err
	}
	if mode == "continue" {
		state, err := m.State(ctx, r)
		if err != nil {
			return nil, err
		}
		if state != "running" {
			return nil, errors.New("continuation requires a running stage; it will not be booted automatically")
		}
	}
	g, err := m.Start(ctx, r)
	if err != nil {
		return nil, err
	}
	g.StartMode = mode
	if mode == "clean" {
		g.Snapshot = snapshot
	}
	if mode == "continue" {
		session, err := Session(g)
		if err != nil {
			return nil, err
		}
		if err := CheckContinuity(last, project, after, recording, session); err != nil {
			return nil, err
		}
	}
	return g, nil
}

func (m *Manager) Finish(r *Record, g *guest.Guest, project, name string, recording bool) error {
	session, err := Session(g)
	// A successful framebuffer take may deliberately end the session. It is a
	// valid clip but cannot be the predecessor of a live continuation.
	if err != nil {
		r.Continuity = nil
		return m.Store.Save(r)
	}
	r.Continuity = &Continuity{Project: project, Scene: name, Recording: recording, Session: session}
	return m.Store.Save(r)
}
