package machine

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/This-Is-NPC/backstage/internal/guest"
)

type Runner interface {
	Run(context.Context, io.Reader, string, ...string) (string, error)
}
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, input io.Reader, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = input
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LIBGUESTFS_BACKEND=direct")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

type Manager struct {
	Store   *Store
	Runner  Runner
	URI     string
	Timeout time.Duration
	Output  io.Writer
}

func New() (*Manager, error) {
	s, err := DefaultStore()
	if err != nil {
		return nil, err
	}
	return &Manager{s, ExecRunner{}, "qemu:///system", 40 * time.Minute, os.Stderr}, nil
}

func (m *Manager) run(ctx context.Context, name string, args ...string) (string, error) {
	return m.Runner.Run(ctx, nil, name, args...)
}
func (m *Manager) virsh(ctx context.Context, args ...string) (string, error) {
	return m.run(ctx, "virsh", append([]string{"-c", m.URI}, args...)...)
}
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func (m *Manager) pool() string { return fmt.Sprintf("backstage-%d", os.Getuid()) }

func (m *Manager) phase(r *Record, phase string) error {
	r.Phase = phase
	r.LastError = ""
	if m.Output != nil {
		fmt.Fprintf(m.Output, ">> stage %s: %s\n", r.Name, phase)
	}
	if err := m.log(r, phase); err != nil {
		return err
	}
	return m.Store.Save(r)
}

func (m *Manager) log(r *Record, message string) error {
	f, err := os.OpenFile(filepath.Join(m.Store.Dir(r.Name), "provision.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339), message)
	return err
}

// heartbeat keeps long downloads/installations observable without claiming an
// installer percentage we cannot measure. stop joins the writer goroutine.
func (m *Manager) heartbeat(ctx context.Context, name, phase string) func() {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		began := time.Now()
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-tick.C:
				if m.Output != nil {
					fmt.Fprintf(m.Output, ">> stage %s: %s (%s elapsed)\n", name, phase, time.Since(began).Round(time.Second))
				}
			}
		}
	}()
	return func() { close(done); wg.Wait() }
}

type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func (m *Manager) Doctor(ctx context.Context) []Check {
	checks := []Check{{"platform", runtime.GOOS == "linux" && runtime.GOARCH == "amd64", "Linux x86_64 with KVM is required"}}
	for _, name := range []string{"virsh", "qemu-system-x86_64", "qemu-img", "guestfish", "xorriso", "ssh", "scp", "ssh-keygen", "openssl", "ffprobe"} {
		path, err := exec.LookPath(name)
		if err != nil {
			path = "install " + name + " (guestfish is supplied by libguestfs)"
		}
		checks = append(checks, Check{name, err == nil, path})
	}
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err == nil {
		_ = f.Close()
	}
	checks = append(checks, Check{"kvm", err == nil, "/dev/kvm must be readable and writable"})
	out, err := m.virsh(ctx, "version")
	checks = append(checks, Check{"libvirt", err == nil, strings.TrimSpace(out)})
	_, _, err = m.firmware(ctx)
	checks = append(checks, Check{"uefi", err == nil, "install edk2/OVMF firmware supported by libvirt"})
	video, err := m.videoModel(ctx)
	checks = append(checks, Check{"video", err == nil, "supported software display: " + video + " (VirtIO or Bochs required)"})
	out, err = m.virsh(ctx, "net-info", "default")
	checks = append(checks, Check{"network", err == nil && strings.Contains(out, "Active:         yes"), "libvirt network 'default' must exist and be active (virsh -c qemu:///system net-start default)"})
	// A missing pool is created by create; an existing directory must be usable.
	info, err := os.Stat(m.Store.Storage)
	detail := "a dedicated pool will be created at " + m.Store.Storage
	ok := os.IsNotExist(err)
	if err == nil {
		ok = info.IsDir() && syscall.Access(m.Store.Storage, 6) == nil
		detail = m.Store.Storage + " must permit your user to read/write and QEMU to traverse"
	}
	checks = append(checks, Check{"storage", ok, detail})
	return checks
}

func (m *Manager) ensurePool(ctx context.Context) error {
	if _, err := m.virsh(ctx, "pool-info", m.pool()); err != nil {
		body := fmt.Sprintf(`<pool type="dir"><name>%s</name><target><path>%s</path><permissions><mode>0711</mode><owner>%d</owner><group>%d</group></permissions></target></pool>`, m.pool(), xmlText(m.Store.Storage), os.Getuid(), os.Getgid())
		path := filepath.Join(m.Store.Root, "pool.xml")
		if err := atomicWrite(path, []byte(body), 0o600); err != nil {
			return err
		}
		if _, err := m.virsh(ctx, "pool-define", path); err != nil {
			return err
		}
		if _, err := m.virsh(ctx, "pool-build", m.pool()); err != nil {
			return err
		}
	}
	// Verify the pool name hasn't been reused for unrelated storage.
	out, err := m.virsh(ctx, "pool-dumpxml", m.pool())
	if err != nil {
		return err
	}
	var pool struct {
		Target struct {
			Path string `xml:"path"`
		} `xml:"target"`
	}
	if err := xml.Unmarshal([]byte(out), &pool); err != nil {
		return err
	}
	if filepath.Clean(pool.Target.Path) != filepath.Clean(m.Store.Storage) {
		return errors.New("backstage pool points to unexpected storage")
	}
	info, err := m.virsh(ctx, "pool-info", m.pool())
	if err != nil {
		return err
	}
	if !strings.Contains(info, "running") {
		if _, err := m.virsh(ctx, "pool-start", m.pool()); err != nil {
			return err
		}
	}
	if err := syscall.Access(m.Store.Storage, 7); err != nil {
		return fmt.Errorf("pool directory %s must be writable by uid %d: %w", m.Store.Storage, os.Getuid(), err)
	}
	return nil
}

func (m *Manager) firmware(ctx context.Context) (string, string, error) {
	out, err := m.virsh(ctx, "domcapabilities", "--arch", "x86_64", "--machine", "q35", "--virttype", "kvm")
	if err != nil {
		return "", "", err
	}
	var caps struct {
		OS struct {
			Loader struct {
				Values []string `xml:"value"`
			} `xml:"loader"`
		} `xml:"os"`
	}
	if err := xml.Unmarshal([]byte(out), &caps); err != nil {
		return "", "", err
	}
	// Prefer the non-Secure-Boot variant; discover its matching variables file.
	for _, code := range caps.OS.Loader.Values {
		if strings.Contains(strings.ToLower(code), "secboot") || strings.Contains(code, ".ms.") {
			continue
		}
		vars := strings.Replace(code, "CODE", "VARS", 1)
		if vars == code {
			continue
		}
		if _, err := os.Stat(vars); err == nil {
			return code, vars, nil
		}
	}
	return "", "", errors.New("no matching OVMF CODE/VARS firmware found in libvirt capabilities")
}

func (m *Manager) diskPath(id, suffix string) string {
	return filepath.Join(m.Store.Storage, id+suffix)
}

func (m *Manager) videoModel(ctx context.Context) (string, error) {
	out, err := m.virsh(ctx, "domcapabilities", "--arch", "x86_64", "--machine", "q35", "--virttype", "kvm")
	if err != nil {
		return "", err
	}
	var caps struct {
		Devices struct {
			Video struct {
				Enums []struct {
					Name   string   `xml:"name,attr"`
					Values []string `xml:"value"`
				} `xml:"enum"`
			} `xml:"video"`
		} `xml:"devices"`
	}
	if err := xml.Unmarshal([]byte(out), &caps); err != nil {
		return "", err
	}
	for _, candidate := range []string{"virtio", "bochs"} {
		for _, e := range caps.Devices.Video.Enums {
			if e.Name == "modelType" {
				for _, v := range e.Values {
					if v == candidate {
						return v, nil
					}
				}
			}
		}
	}
	return "", errors.New("QEMU needs a VirtIO or Bochs display device")
}

func (m *Manager) define(ctx context.Context, r *Record, iso, seed string) error {
	if r.Video == "" {
		var err error
		r.Video, err = m.videoModel(ctx)
		if err != nil {
			return err
		}
	}
	if id, err := m.virsh(ctx, "domuuid", r.Domain); err == nil {
		if strings.TrimSpace(id) != uuid(r.ID) {
			return errors.New("refusing to redefine a foreign domain")
		}
	} else if _, err := m.virsh(ctx, "list", "--all", "--uuid"); err != nil {
		return err
	}
	cds := ""
	for index, path := range []string{iso, seed} {
		if path != "" {
			cds += fmt.Sprintf(`<disk type="file" device="cdrom"><driver name="qemu" type="raw"/><source file="%s"/><target dev="sd%c" bus="sata"/><readonly/></disk>`, xmlText(path), 'a'+index)
		}
	}
	mac := "52:54:" + r.ID[:2] + ":" + r.ID[2:4] + ":" + r.ID[4:6] + ":" + r.ID[6:8]
	body := fmt.Sprintf(`<domain type="kvm"><name>%s</name><uuid>%s</uuid><memory unit="bytes">%d</memory><vcpu>%d</vcpu><os><type arch="x86_64" machine="q35">hvm</type><loader readonly="yes" type="pflash">%s</loader><nvram>%s</nvram><boot dev="hd"/><boot dev="cdrom"/></os><features><acpi/><apic/></features><cpu mode="host-passthrough"/><clock offset="utc"/><on_reboot>restart</on_reboot><devices><disk type="file" device="disk"><driver name="qemu" type="qcow2"/><source file="%s"/><target dev="vda" bus="virtio"/></disk>%s<interface type="network"><mac address="%s"/><source network="default"/><model type="virtio"/></interface><video><model type="%s" heads="1" primary="yes"/></video><graphics type="vnc" autoport="yes" listen="127.0.0.1"/><input type="tablet" bus="usb"/><serial type="pty"/><console type="pty"/><channel type="unix"><target type="virtio" name="org.qemu.guest_agent.0"/></channel></devices></domain>`, xmlText(r.Domain), uuid(r.ID), r.Spec.Memory, r.Spec.CPUs, xmlText(r.Firmware), xmlText(r.NVRAM), xmlText(r.Disk), cds, mac, xmlText(r.Video))
	path := filepath.Join(m.Store.Dir(r.Name), "domain.xml")
	if err := atomicWrite(path, []byte(body), 0o600); err != nil {
		return err
	}
	if _, err := m.virsh(ctx, "define", path); err != nil {
		return err
	}
	return nil
}

func uuid(id string) string {
	return id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
}

// Owned checks libvirt identity before every destructive domain operation.
func (m *Manager) Owned(ctx context.Context, r *Record) error {
	out, err := m.virsh(ctx, "domuuid", r.Domain)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != uuid(r.ID) {
		return errors.New("refusing to operate on a domain not owned by this stage")
	}
	return nil
}

func (m *Manager) State(ctx context.Context, r *Record) (string, error) {
	if err := m.Owned(ctx, r); err != nil {
		return "", err
	}
	out, err := m.virsh(ctx, "domstate", r.Domain)
	return strings.TrimSpace(out), err
}

func (m *Manager) Stop(ctx context.Context, r *Record, force bool) error {
	state, err := m.State(ctx, r)
	if err != nil {
		return err
	}
	r.Continuity = nil
	if err := m.Store.Save(r); err != nil {
		return err
	}
	if state == "shut off" {
		return nil
	}
	verb := "shutdown"
	if force {
		verb = "destroy"
	}
	if _, err := m.virsh(ctx, verb, r.Domain); err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		state, err = m.State(wait, r)
		if err != nil {
			return err
		}
		if state == "shut off" {
			return nil
		}
		if err := pause(wait, time.Second); err != nil {
			return fmt.Errorf("waiting for shutdown (use stop --force explicitly if necessary): %w", err)
		}
	}
}

func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (m *Manager) Guest(r *Record) (*guest.Guest, error) {
	c, err := m.Store.Credentials(r.Name)
	if err != nil {
		return nil, err
	}
	g := guest.New(r.Domain, "omarchy", "backstage-admin", c.Key, r.URI)
	g.Password = c.Password
	g.Managed = true
	g.KnownHosts = filepath.Join(m.Store.Dir(r.Name), "known_hosts")
	g.StageName = r.Name
	g.Origin = r.Source.Image
	g.ISOVersion = r.Source.Version
	g.ISOChecksum = r.Source.SHA256
	g.Recipe = r.Source.Recipe
	return g, nil
}

func (m *Manager) Start(ctx context.Context, r *Record) (*guest.Guest, error) {
	if r.Status != "ready" && r.Status != "building" {
		return nil, fmt.Errorf("stage %s is %s: %s", r.Name, r.Status, r.LastError)
	}
	if err := m.Owned(ctx, r); err != nil {
		return nil, err
	}
	g, err := m.Guest(r)
	if err != nil {
		return nil, err
	}
	g.Context = ctx
	if err := g.Start(5 * time.Minute); err != nil {
		return nil, err
	}
	return g, nil
}

func ParseSize(s string) (uint64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := uint64(1)
	for _, suffix := range []struct {
		text string
		n    uint64
	}{{"GIB", 1 << 30}, {"MIB", 1 << 20}, {"G", 1 << 30}, {"M", 1 << 20}} {
		if strings.HasSuffix(s, suffix.text) {
			mult = suffix.n
			s = strings.TrimSuffix(s, suffix.text)
			break
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n == 0 || n > (^uint64(0))/mult {
		return 0, fmt.Errorf("invalid size: %s", s)
	}
	return n * mult, nil
}
