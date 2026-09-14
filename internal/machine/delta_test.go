package machine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func catalogImage(t *testing.T, m *Manager, parent string) *Image {
	t.Helper()
	id := randomID()
	i := &Image{Schema: ImageSchema, ID: id, Disk: m.diskPath(id, "-image.qcow2"), NVRAM: m.diskPath(id, "-image.fd"), Parent: parent}
	if parent != "" {
		i.Schema = ImageSchemaV2
	}
	if err := os.WriteFile(i.Disk, []byte("disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(i.NVRAM, []byte("vars"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(m.Store.imageJSON(id), i); err != nil {
		t.Fatal(err)
	}
	return i
}

func chainInfo(active string, disks ...string) string {
	var parts []string
	parts = append(parts, fmt.Sprintf(`{"filename":%q,"full-backing-filename":%q}`, active, disks[0]))
	for i, d := range disks {
		if i+1 < len(disks) {
			parts = append(parts, fmt.Sprintf(`{"filename":%q,"full-backing-filename":%q}`, d, disks[i+1]))
		} else {
			parts = append(parts, fmt.Sprintf(`{"filename":%q}`, d))
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func attachDeltaRunner(m *Manager, r *Record, disks []string, convert func(args []string) error) {
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			if len(args) > 0 && args[0] == "info" {
				return chainInfo(r.Disk, disks...), nil
			}
			if convert != nil {
				return "", convert(args)
			}
			return "", os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
		}
		switch args[2] {
		case "domuuid":
			return uuid(r.ID), nil
		case "domstate":
			return "shut off", nil
		default:
			return "", fmt.Errorf("unexpected %s %v", bin, args)
		}
	})
}

func oldBinaryAccepts(i *Image) bool {
	return i.Schema == 1
}

func TestStoreImageReadsSchema1And2(t *testing.T) {
	m := testManager(t)
	a := catalogImage(t, m, "")
	b := catalogImage(t, m, a.ID)
	got, err := m.Store.Image(a.ID)
	if err != nil || got.Schema != ImageSchema {
		t.Fatalf("schema1: %+v %v", got, err)
	}
	got, err = m.Store.Image(b.ID)
	if err != nil || got.Schema != ImageSchemaV2 || got.Parent != a.ID {
		t.Fatalf("schema2: %+v %v", got, err)
	}
}

func TestSnapshotWritesDeltaAgainstSourceImage(t *testing.T) {
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	var usedB bool
	attachDeltaRunner(m, r, []string{parent.Disk}, func(args []string) error {
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-B "+parent.Disk) {
			t.Fatalf("convert: %v", args)
		}
		usedB = true
		return os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
	})
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	if !usedB {
		t.Fatal("expected delta convert")
	}
	child, err := m.Store.Image(r.Snapshots["hand"])
	if err != nil {
		t.Fatal(err)
	}
	if child.Schema != ImageSchemaV2 || child.Parent != parent.ID {
		t.Fatalf("child: %+v", child)
	}
	got, err := m.Store.Image(parent.ID)
	if err != nil || got.Schema != ImageSchemaV2 {
		t.Fatalf("parent not promoted: %+v %v", got, err)
	}
	log := readProvisionLog(t, m, "demo")
	if !strings.Contains(log, "timing capture-mode delta") || !strings.Contains(log, "timing image-depth 1") {
		t.Fatalf("log: %s", log)
	}
	if strings.Contains(log, "timing capture-fallback") {
		t.Fatalf("fallback on delta: %s", log)
	}
}

func TestDeltaOnDeltaStaysIncremental(t *testing.T) {
	m, r := captureReady(t, "demo")
	base := catalogImage(t, m, "")
	base.Schema = ImageSchemaV2
	if err := atomicJSON(m.Store.imageJSON(base.ID), base); err != nil {
		t.Fatal(err)
	}
	mid := catalogImage(t, m, base.ID)
	r.Source.Image = mid.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	attachDeltaRunner(m, r, []string{mid.Disk, base.Disk}, nil)
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	child, err := m.Store.Image(r.Snapshots["hand"])
	if err != nil || child.Parent != mid.ID || child.Schema != ImageSchemaV2 {
		t.Fatalf("child: %+v %v", child, err)
	}
	log := readProvisionLog(t, m, "demo")
	if !strings.Contains(log, "timing image-depth 2") {
		t.Fatalf("log: %s", log)
	}
}

func TestSaveInitialNeverWritesDelta(t *testing.T) {
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	attachDeltaRunner(m, r, []string{parent.Disk}, func(args []string) error {
		if strings.Contains(strings.Join(args, " "), "-B ") {
			t.Fatalf("save-initial used -B: %v", args)
		}
		return os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
	})
	img, err := m.capture(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if img.Schema != ImageSchema || img.Parent != "" {
		t.Fatalf("initial: %+v", img)
	}
	got, err := m.Store.Image(parent.ID)
	if err != nil || got.Schema != ImageSchema || got.Parent != "" {
		t.Fatalf("promoted origin: %+v %v", got, err)
	}
}

func TestUnexpectedBackingFallsBackToComplete(t *testing.T) {
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	var progress strings.Builder
	m.Output = &progress
	other := m.diskPath("otherpath", "-image.qcow2")
	attachDeltaRunner(m, r, []string{other}, func(args []string) error {
		if strings.Contains(strings.Join(args, " "), "-B ") {
			t.Fatalf("fallback used -B: %v", args)
		}
		return os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
	})
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	child, err := m.Store.Image(r.Snapshots["hand"])
	if err != nil || child.Schema != ImageSchema || child.Parent != "" {
		t.Fatalf("child: %+v %v", child, err)
	}
	got, _ := m.Store.Image(parent.ID)
	if got.Schema != ImageSchema {
		t.Fatal("parent promoted on fallback")
	}
	log := readProvisionLog(t, m, "demo")
	if !strings.Contains(log, "timing capture-mode complete") || !strings.Contains(log, "timing capture-fallback "+fallbackBackingPath) {
		t.Fatalf("log: %s", log)
	}
	if !strings.Contains(progress.String(), "capture complete ("+fallbackBackingPath+")") {
		t.Fatalf("progress: %s", progress.String())
	}
}

func TestCatalogDisagreementFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		setup        func(t *testing.T, m *Manager, r *Record) []string
	}{
		{
			name:   "missing-ancestor",
			reason: fallbackMissingAncestor,
			setup: func(t *testing.T, m *Manager, r *Record) []string {
				id := randomID()
				r.Source.Image = id
				disk := m.diskPath(id, "-image.qcow2")
				return []string{disk}
			},
		},
		{
			name:   "repeated-ancestor",
			reason: fallbackRepeatedAncestor,
			setup: func(t *testing.T, m *Manager, r *Record) []string {
				a := catalogImage(t, m, "")
				a.Parent = a.ID
				a.Schema = ImageSchemaV2
				if err := atomicJSON(m.Store.imageJSON(a.ID), a); err != nil {
					t.Fatal(err)
				}
				r.Source.Image = a.ID
				return []string{a.Disk}
			},
		},
		{
			name:   "catalog-parent-mismatch",
			reason: fallbackCatalogParent,
			setup: func(t *testing.T, m *Manager, r *Record) []string {
				base := catalogImage(t, m, "")
				mid := catalogImage(t, m, base.ID)
				r.Source.Image = mid.ID
				return []string{mid.Disk}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, r := captureReady(t, "demo")
			disks := tc.setup(t, m, r)
			if err := m.Store.Save(r); err != nil {
				t.Fatal(err)
			}
			attachDeltaRunner(m, r, disks, nil)
			if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
				t.Fatal(err)
			}
			log := readProvisionLog(t, m, "demo")
			if !strings.Contains(log, "timing capture-fallback "+tc.reason) {
				t.Fatalf("log: %s", log)
			}
			child, err := m.Store.Image(r.Snapshots["hand"])
			if err != nil || child.Parent != "" {
				t.Fatalf("expected complete: %+v %v", child, err)
			}
		})
	}
}

func TestDepthLimitFlattens(t *testing.T) {
	m, r := captureReady(t, "demo")
	zero := 0
	m.MaxImageDepth = &zero
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	attachDeltaRunner(m, r, []string{parent.Disk}, func(args []string) error {
		if strings.Contains(strings.Join(args, " "), "-B ") {
			t.Fatal("limit 0 used delta")
		}
		return os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
	})
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readProvisionLog(t, m, "demo"), "timing capture-fallback "+fallbackDepthLimit) {
		t.Fatal(readProvisionLog(t, m, "demo"))
	}
}

func TestDepthAboveLimitFlattens(t *testing.T) {
	m, r := captureReady(t, "demo")
	one := 1
	m.MaxImageDepth = &one
	base := catalogImage(t, m, "")
	base.Schema = ImageSchemaV2
	if err := atomicJSON(m.Store.imageJSON(base.ID), base); err != nil {
		t.Fatal(err)
	}
	mid := catalogImage(t, m, base.ID)
	r.Source.Image = mid.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	attachDeltaRunner(m, r, []string{mid.Disk, base.Disk}, nil)
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	child, _ := m.Store.Image(r.Snapshots["hand"])
	if child.Parent != "" {
		t.Fatalf("should flatten: %+v", child)
	}
	if !strings.Contains(readProvisionLog(t, m, "demo"), fallbackDepthLimit) {
		t.Fatal(readProvisionLog(t, m, "demo"))
	}
}

func TestUnreadableDiskFailsWithoutCommit(t *testing.T) {
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(r.Disk); err != nil {
		t.Fatal(err)
	}
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			t.Fatal("must not convert an unreadable disk")
		}
		switch args[2] {
		case "domuuid":
			return uuid(r.ID), nil
		case "domstate":
			return "shut off", nil
		default:
			return "", fmt.Errorf("unexpected %v", args)
		}
	})
	if err := m.Snapshot(context.Background(), r, "hand"); err == nil {
		t.Fatal("expected unreadable")
	}
	if r.Snapshots["hand"] != "" {
		t.Fatal("mapping committed")
	}
	got, _ := m.Store.Image(parent.ID)
	if got.Schema != ImageSchema {
		t.Fatal("parent rewritten")
	}
}

func TestConvertFailureDoesNotPromoteParent(t *testing.T) {
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	attachDeltaRunner(m, r, []string{parent.Disk}, func([]string) error {
		return errors.New("qemu-img convert failed")
	})
	if err := m.Snapshot(context.Background(), r, "hand"); err == nil {
		t.Fatal("expected convert failure")
	}
	got, _ := m.Store.Image(parent.ID)
	if got.Schema != ImageSchema {
		t.Fatal("parent promoted before convert")
	}
	if r.Snapshots["hand"] != "" {
		t.Fatal("mapping committed")
	}
}

func TestParentRewriteFailureRemovesChild(t *testing.T) {
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	attachDeltaRunner(m, r, []string{parent.Disk}, nil)
	prev := writeImageJSON
	writeImageJSON = func(path string, v any) error {
		if strings.HasSuffix(path, parent.ID+".json") {
			return errors.New("parent rewrite failed")
		}
		return atomicJSON(path, v)
	}
	t.Cleanup(func() { writeImageJSON = prev })
	if err := m.Snapshot(context.Background(), r, "hand"); err == nil || !strings.Contains(err.Error(), "parent rewrite failed") {
		t.Fatalf("err: %v", err)
	}
	got, _ := m.Store.Image(parent.ID)
	if got.Schema != ImageSchema {
		t.Fatal("parent changed")
	}
	if r.Snapshots["hand"] != "" {
		t.Fatal("mapping committed")
	}
	images, err := filepath.Glob(filepath.Join(m.Store.Root, "images", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 {
		t.Fatalf("child leaked: %v", images)
	}
}

func TestCrashBetweenParentAndChildJSON(t *testing.T) {
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	promoted := *parent
	promoted.Schema = ImageSchemaV2
	if err := atomicJSON(m.Store.imageJSON(parent.ID), promoted); err != nil {
		t.Fatal(err)
	}
	got, err := m.Store.Image(parent.ID)
	if err != nil || got.Schema != ImageSchemaV2 {
		t.Fatalf("new binary reads promoted parent: %+v %v", got, err)
	}
	if r.Snapshots["hand"] != "" {
		t.Fatal("child was catalogued")
	}
}

func TestCapturePermissions0440ThenACL(t *testing.T) {
	m, r := captureReady(t, "demo")
	var saw string
	protectCapturedDisk = func(path string) error {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o440 {
			t.Fatalf("ACL before 0440: %o", info.Mode().Perm())
		}
		saw = path
		return applyNamedReadACL(path)
	}
	t.Cleanup(func() { protectCapturedDisk = applyNamedReadACL })
	img, err := m.capture(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if saw != img.Disk {
		t.Fatal("ACL hook not called")
	}
}

func TestDeleteOriginKeepsCloneOnDeltaSource(t *testing.T) {
	m := testManager(t)
	base := catalogImage(t, m, "")
	base.Schema = ImageSchemaV2
	if err := atomicJSON(m.Store.imageJSON(base.ID), base); err != nil {
		t.Fatal(err)
	}
	delta := catalogImage(t, m, base.ID)
	origin := testRecord("origin")
	origin.Source.Image = base.ID
	origin.Snapshots["ready"] = delta.ID
	if err := m.Store.Save(origin); err != nil {
		t.Fatal(err)
	}
	initial := catalogImage(t, m, "")
	clone := testRecord("clone")
	clone.Source.Image = delta.ID
	clone.Snapshots["initial"] = initial.ID
	if err := m.Store.Save(clone); err != nil {
		t.Fatal(err)
	}
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, _ string, args ...string) (string, error) {
		switch args[2] {
		case "domuuid":
			return "", errors.New("not defined")
		case "list":
			return "", nil
		default:
			return "", fmt.Errorf("unexpected %v", args)
		}
	})
	if err := m.Delete(context.Background(), origin); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{base.ID, delta.ID, initial.ID} {
		if _, err := m.Store.Image(id); err != nil {
			t.Fatalf("lost %s: %v", id, err)
		}
	}
	loaded, err := m.Store.Load("clone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.Image(loaded.Source.Image); err != nil {
		t.Fatal("clone source gone")
	}
	if _, err := m.Store.Image(loaded.Snapshots["initial"]); err != nil {
		t.Fatal("clone initial gone")
	}
}

func TestCollectKeepsAncestorsFromJournalAndBases(t *testing.T) {
	m := testManager(t)
	base := catalogImage(t, m, "")
	base.Schema = ImageSchemaV2
	if err := atomicJSON(m.Store.imageJSON(base.ID), base); err != nil {
		t.Fatal(err)
	}
	delta := catalogImage(t, m, base.ID)
	journal := catalogImage(t, m, delta.ID)
	orphan := catalogImage(t, m, "")
	r := testRecord("demo")
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(m.Store.Dir(r.Name), "activate.json"), activation{
		Old:  Record{Source: Source{Image: journal.ID}},
		Next: Record{Source: Source{Image: delta.ID}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(m.Store.Root, "bases", "base.json"), base.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Collect(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{base.ID, delta.ID, journal.ID} {
		if _, err := m.Store.Image(id); err != nil {
			t.Fatalf("collected ancestor %s: %v", id, err)
		}
	}
	if _, err := m.Store.Image(orphan.ID); !os.IsNotExist(err) {
		t.Fatalf("orphan remains: %v", err)
	}
}

func TestCollectCycleFailsBeforeDelete(t *testing.T) {
	m := testManager(t)
	a := catalogImage(t, m, "")
	b := catalogImage(t, m, a.ID)
	a.Parent = b.ID
	a.Schema = ImageSchemaV2
	if err := atomicJSON(m.Store.imageJSON(a.ID), a); err != nil {
		t.Fatal(err)
	}
	orphan := catalogImage(t, m, "")
	r := testRecord("demo")
	r.Source.Image = b.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := m.Collect(); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle: %v", err)
	}
	if _, err := m.Store.Image(orphan.ID); err != nil {
		t.Fatal("deleted before validating")
	}
}

func TestCollectMissingAncestorFailsBeforeDelete(t *testing.T) {
	m := testManager(t)
	delta := catalogImage(t, m, randomID())
	orphan := catalogImage(t, m, "")
	r := testRecord("demo")
	r.Snapshots["ready"] = delta.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := m.Collect(); err == nil {
		t.Fatal("expected missing ancestor")
	}
	if _, err := m.Store.Image(orphan.ID); err != nil {
		t.Fatal("deleted before validating")
	}
}

func TestSnapshotAgainstCachedBaseStaysComplete(t *testing.T) {
	m, r := captureReady(t, "demo")
	base := catalogImage(t, m, "")
	r.Source.Image = base.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(m.Store.Root, "bases", "omarchy.json"), base.ID); err != nil {
		t.Fatal(err)
	}
	attachDeltaRunner(m, r, []string{base.Disk}, func(args []string) error {
		if strings.Contains(strings.Join(args, " "), "-B ") {
			t.Fatalf("cached base used delta: %v", args)
		}
		return os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
	})
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	child, err := m.Store.Image(r.Snapshots["hand"])
	if err != nil || child.Parent != "" || child.Schema != ImageSchema {
		t.Fatalf("child: %+v %v", child, err)
	}
	got, err := m.Store.Image(base.ID)
	if err != nil || got.Schema != ImageSchema {
		t.Fatalf("base promoted: %+v %v", got, err)
	}
	log := readProvisionLog(t, m, "demo")
	if !strings.Contains(log, "timing capture-fallback "+fallbackCachedBase) {
		t.Fatalf("log: %s", log)
	}
}

func TestUnreadableBaseCacheFallsBackToComplete(t *testing.T) {
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Store.Root, "bases", "omarchy.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var progress strings.Builder
	m.Output = &progress
	attachDeltaRunner(m, r, []string{parent.Disk}, func(args []string) error {
		if strings.Contains(strings.Join(args, " "), "-B ") {
			t.Fatalf("unreadable base cache used delta: %v", args)
		}
		return os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
	})
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	if r.Snapshots["hand"] == "" {
		t.Fatal("snapshot not saved")
	}
	child, err := m.Store.Image(r.Snapshots["hand"])
	if err != nil || child.Parent != "" || child.Schema != ImageSchema {
		t.Fatalf("child: %+v %v", child, err)
	}
	got, err := m.Store.Image(parent.ID)
	if err != nil || got.Schema != ImageSchema {
		t.Fatalf("parent promoted: %+v %v", got, err)
	}
	log := readProvisionLog(t, m, "demo")
	if !strings.Contains(log, "timing capture-mode complete") || !strings.Contains(log, "timing capture-fallback "+fallbackBaseCacheUnread) {
		t.Fatalf("log: %s", log)
	}
	if !strings.Contains(progress.String(), "capture complete ("+fallbackBaseCacheUnread+")") {
		t.Fatalf("progress: %s", progress.String())
	}
}

func TestLimitZeroSkipsBackingProbe(t *testing.T) {
	m, r := captureReady(t, "demo")
	zero := 0
	m.MaxImageDepth = &zero
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			if len(args) > 0 && args[0] == "info" {
				return "", errors.New("qemu-img info failed")
			}
			return "", os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
		}
		switch args[2] {
		case "domuuid":
			return uuid(r.ID), nil
		case "domstate":
			return "shut off", nil
		default:
			return "", fmt.Errorf("unexpected %v", args)
		}
	})
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	if r.Snapshots["hand"] == "" {
		t.Fatal("snapshot not saved")
	}
	log := readProvisionLog(t, m, "demo")
	if !strings.Contains(log, "timing capture-mode complete") || !strings.Contains(log, "timing capture-fallback "+fallbackDepthLimit) {
		t.Fatalf("log: %s", log)
	}
}

func TestQemuChainLongerThanCatalogFallsBack(t *testing.T) {
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	extra := m.diskPath("extra", "-image.qcow2")
	attachDeltaRunner(m, r, []string{parent.Disk, extra}, nil)
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	child, _ := m.Store.Image(r.Snapshots["hand"])
	if child.Parent != "" {
		t.Fatalf("expected complete: %+v", child)
	}
	if !strings.Contains(readProvisionLog(t, m, "demo"), "timing capture-fallback "+fallbackCatalogParent) {
		t.Fatal(readProvisionLog(t, m, "demo"))
	}
}

func TestBackingFilenameMismatchFallsBack(t *testing.T) {
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	alias := m.diskPath("aliaspath", "-image.qcow2")
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" && len(args) > 0 && args[0] == "info" {
			return fmt.Sprintf(`[{"filename":%q,"full-backing-filename":%q},{"filename":%q}]`, r.Disk, parent.Disk, alias), nil
		}
		if bin == "qemu-img" {
			return "", os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
		}
		switch args[2] {
		case "domuuid":
			return uuid(r.ID), nil
		case "domstate":
			return "shut off", nil
		default:
			return "", fmt.Errorf("unexpected %v", args)
		}
	})
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readProvisionLog(t, m, "demo"), "timing capture-fallback "+fallbackBackingChain) {
		t.Fatal(readProvisionLog(t, m, "demo"))
	}
}

func TestStoreImageRejectsSchema3(t *testing.T) {
	m := testManager(t)
	id := randomID()
	i := Image{Schema: 3, ID: id, Disk: m.diskPath(id, "-image.qcow2"), NVRAM: m.diskPath(id, "-image.fd")}
	if err := atomicJSON(m.Store.imageJSON(id), i); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.Image(id); err == nil || !strings.Contains(err.Error(), "unsupported image record") {
		t.Fatalf("schema 3: %v", err)
	}
}

func TestCollectIgnoresAbsentDirectRef(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *Manager, *Record, string)
	}{
		{"snapshot", func(_ *testing.T, _ *Manager, r *Record, id string) {
			r.Snapshots["ghost"] = id
		}},
		{"source", func(_ *testing.T, _ *Manager, r *Record, id string) {
			r.Source.Image = id
		}},
		{"activate-next", func(t *testing.T, m *Manager, r *Record, id string) {
			if err := atomicJSON(filepath.Join(m.Store.Dir(r.Name), "activate.json"), activation{
				Next: Record{Source: Source{Image: id}},
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{"activate-old", func(t *testing.T, m *Manager, r *Record, id string) {
			if err := atomicJSON(filepath.Join(m.Store.Dir(r.Name), "activate.json"), activation{
				Old: Record{Source: Source{Image: id}},
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{"base", func(t *testing.T, m *Manager, _ *Record, id string) {
			if err := atomicJSON(filepath.Join(m.Store.Root, "bases", "omarchy.json"), id); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testManager(t)
			orphan := catalogImage(t, m, "")
			r := testRecord("demo")
			if err := m.Store.Save(r); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, m, r, randomID())
			if err := m.Store.Save(r); err != nil {
				t.Fatal(err)
			}
			if err := m.Collect(); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Store.Image(orphan.ID); !os.IsNotExist(err) {
				t.Fatalf("orphan remains: %v", err)
			}
		})
	}
}

func TestCollectDiskWithoutJSONStops(t *testing.T) {
	for _, tc := range []struct {
		name, where string
		setup       func(*testing.T, *Manager, *Record, string)
	}{
		{"snapshot", "stage demo snapshot broken", func(_ *testing.T, _ *Manager, r *Record, id string) {
			r.Snapshots["broken"] = id
		}},
		{"source", "stage demo source", func(_ *testing.T, _ *Manager, r *Record, id string) {
			r.Source.Image = id
		}},
		{"activate-next", "stage demo activate.json next", func(t *testing.T, m *Manager, r *Record, id string) {
			if err := atomicJSON(filepath.Join(m.Store.Dir(r.Name), "activate.json"), activation{
				Next: Record{Source: Source{Image: id}},
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{"activate-old", "stage demo activate.json old", func(t *testing.T, m *Manager, r *Record, id string) {
			if err := atomicJSON(filepath.Join(m.Store.Dir(r.Name), "activate.json"), activation{
				Old: Record{Source: Source{Image: id}},
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{"base", "base omarchy.json", func(t *testing.T, m *Manager, _ *Record, id string) {
			if err := atomicJSON(filepath.Join(m.Store.Root, "bases", "omarchy.json"), id); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testManager(t)
			orphan := catalogImage(t, m, "")
			missing := randomID()
			if err := os.WriteFile(m.diskPath(missing, "-image.qcow2"), []byte("disk"), 0o600); err != nil {
				t.Fatal(err)
			}
			r := testRecord("demo")
			if err := m.Store.Save(r); err != nil {
				t.Fatal(err)
			}
			tc.setup(t, m, r, missing)
			if err := m.Store.Save(r); err != nil {
				t.Fatal(err)
			}
			err := m.Collect()
			if err == nil || !strings.Contains(err.Error(), tc.where) || !strings.Contains(err.Error(), missing) {
				t.Fatalf("err: %v", err)
			}
			if _, err := m.Store.Image(orphan.ID); err != nil {
				t.Fatal("deleted before validating")
			}
		})
	}
}

func TestSnapshotAgainstCachedBaseAncestorStaysComplete(t *testing.T) {
	m, r := captureReady(t, "demo")
	base := catalogImage(t, m, "")
	mid := catalogImage(t, m, base.ID)
	r.Source.Image = mid.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(m.Store.Root, "bases", "omarchy.json"), base.ID); err != nil {
		t.Fatal(err)
	}
	attachDeltaRunner(m, r, []string{mid.Disk, base.Disk}, func(args []string) error {
		if strings.Contains(strings.Join(args, " "), "-B ") {
			t.Fatalf("cached ancestor used delta: %v", args)
		}
		return os.WriteFile(args[len(args)-1], []byte("image"), 0o600)
	})
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	child, err := m.Store.Image(r.Snapshots["hand"])
	if err != nil || child.Parent != "" {
		t.Fatalf("child: %+v %v", child, err)
	}
	got, err := m.Store.Image(base.ID)
	if err != nil || got.Schema != ImageSchema {
		t.Fatalf("base promoted: %+v %v", got, err)
	}
	if !strings.Contains(readProvisionLog(t, m, "demo"), "timing capture-fallback "+fallbackCachedBase) {
		t.Fatal(readProvisionLog(t, m, "demo"))
	}
}

func TestCollectRefusesLaterUnmanagedOrUnreadable(t *testing.T) {
	for _, tc := range []struct {
		name string
		bad  func(t *testing.T, m *Manager, id string)
	}{
		{"unmanaged", func(t *testing.T, m *Manager, id string) {
			i := Image{Schema: ImageSchema, ID: id, Disk: filepath.Join(t.TempDir(), "foreign.qcow2"), NVRAM: m.diskPath(id, "-image.fd")}
			if err := os.WriteFile(i.Disk, []byte("disk"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(i.NVRAM, []byte("vars"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := atomicJSON(m.Store.imageJSON(id), i); err != nil {
				t.Fatal(err)
			}
		}},
		{"unreadable", func(t *testing.T, m *Manager, id string) {
			i := Image{Schema: 3, ID: id, Disk: m.diskPath(id, "-image.qcow2"), NVRAM: m.diskPath(id, "-image.fd")}
			if err := os.WriteFile(i.Disk, []byte("disk"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := atomicJSON(m.Store.imageJSON(id), i); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testManager(t)
			keep := strings.Repeat("1", 32)
			i := Image{Schema: ImageSchema, ID: keep, Disk: m.diskPath(keep, "-image.qcow2"), NVRAM: m.diskPath(keep, "-image.fd")}
			if err := os.WriteFile(i.Disk, []byte("disk"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(i.NVRAM, []byte("vars"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := atomicJSON(m.Store.imageJSON(keep), i); err != nil {
				t.Fatal(err)
			}
			bad := strings.Repeat("9", 32)
			tc.bad(t, m, bad)
			if err := m.Collect(); err == nil {
				t.Fatal("expected collect to stop")
			}
			if _, err := m.Store.Image(keep); err != nil {
				t.Fatal("deletable image was removed")
			}
		})
	}
}

func TestCollectRefusesUnmanagedImage(t *testing.T) {
	m := testManager(t)
	id := randomID()
	i := Image{Schema: ImageSchema, ID: id, Disk: filepath.Join(t.TempDir(), "foreign.qcow2"), NVRAM: m.diskPath(id, "-image.fd")}
	if err := os.WriteFile(i.Disk, []byte("disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(i.NVRAM, []byte("vars"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(m.Store.imageJSON(id), i); err != nil {
		t.Fatal(err)
	}
	if err := m.Collect(); err == nil || !strings.Contains(err.Error(), "outside managed storage") {
		t.Fatalf("err: %v", err)
	}
	if _, err := m.Store.Image(id); err != nil {
		t.Fatal("unmanaged image was removed")
	}
}

func collectLike6ee1c85(m *Manager) error {
	used := map[string]bool{}
	stages, err := m.Store.List()
	if err != nil {
		return err
	}
	for _, r := range stages {
		used[r.Source.Image] = true
		for _, id := range r.Snapshots {
			used[id] = true
		}
		var a activation
		if err := readJSON(filepath.Join(m.Store.Dir(r.Name), "activate.json"), &a); err == nil {
			used[a.Next.Source.Image] = true
			used[a.Old.Source.Image] = true
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	baseFiles, err := filepath.Glob(filepath.Join(m.Store.Root, "bases", "*.json"))
	if err != nil {
		return err
	}
	for _, path := range baseFiles {
		var id string
		if err := readJSON(path, &id); err != nil {
			return err
		}
		used[id] = true
	}
	files, err := filepath.Glob(filepath.Join(m.Store.Root, "images", "*.json"))
	if err != nil {
		return err
	}
	for _, path := range files {
		id := strings.TrimSuffix(filepath.Base(path), ".json")
		if used[id] {
			continue
		}
		var i Image
		if err := readJSON(path, &i); err != nil {
			return err
		}
		if i.Schema != 1 || i.ID != id {
			return errors.New("unsupported image record")
		}
		if i.Disk != m.diskPath(id, "-image.qcow2") || i.NVRAM != m.diskPath(id, "-image.fd") {
			return errors.New("refusing to collect an image outside managed storage")
		}
		for _, file := range []string{i.Disk, i.NVRAM, path} {
			if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err := os.RemoveAll(filepath.Join(m.Store.Root, "images", id)); err != nil {
			return err
		}
	}
	return nil
}

func TestOldCollectStopsAtSchema2AfterDeletingUnusedSchema1(t *testing.T) {
	m := testManager(t)
	unused := strings.Repeat("a", 32)
	parentID := strings.Repeat("b", 32)
	childID := strings.Repeat("c", 32)
	for _, id := range []string{unused, parentID, childID} {
		schema := ImageSchema
		parent := ""
		if id == parentID {
			schema = ImageSchemaV2
		}
		if id == childID {
			schema = ImageSchemaV2
			parent = parentID
		}
		i := Image{Schema: schema, ID: id, Disk: m.diskPath(id, "-image.qcow2"), NVRAM: m.diskPath(id, "-image.fd"), Parent: parent}
		if err := os.WriteFile(i.Disk, []byte("disk"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(i.NVRAM, []byte("vars"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := atomicJSON(m.Store.imageJSON(id), i); err != nil {
			t.Fatal(err)
		}
	}
	r := testRecord("clone")
	r.Source.Image = childID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	err := collectLike6ee1c85(m)
	if err == nil || !strings.Contains(err.Error(), "unsupported image record") {
		t.Fatalf("old collect: %v", err)
	}
	if _, err := m.Store.Image(unused); !os.IsNotExist(err) {
		t.Fatalf("old collect should have removed unused schema 1: %v", err)
	}
	if _, err := m.Store.Image(parentID); err != nil {
		t.Fatal("old collect deleted the schema 2 parent")
	}
	if _, err := m.Store.Image(childID); err != nil {
		t.Fatal("old collect deleted the used child")
	}
}

func TestOldBinaryRefusesPromotedParentRestoreAndClone(t *testing.T) {
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	r.Snapshots["ready"] = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	attachDeltaRunner(m, r, []string{parent.Disk}, nil)
	if err := m.Snapshot(context.Background(), r, "hand"); err != nil {
		t.Fatal(err)
	}
	promoted, err := m.Store.Image(parent.ID)
	if err != nil || oldBinaryAccepts(promoted) {
		t.Fatalf("old restore/clone would accept parent: %+v %v", promoted, err)
	}
	child, err := m.Store.Image(r.Snapshots["hand"])
	if err != nil || oldBinaryAccepts(child) {
		t.Fatalf("old restore/clone would accept child: %+v %v", child, err)
	}
}

func TestDeleteSnapshotKeepsAncestors(t *testing.T) {
	m := testManager(t)
	base := catalogImage(t, m, "")
	base.Schema = ImageSchemaV2
	if err := atomicJSON(m.Store.imageJSON(base.ID), base); err != nil {
		t.Fatal(err)
	}
	ready := catalogImage(t, m, base.ID)
	later := catalogImage(t, m, ready.ID)
	r := testRecord("demo")
	r.Snapshots = map[string]string{"initial": base.ID, "ready": ready.ID, "later": later.ID}
	r.SnapshotOrigins = map[string]SnapshotOrigin{"ready": testOrigin("/p", "a"), "later": testOrigin("/p", "b")}
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DeleteSnapshot(r, "ready"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{base.ID, ready.ID, later.ID} {
		if _, err := m.Store.Image(id); err != nil {
			t.Fatalf("lost %s: %v", id, err)
		}
	}
}

func TestReplaceSnapshotWritesCaptureMeta(t *testing.T) {
	m, r := captureReady(t, "demo")
	parent := catalogImage(t, m, "")
	r.Source.Image = parent.ID
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	attachDeltaRunner(m, r, []string{parent.Disk}, nil)
	result, err := m.ReplaceSnapshot(context.Background(), r, "ready", testOrigin("/p", "s"), false)
	if err != nil {
		t.Fatal(err)
	}
	if result.CaptureMode == nil || *result.CaptureMode != CaptureModeDelta {
		t.Fatalf("mode: %+v", result)
	}
	if result.ImageDepth == nil || *result.ImageDepth != 1 {
		t.Fatalf("depth: %+v", result)
	}
	if result.CaptureFallback != nil {
		t.Fatalf("fallback: %+v", result)
	}
}

func TestParseBackingChainAcceptsImagesWrapper(t *testing.T) {
	chain, err := parseBackingChain(`{"images":[{"filename":"/a"}]}`)
	if err != nil || len(chain) != 1 || chain[0].Filename != "/a" {
		t.Fatalf("%+v %v", chain, err)
	}
}

func TestImageRecordOmitsEmptyParent(t *testing.T) {
	body, err := json.Marshal(Image{Schema: ImageSchema, ID: strings.Repeat("a", 32)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"parent"`) {
		t.Fatalf("%s", body)
	}
}
