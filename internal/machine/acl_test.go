package machine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyNamedReadACLEffectiveMaskIncludesRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.qcow2")
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyNamedReadACL(path); err != nil {
		t.Fatal(err)
	}
	ok, err := namedUserReadEffective(path, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("named read is not effective after applyNamedReadACL")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o007 != 0 {
		t.Fatalf("relaxed other bits: %o", info.Mode().Perm())
	}
}

func TestChmod0600AfterACLZerosEffectiveNamedRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.qcow2")
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyNamedReadACL(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	ok, err := namedUserReadEffective(path, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("chmod 0600 after the ACL did not zero the effective named read")
	}
}

func TestChmod0440AfterACLPreservesEffectiveNamedRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.qcow2")
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyNamedReadACL(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o440); err != nil {
		t.Fatal(err)
	}
	ok, err := namedUserReadEffective(path, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("chmod 0440 after the ACL lost the effective named read")
	}
}

func TestCaptureAppliesNamedReadACL(t *testing.T) {
	m, r := captureReady(t, "demo")
	img, err := m.capture(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := namedUserReadEffective(img.Disk, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("capture did not leave an effective named read ACL")
	}
	info, err := os.Stat(img.Disk)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o440 {
		t.Fatalf("capture mode: %o", info.Mode().Perm())
	}
}

func TestCaptureACLFailureRemovesPartial(t *testing.T) {
	m, r := captureReady(t, "demo")
	var dest string
	protectCapturedDisk = func(path string) error {
		dest = path
		return errors.New("xattr: operation not supported")
	}
	t.Cleanup(func() { protectCapturedDisk = applyNamedReadACL })
	_, err := m.capture(context.Background(), r)
	if err == nil {
		t.Fatal("expected ACL failure")
	}
	if !strings.Contains(err.Error(), "operation not supported") {
		t.Fatalf("error: %v", err)
	}
	if dest == "" {
		t.Fatal("protectCapturedDisk was not called")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("partial remains: %v", err)
	}
}

func TestDoctorPoolACLPassesOnCapableFilesystem(t *testing.T) {
	m := testManager(t)
	m.Store.Storage = filepath.Join(t.TempDir(), "missing-pool")
	missing := m.poolACLCheck()
	if !missing.OK || !strings.Contains(missing.Detail, "not created yet") {
		t.Fatalf("missing pool: %#v", missing)
	}
	m = testManager(t)
	check := m.poolACLCheck()
	if !check.OK {
		t.Fatalf("pool-acl: %s", check.Detail)
	}
	if check.Name != "pool-acl" {
		t.Fatalf("name: %s", check.Name)
	}
	found := false
	for _, c := range m.Doctor(context.Background()) {
		if c.Name == "pool-acl" {
			found = true
			if !c.OK {
				t.Fatalf("Doctor pool-acl: %s", c.Detail)
			}
		}
	}
	if !found {
		t.Fatal("Doctor omitted pool-acl")
	}
}

func TestDoctorListsUnreadableImageRemediation(t *testing.T) {
	m := testManager(t)
	id := randomID()
	disk := m.diskPath(id, "-image.qcow2")
	i := Image{Schema: ImageSchema, ID: id, Disk: disk, NVRAM: m.diskPath(id, "-image.fd")}
	if err := os.WriteFile(disk, []byte("disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(disk, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(disk, 0o600) })
	if err := atomicJSON(filepath.Join(m.Store.Root, "images", id+".json"), i); err != nil {
		t.Fatal(err)
	}
	want := remediateImageACL(disk)
	found := false
	for _, c := range m.catalogACLChecks() {
		if c.Name != "image-"+id[:8] {
			continue
		}
		found = true
		if c.OK {
			t.Fatal("unreadable image reported ok")
		}
		if c.Detail != want {
			t.Fatalf("remediation: %q want %q", c.Detail, want)
		}
	}
	if !found {
		t.Fatal("unreadable image was not listed")
	}
}

func TestQemuImgConvertReadsBackingThroughNamedACLMask(t *testing.T) {
	qemu, err := exec.LookPath("qemu-img")
	if err != nil {
		t.Skip("qemu-img not installed")
	}
	dir := t.TempDir()
	backing := filepath.Join(dir, "backing.qcow2")
	overlay := filepath.Join(dir, "overlay.qcow2")
	out := filepath.Join(dir, "out.qcow2")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(qemu, args...)
		cmd.Dir = dir
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", qemu, args, err, b)
		}
	}
	run("create", "-f", "qcow2", backing, "1M")
	if err := os.Chmod(backing, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyNamedReadACL(backing); err != nil {
		t.Fatal(err)
	}
	run("create", "-f", "qcow2", "-F", "qcow2", "-b", backing, overlay)
	run("convert", "-O", "qcow2", overlay, out)
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}

	// Mode bits can deny the backing open. The named ACL for this
	// process's own uid is not consulted: the owner is checked only
	// against user::. We do not chown to libvirt-qemu (957) here.
	if err := os.Chmod(backing, 0o000); err != nil {
		t.Fatal(err)
	}
	if err := applyNamedReadACL(backing); err != nil {
		t.Fatal(err)
	}
	ok, err := namedUserReadEffective(backing, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("named read is not effective on the backing")
	}
	cmd := exec.Command(qemu, "convert", "-O", "qcow2", overlay, out)
	if b, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("owner still opened the backing after chmod 000; named ACL for the owner uid is not the DAC path:\n%s", b)
	}

	// A9: chmod 0440 keeps the mask r (group bits). Owner read returns
	// through user::, which is what this process actually uses.
	if err := os.Chmod(backing, 0o440); err != nil {
		t.Fatal(err)
	}
	ok, err = namedUserReadEffective(backing, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("chmod 0440 lost the effective named read")
	}
	run("convert", "-O", "qcow2", overlay, out)
}
