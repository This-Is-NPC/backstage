// Package machine owns persistent, user-scoped libvirt recording stages.
// It is independent of scene files and of the recording engine.
package machine

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"syscall"
	"time"
)

const Schema = 1
const Recipe = "1"

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

func ValidateName(name string) error {
	if !namePattern.MatchString(name) || name == "image-catalog" {
		return fmt.Errorf("invalid stage/snapshot name %q: use 1–48 lowercase letters, digits or hyphens, starting with a letter", name)
	}
	return nil
}

type Spec struct {
	Omarchy  string `json:"omarchy"`
	CPUs     int    `json:"cpus"`
	Memory   uint64 `json:"memory"`
	Disk     uint64 `json:"disk"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Keyboard string `json:"keyboard"`
	Locale   string `json:"locale"`
	Timezone string `json:"timezone"`
}

func DefaultSpec() Spec {
	return Spec{"latest", 4, 8 << 30, 40 << 30, 1920, 1080, "us", "en_US.UTF-8", "UTC"}
}

func (s Spec) Validate() error {
	if s.CPUs < 1 || s.CPUs > 256 || s.Memory < 2<<30 || s.Disk < 20<<30 || s.Width < 640 || s.Height < 480 || s.Width > 7680 || s.Height > 4320 {
		return errors.New("stage needs 1–256 CPUs, at least 2 GiB RAM, 20 GiB disk and a resolution between 640×480 and 7680×4320")
	}
	for _, value := range []string{s.Keyboard, s.Locale, s.Timezone} {
		if !regexp.MustCompile(`^[A-Za-z0-9_./+@-]+$`).MatchString(value) || len(value) > 100 || value == "" {
			return fmt.Errorf("invalid guest setting %q", value)
		}
	}
	return nil
}

type Source struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Recipe  string `json:"recipe"`
	Image   string `json:"image,omitempty"`
}

type Continuity struct {
	Project   string `json:"project"`
	Scene     string `json:"scene"`
	Recording bool   `json:"recording"`
	Session   string `json:"session"`
}

type Record struct {
	Schema     int               `json:"schema"`
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Domain     string            `json:"domain"`
	URI        string            `json:"uri"`
	Disk       string            `json:"disk"`
	NVRAM      string            `json:"nvram"`
	Firmware   string            `json:"firmware"`
	Video      string            `json:"video"`
	Spec       Spec              `json:"spec"`
	Source     Source            `json:"source"`
	Status     string            `json:"status"`
	Phase      string            `json:"phase"`
	LastError  string            `json:"last-error,omitempty"`
	Created    time.Time         `json:"created"`
	Snapshots  map[string]string `json:"snapshots"`
	Continuity *Continuity       `json:"continuity,omitempty"`
}

type Credentials struct {
	Password string `json:"password"`
	Key      string `json:"key"`
}

// Image is a standalone immutable disk plus matching firmware and credentials.
// Active disks depend only on these objects, never on another stage's disk.
type Image struct {
	Schema      int         `json:"schema"`
	ID          string      `json:"id"`
	Disk        string      `json:"disk"`
	NVRAM       string      `json:"nvram"`
	Firmware    string      `json:"firmware"`
	Spec        Spec        `json:"spec"`
	Source      Source      `json:"source"`
	Credentials Credentials `json:"credentials"`
	Created     time.Time   `json:"created"`
}

type Store struct{ Root, Cache, Storage string }

func DefaultStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	return &Store{filepath.Join(data, "backstage", "machines"), filepath.Join(cache, "backstage", "iso"), fmt.Sprintf("/var/lib/libvirt/images/backstage-%d", os.Getuid())}, nil
}

func (s *Store) Init() error {
	for _, dir := range []string{s.Root, filepath.Join(s.Root, "stages"), filepath.Join(s.Root, "images"), filepath.Join(s.Root, "locks"), filepath.Join(s.Root, "bases"), s.Cache} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func atomicJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'), 0o600)
}

func atomicWrite(path string, b []byte, mode os.FileMode) (err error) {
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func (s *Store) Dir(name string) string { return filepath.Join(s.Root, "stages", name) }

func (s *Store) Load(name string) (*Record, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	var r Record
	if err := readJSON(filepath.Join(s.Dir(name), "stage.json"), &r); err != nil {
		return nil, fmt.Errorf("stage %s: %w", name, err)
	}
	if r.Schema != Schema || r.Name != name || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(r.ID) {
		return nil, errors.New("unsupported or invalid stage record")
	}
	return &r, nil
}

func (s *Store) Save(r *Record) error {
	if err := ValidateName(r.Name); err != nil {
		return err
	}
	return atomicJSON(filepath.Join(s.Dir(r.Name), "stage.json"), r)
}

func (s *Store) Credentials(name string) (Credentials, error) {
	var c Credentials
	if err := ValidateName(name); err != nil {
		return c, err
	}
	err := readJSON(filepath.Join(s.Dir(name), "credentials.json"), &c)
	return c, err
}

func (s *Store) SaveCredentials(name string, c Credentials) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	return atomicJSON(filepath.Join(s.Dir(name), "credentials.json"), c)
}

func (s *Store) Image(id string) (*Image, error) {
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(id) {
		return nil, errors.New("invalid image id")
	}
	var i Image
	if err := readJSON(filepath.Join(s.Root, "images", id+".json"), &i); err != nil {
		return nil, err
	}
	if i.Schema != Schema || i.ID != id {
		return nil, errors.New("unsupported image record")
	}
	return &i, nil
}

func (s *Store) List() ([]*Record, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, "stages"))
	if os.IsNotExist(err) {
		return []*Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := []*Record{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		r, err := s.Load(e.Name())
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, nil
}

// LockMany uses a fixed order and nonblocking flock. Process death releases it.
func (s *Store) LockMany(names ...string) (func(), error) {
	if err := s.Init(); err != nil {
		return nil, err
	}
	sort.Strings(names)
	files := []*os.File{}
	release := func() {
		for _, f := range files {
			_ = f.Close()
		}
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		if err := ValidateName(name); err != nil && name != "image-catalog" {
			release()
			return nil, err
		}
		f, err := os.OpenFile(filepath.Join(s.Root, "locks", name+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			release()
			return nil, err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			_ = f.Close()
			release()
			return nil, fmt.Errorf("stage/resource %s is busy", name)
		}
		files = append(files, f)
	}
	return release, nil
}
