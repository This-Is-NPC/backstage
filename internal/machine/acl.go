package machine

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/user"
	"strconv"

	"golang.org/x/sys/unix"
)

// POSIX ACL v2 on system.posix_acl_access. Tags and perms match
// include/uapi/linux/posix_acl.h.
const (
	posixACLAccess  = "system.posix_acl_access"
	aclXattrVersion = 2
	aclTagUserObj   = 0x01
	aclTagUser      = 0x02
	aclTagGroupObj  = 0x04
	aclTagMask      = 0x10
	aclTagOther     = 0x20
	aclPermRead     = 0x04
	aclPermWrite    = 0x02
	aclPermExecute  = 0x01
	aclUndefinedID  = 0xffffffff
	aclHeaderSize   = 4
	aclEntrySize    = 8
)

// protectCapturedDisk is the test hook; production uses applyNamedReadACL.
var protectCapturedDisk = applyNamedReadACL

// applyNamedReadACL writes user:<process uid>:r and an r mask onto path.
//
// Call this after the last chmod. On btrfs (and other Linux ACL
// implementations) a later chmod recomputes the mask from the group
// mode bits and clears the named entry's effective rights:
//
//	chmod 0600; ACL user:uid:r → mask r
//	chmod 0600 again           → mask empty, user:uid:r #effective:---
//
// A9 must chmod 0440 (group bits become the mask) before the ACL, or
// not chmod after it. Do not chmod 0600 after this function.
func applyNamedReadACL(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot apply a named read ACL on %s: %w (the pool filesystem must support POSIX ACLs)", path, err)
	}
	uid := os.Getuid()
	if err := unix.Setxattr(path, posixACLAccess, encodeAccessACL(info.Mode().Perm(), uid, aclPermRead), 0); err != nil {
		return fmt.Errorf("cannot apply a named read ACL on %s: %w (the pool filesystem must support POSIX ACLs)", path, err)
	}
	ok, err := namedUserReadEffective(path, uid)
	if err != nil {
		return fmt.Errorf("cannot apply a named read ACL on %s: %w (the pool filesystem must support POSIX ACLs)", path, err)
	}
	if !ok {
		return fmt.Errorf("named read ACL on %s is not effective (the pool filesystem must support POSIX ACLs; do not chmod after the ACL)", path)
	}
	return nil
}

func encodeAccessACL(mode os.FileMode, namedUID int, namedPerm uint16) []byte {
	buf := make([]byte, aclHeaderSize+5*aclEntrySize)
	binary.LittleEndian.PutUint32(buf[0:4], aclXattrVersion)
	putACLEntry(buf, 0, aclTagUserObj, modeACLPerm(mode, 6), aclUndefinedID)
	putACLEntry(buf, 1, aclTagUser, namedPerm, uint32(namedUID))
	putACLEntry(buf, 2, aclTagGroupObj, modeACLPerm(mode, 3), aclUndefinedID)
	putACLEntry(buf, 3, aclTagMask, aclPermRead, aclUndefinedID)
	putACLEntry(buf, 4, aclTagOther, modeACLPerm(mode, 0), aclUndefinedID)
	return buf
}

func putACLEntry(buf []byte, i int, tag, perm uint16, id uint32) {
	off := aclHeaderSize + i*aclEntrySize
	binary.LittleEndian.PutUint16(buf[off:off+2], tag)
	binary.LittleEndian.PutUint16(buf[off+2:off+4], perm)
	binary.LittleEndian.PutUint32(buf[off+4:off+8], id)
}

func modeACLPerm(mode os.FileMode, shift int) uint16 {
	bits := (uint32(mode) >> shift) & 7
	var p uint16
	if bits&4 != 0 {
		p |= aclPermRead
	}
	if bits&2 != 0 {
		p |= aclPermWrite
	}
	if bits&1 != 0 {
		p |= aclPermExecute
	}
	return p
}

type aclEntry struct {
	tag, perm uint16
	id        uint32
}

func parseAccessACL(b []byte) (uint32, []aclEntry, error) {
	if len(b) < aclHeaderSize || (len(b)-aclHeaderSize)%aclEntrySize != 0 {
		return 0, nil, fmt.Errorf("invalid POSIX ACL xattr")
	}
	ver := binary.LittleEndian.Uint32(b[:4])
	if ver != aclXattrVersion {
		return 0, nil, fmt.Errorf("unsupported POSIX ACL version %d", ver)
	}
	n := (len(b) - aclHeaderSize) / aclEntrySize
	ents := make([]aclEntry, n)
	for i := range ents {
		off := aclHeaderSize + i*aclEntrySize
		ents[i] = aclEntry{
			tag:  binary.LittleEndian.Uint16(b[off : off+2]),
			perm: binary.LittleEndian.Uint16(b[off+2 : off+4]),
			id:   binary.LittleEndian.Uint32(b[off+4 : off+8]),
		}
	}
	return ver, ents, nil
}

func readAccessACL(path string) ([]aclEntry, error) {
	n, err := unix.Getxattr(path, posixACLAccess, nil)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	if _, err := unix.Getxattr(path, posixACLAccess, buf); err != nil {
		return nil, err
	}
	_, ents, err := parseAccessACL(buf)
	return ents, err
}

func namedUserReadEffective(path string, uid int) (bool, error) {
	ents, err := readAccessACL(path)
	if err != nil {
		return false, err
	}
	var named, mask *aclEntry
	var haveMask bool
	for i := range ents {
		switch ents[i].tag {
		case aclTagUser:
			if ents[i].id == uint32(uid) {
				named = &ents[i]
			}
		case aclTagMask:
			mask = &ents[i]
			haveMask = true
		}
	}
	if named == nil {
		return false, nil
	}
	perm := named.perm
	if haveMask {
		perm &= mask.perm
	}
	return perm&aclPermRead != 0, nil
}

func currentUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return strconv.Itoa(os.Getuid())
}

func remediateImageACL(path string) string {
	return "sudo setfacl -m u:" + currentUsername() + ":r,m::r " + path
}

func (m *Manager) poolACLCheck() Check {
	dir := m.Store.Storage
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return Check{"pool-acl", true, "pool not created yet at " + dir + "; rerun doctor after the first stage"}
	}
	f, err := os.CreateTemp(dir, ".acl-probe-*")
	if err != nil {
		return Check{"pool-acl", false, "cannot create a probe file in " + dir + ": " + err.Error() + " (the pool filesystem must support POSIX ACLs)"}
	}
	path := f.Name()
	_ = f.Close()
	defer func() { _ = os.Remove(path) }()
	if err := os.Chmod(path, 0o600); err != nil {
		return Check{"pool-acl", false, err.Error()}
	}
	if err := applyNamedReadACL(path); err != nil {
		return Check{"pool-acl", false, err.Error()}
	}
	return Check{"pool-acl", true, dir + " accepts a named POSIX read ACL"}
}

func (m *Manager) catalogACLChecks() []Check {
	images, err := m.Store.listImages()
	if err != nil {
		return []Check{{"image-acl", false, err.Error()}}
	}
	if len(images) == 0 {
		return []Check{{"image-acl", true, "no catalog images"}}
	}
	var checks []Check
	for _, img := range images {
		if img.Disk == "" {
			continue
		}
		if _, err := os.Stat(img.Disk); os.IsNotExist(err) {
			continue
		}
		if unix.Access(img.Disk, unix.R_OK) == nil {
			continue
		}
		id := img.ID
		if len(id) > 8 {
			id = id[:8]
		}
		checks = append(checks, Check{"image-" + id, false, remediateImageACL(img.Disk)})
	}
	if len(checks) == 0 {
		return []Check{{"image-acl", true, "catalog images are readable"}}
	}
	return checks
}
