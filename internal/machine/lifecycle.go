package machine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/This-Is-NPC/backstage/internal/guest"
	"github.com/This-Is-NPC/backstage/internal/recorder"
)

func copyFile(from, to string, mode os.FileMode) (err error) {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
		if err != nil {
			_ = os.Remove(to)
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return out.Close()
}

func (m *Manager) newCredentials(ctx context.Context, dir string) (Credentials, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Credentials{}, err
	}
	c := Credentials{Password: randomID(), Key: filepath.Join(dir, "id_ed25519")}
	_, err := m.run(ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", c.Key, "-C", "backstage")
	return c, err
}

// Create expects the caller to hold the stage and image-catalog locks.
func (m *Manager) Create(ctx context.Context, name string, spec Spec) (r *Record, err error) {
	if err = ValidateName(name); err != nil {
		return nil, err
	}
	if err = spec.Validate(); err != nil {
		return nil, err
	}
	if err = m.Store.Init(); err != nil {
		return nil, err
	}
	r, err = m.Store.Load(name)
	if err == nil {
		if err = m.Recover(ctx, r); err != nil {
			return r, err
		}
		if r.Spec != spec {
			return nil, errors.New("stage exists with a different configuration")
		}
		if r.Status == "ready" {
			return r, m.Owned(ctx, r)
		}
	} else {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		r = &Record{Schema: Schema, ID: randomID(), Name: name, URI: m.URI, Spec: spec, Status: "building", Created: time.Now().UTC(), Snapshots: map[string]string{}}
		r.Domain = fmt.Sprintf("backstage-%d-%s", os.Getuid(), name)
		if _, e := m.virsh(ctx, "domuuid", r.Domain); e == nil {
			return nil, errors.New("domain name already exists; refusing to adopt it")
		}
		if err = m.Store.Save(r); err != nil {
			return nil, err
		}
	}
	defer func() {
		if err != nil {
			r.Status = "failed"
			r.LastError = err.Error()
			_ = m.log(r, r.LastError)
			_ = m.Store.Save(r)
		}
	}()
	r.Status = "building"
	if err = m.ensurePool(ctx); err != nil {
		return r, err
	}
	if r.Source.SHA256 == "" {
		if err = m.phase(r, "resolve-iso"); err != nil {
			return r, err
		}
		r.Source, err = resolveISO(ctx, spec.Omarchy)
		if err != nil {
			return r, err
		}
		if err = m.Store.Save(r); err != nil {
			return r, err
		}
	}
	key := baseKey(spec, r.Source)
	var baseID string
	baseFile := filepath.Join(m.Store.Root, "bases", key+".json")
	if e := readJSON(baseFile, &baseID); e != nil && !os.IsNotExist(e) {
		return r, e
	}
	var base *Image
	if baseID != "" {
		base, err = m.Store.Image(baseID)
		if err != nil {
			return r, err
		}
	} else {
		base, err = m.installBase(ctx, r)
		if err != nil {
			return r, err
		}
		if err = atomicJSON(baseFile, base.ID); err != nil {
			return r, err
		}
	}
	if err = m.materialize(ctx, r, base); err != nil {
		return r, err
	}
	return r, nil
}

func (m *Manager) installBase(ctx context.Context, r *Record) (*Image, error) {
	if err := m.phase(r, "download-iso"); err != nil {
		return nil, err
	}
	iso := filepath.Join(m.Store.Cache, r.Source.SHA256+".iso")
	stopProgress := m.heartbeat(ctx, r.Name, "download-iso")
	downloadErr := downloadVerified(ctx, r.Source, iso)
	stopProgress()
	if err := downloadErr; err != nil {
		return nil, err
	}
	// QEMU cannot traverse the private cache. Stage a verified installation medium
	// in its storage pool without making the user's home directory public.
	media := m.diskPath(r.ID, "-install.iso")
	if sum, _ := checksum(media); sum != r.Source.SHA256 {
		_ = os.Remove(media)
		if err := copyFile(iso, media, 0o644); err != nil {
			return nil, err
		}
	}
	c, err := m.Store.Credentials(r.Name)
	if os.IsNotExist(err) {
		c, err = m.newCredentials(ctx, filepath.Join(m.Store.Dir(r.Name), "bootstrap-"+randomID()))
		if err == nil {
			err = m.Store.SaveCredentials(r.Name, c)
		}
	}
	if err != nil {
		return nil, err
	}
	seed, err := m.cidata(ctx, r, c)
	if err != nil {
		return nil, err
	}
	if r.Disk == "" {
		code, vars, err := m.firmware(ctx)
		if err != nil {
			return nil, err
		}
		r.Firmware = code
		r.Disk = m.diskPath(r.ID, "-install.qcow2")
		r.NVRAM = m.diskPath(r.ID, "-install.fd")
		if _, err := m.run(ctx, "qemu-img", "create", "-f", "qcow2", r.Disk, fmt.Sprint(r.Spec.Disk)); err != nil {
			return nil, err
		}
		if err := os.Chmod(r.Disk, 0o600); err != nil {
			return nil, err
		}
		if err := copyFile(vars, r.NVRAM, 0o600); err != nil {
			return nil, err
		}
		if err := m.Store.Save(r); err != nil {
			return nil, err
		}
	}
	if _, err := m.virsh(ctx, "domuuid", r.Domain); err == nil {
		if err := m.Owned(ctx, r); err != nil {
			return nil, err
		}
	} else if err := m.define(ctx, r, media, seed); err != nil {
		return nil, err
	}
	if err := m.phase(r, "install-omarchy"); err != nil {
		return nil, err
	}
	installCtx, cancel := context.WithTimeout(ctx, m.Timeout)
	defer cancel()
	g := guest.New(r.Domain, "omarchy", "omarchy", c.Key, r.URI)
	g.Context = installCtx
	g.Managed = true
	g.Password = c.Password
	// The live ISO also runs sshd, with a different host key. Bootstrap uses
	// the disposable guest policy until our installed account actually answers;
	// accepting/pinning keys on failed login attempts would pin the ISO's key.
	stopProgress = m.heartbeat(installCtx, r.Name, "install-omarchy")
	startErr := g.Start(m.Timeout)
	stopProgress()
	if err := startErr; err != nil {
		return nil, err
	}
	if _, err := g.AssertOmarchy(); err != nil {
		return nil, err
	}
	host, err := g.SSH("hostname")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(host) != r.Name {
		return nil, errors.New("installed guest hostname does not match its cidata configuration")
	}
	hostKey, err := g.SSH("cat /etc/ssh/ssh_host_ed25519_key.pub")
	if err != nil {
		return nil, err
	}
	g.KnownHosts = filepath.Join(m.Store.Dir(r.Name), "known_hosts")
	if err := atomicWrite(g.KnownHosts, []byte(r.Domain+" "+strings.TrimSpace(hostKey)+"\n"), 0o600); err != nil {
		return nil, err
	}
	if err := m.phase(r, "provision-tools"); err != nil {
		return nil, err
	}
	public, err := os.ReadFile(c.Key + ".pub")
	if err != nil {
		return nil, err
	}
	// Bootstrap sudo needs a password; pass it on stdin, never in argv or logs.
	script := provisionScript(r.Spec, string(public))
	args := []string{"-i", c.Key, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=" + g.KnownHosts, "-o", "HostKeyAlias=" + r.Domain, "omarchy@" + g.Address, "sudo -S -p '' bash -c " + quote(script)}
	if _, err := m.Runner.Run(ctx, strings.NewReader(c.Password+"\n"), "ssh", args...); err != nil {
		return nil, err
	}
	g.Admin = "backstage-admin"
	if err := m.validateGuest(ctx, g, r); err != nil {
		return nil, err
	}
	if err := m.phase(r, "save-base"); err != nil {
		return nil, err
	}
	if err := m.Stop(ctx, r, false); err != nil {
		return nil, err
	}
	if err := m.define(ctx, r, "", ""); err != nil {
		return nil, err
	}
	_ = os.Remove(seed)
	_ = os.Remove(media)
	_ = os.RemoveAll(filepath.Join(m.Store.Dir(r.Name), "cidata"))
	return m.capture(ctx, r)
}

func provisionScript(s Spec, public string) string {
	return fmt.Sprintf(`set -eu
pacman -Syu --noconfirm --needed ydotool wf-recorder qemu-guest-agent
id backstage-admin >/dev/null 2>&1 || useradd -m -s /bin/bash backstage-admin
install -d -m 700 -o backstage-admin -g backstage-admin /home/backstage-admin/.ssh
printf '%%s\n' %s >/home/backstage-admin/.ssh/authorized_keys
chown backstage-admin:backstage-admin /home/backstage-admin/.ssh/authorized_keys
chmod 600 /home/backstage-admin/.ssh/authorized_keys
printf 'backstage-admin ALL=(ALL) NOPASSWD: ALL\n' >/etc/sudoers.d/backstage-admin
chmod 440 /etc/sudoers.d/backstage-admin
visudo -cf /etc/sudoers.d/backstage-admin
install -d /etc/sddm.conf.d
printf '[Autologin]\nUser=omarchy\nSession=hyprland-uwsm.desktop\nRelogin=true\n' >/etc/sddm.conf.d/90-backstage.conf
systemctl start qemu-guest-agent
install -d -o omarchy -g omarchy /home/omarchy/.config/hypr
if test -f /home/omarchy/.config/hypr/hyprland.lua; then
printf 'hl.monitor({ output = "", mode = "%dx%d@60", position = "auto", scale = 1 })\nhl.env("GDK_SCALE", "1")\n' >/home/omarchy/.config/hypr/monitors.lua
chown omarchy:omarchy /home/omarchy/.config/hypr/monitors.lua
else
printf '# Backstage managed monitor\nmonitor = , %[2]dx%[3]d@60, auto, 1\nenv = GDK_SCALE,1\n' >/home/omarchy/.config/hypr/backstage-monitor.conf
touch /home/omarchy/.config/hypr/monitors.conf
grep -qF 'source = ~/.config/hypr/backstage-monitor.conf' /home/omarchy/.config/hypr/monitors.conf || printf '\nsource = ~/.config/hypr/backstage-monitor.conf\n' >>/home/omarchy/.config/hypr/monitors.conf
chown omarchy:omarchy /home/omarchy/.config/hypr/monitors.conf
chown omarchy:omarchy /home/omarchy/.config/hypr/backstage-monitor.conf
fi
`, quote(strings.TrimSpace(public)), s.Width, s.Height)
}

func (m *Manager) validateGuest(ctx context.Context, g *guest.Guest, r *Record) error {
	if err := m.phase(r, "validate-recording"); err != nil {
		return err
	}
	g.Context = ctx
	if err := g.Provision(); err != nil {
		return err
	}
	// First unattended boot may still be at the greeter. Restart the display
	// manager once to activate the autologin installed above.
	if _, err := g.InSession("hyprctl -j monitors"); err != nil {
		if _, err := g.Root("systemctl restart sddm"); err != nil {
			return err
		}
	}
	deadline, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, _ = g.InSession("hyprctl reload")
	for {
		out, err := g.InSession("hyprctl -j monitors")
		if err == nil {
			var monitors []struct {
				Width  int `json:"width"`
				Height int `json:"height"`
			}
			if json.Unmarshal([]byte(out), &monitors) == nil && len(monitors) == 1 && monitors[0].Width == r.Spec.Width && monitors[0].Height == r.Spec.Height {
				break
			}
		}
		if err := pause(deadline, 2*time.Second); err != nil {
			return fmt.Errorf("guest desktop never reached %dx%d: %w", r.Spec.Width, r.Spec.Height, err)
		}
	}
	if err := g.Prepare(); err != nil {
		return err
	}
	if err := g.OpenTerminal(); err != nil {
		return err
	}
	// Prove keyboard delivery rather than merely checking that a daemon exists.
	marker := "/tmp/backstage-keyboard-" + randomID()
	if err := g.Type("touch "+marker, 5*time.Millisecond); err != nil {
		return err
	}
	if err := g.Key("enter"); err != nil {
		return err
	}
	if err := pause(ctx, time.Second); err != nil {
		return err
	}
	if !g.Try("test -f " + marker) {
		return errors.New("virtual keyboard did not reach the guest terminal")
	}
	_, _ = g.SSH("rm -f " + marker)
	clip := filepath.Join(m.Store.Dir(r.Name), "validation.mp4")
	w := recorder.NewWF(g, 30)
	if err := w.Start(clip); err != nil {
		return err
	}
	waitErr := pause(ctx, 3*time.Second)
	_, err := w.Stop()
	if err != nil {
		return err
	}
	if waitErr != nil {
		return waitErr
	}
	out, err := m.run(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", clip)
	if err != nil {
		return err
	}
	var duration float64
	if _, err := fmt.Sscan(out, &duration); err != nil || duration < 1 {
		return errors.New("validation recording is too short or invalid")
	}
	return g.ClearTheDesktop()
}

func (m *Manager) capture(ctx context.Context, r *Record) (*Image, error) {
	state, err := m.State(ctx, r)
	if err != nil {
		return nil, err
	}
	if state != "shut off" {
		return nil, errors.New("snapshot requires a stopped stage")
	}
	c, err := m.Store.Credentials(r.Name)
	if err != nil {
		return nil, err
	}
	id := randomID()
	i := &Image{Schema: Schema, ID: id, Disk: m.diskPath(id, "-image.qcow2"), NVRAM: m.diskPath(id, "-image.fd"), Firmware: r.Firmware, Spec: r.Spec, Source: r.Source, Created: time.Now().UTC()}
	if _, err := m.run(ctx, "qemu-img", "convert", "-O", "qcow2", r.Disk, i.Disk); err != nil {
		return nil, err
	}
	if err := os.Chmod(i.Disk, 0o600); err != nil {
		return nil, err
	}
	if err := copyFile(r.NVRAM, i.NVRAM, 0o600); err != nil {
		return nil, err
	}
	keyDir := filepath.Join(m.Store.Root, "images", id)
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return nil, err
	}
	i.Credentials = Credentials{Password: c.Password, Key: filepath.Join(keyDir, "id_ed25519")}
	for _, suffix := range []string{"", ".pub"} {
		if err := copyFile(c.Key+suffix, i.Credentials.Key+suffix, 0o600); err != nil {
			return nil, err
		}
	}
	if err := atomicJSON(filepath.Join(m.Store.Root, "images", id+".json"), i); err != nil {
		return nil, err
	}
	return i, nil
}

func (m *Manager) Snapshot(ctx context.Context, r *Record, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if _, ok := r.Snapshots[name]; ok {
		return errors.New("snapshot already exists; choose another name")
	}
	if r.Status != "ready" {
		return errors.New("only ready stages can be snapshotted")
	}
	if err := m.Stop(ctx, r, false); err != nil {
		return err
	}
	i, err := m.capture(ctx, r)
	if err != nil {
		return err
	}
	r.Snapshots[name] = i.ID
	return m.Store.Save(r)
}

func (m *Manager) Restore(ctx context.Context, r *Record, name string) error {
	id, ok := r.Snapshots[name]
	if !ok {
		return fmt.Errorf("snapshot %q not found", name)
	}
	i, err := m.Store.Image(id)
	if err != nil {
		return err
	}
	if err := m.Stop(ctx, r, false); err != nil {
		return err
	}
	return m.activate(ctx, r, i, false)
}

func (m *Manager) materialize(ctx context.Context, r *Record, i *Image) error {
	if err := m.phase(r, "personalize-clone"); err != nil {
		return err
	}
	r.Status = "building"
	if _, err := m.virsh(ctx, "domuuid", r.Domain); err == nil {
		if err := m.Stop(ctx, r, false); err != nil {
			return err
		}
	}
	if err := m.activate(ctx, r, i, true); err != nil {
		return err
	}
	g, err := m.Start(ctx, r)
	if err != nil {
		return err
	}
	if err := m.validateGuest(ctx, g, r); err != nil {
		return err
	}
	if err := m.phase(r, "save-initial"); err != nil {
		return err
	}
	if err := m.Stop(ctx, r, false); err != nil {
		return err
	}
	initial, err := m.capture(ctx, r)
	if err != nil {
		return err
	}
	r.Snapshots["initial"] = initial.ID
	r.Status = "ready"
	r.Phase = "ready"
	r.LastError = ""
	return m.Store.Save(r)
}

func (m *Manager) activate(ctx context.Context, r *Record, i *Image, personalize bool) error {
	old := *r
	oldCredentials, _ := m.Store.Credentials(r.Name)
	id := r.ID + "-" + randomID()
	nextDisk, nextVars := m.diskPath(id, ".qcow2"), m.diskPath(id, ".fd")
	if _, err := m.run(ctx, "qemu-img", "create", "-f", "qcow2", "-F", "qcow2", "-b", i.Disk, nextDisk); err != nil {
		return err
	}
	if err := os.Chmod(nextDisk, 0o600); err != nil {
		return err
	}
	if err := copyFile(i.NVRAM, nextVars, 0o600); err != nil {
		return err
	}
	c := i.Credentials
	if personalize {
		var err error
		c, err = m.newCredentials(ctx, filepath.Join(m.Store.Dir(r.Name), "access-"+randomID()))
		if err != nil {
			return err
		}
		if err := m.customize(ctx, r, nextDisk, c); err != nil {
			return err
		}
	}
	r.Disk = nextDisk
	r.NVRAM = nextVars
	r.Firmware = i.Firmware
	r.Source = i.Source
	r.Source.Image = i.ID
	r.Continuity = nil
	// Keep the previous files until both libvirt and the registry point at the
	// replacement. A journal lets a subsequent operation recover a crash here.
	journal := filepath.Join(m.Store.Dir(r.Name), "activate.json")
	if err := atomicJSON(journal, activation{Old: old, OldCredentials: oldCredentials, Next: *r, Credentials: c}); err != nil {
		return err
	}
	if err := m.define(ctx, r, "", ""); err != nil {
		*r = old
		return err
	}
	if err := m.Store.SaveCredentials(r.Name, c); err != nil {
		return err
	}
	if err := m.Store.Save(r); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(m.Store.Dir(r.Name), "known_hosts"))
	if err := os.Remove(journal); err != nil {
		return err
	}
	return nil
}

type activation struct {
	Old            Record
	OldCredentials Credentials
	Next           Record
	Credentials    Credentials
}

// Recover rolls back an interrupted activation while the domain is stopped.
func (m *Manager) Recover(ctx context.Context, r *Record) error {
	path := filepath.Join(m.Store.Dir(r.Name), "activate.json")
	var a activation
	if err := readJSON(path, &a); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := m.Owned(ctx, r); err != nil {
		return err
	}
	state, err := m.State(ctx, r)
	if err != nil {
		return err
	}
	if state != "shut off" {
		return errors.New("interrupted activation: stop the stage before recovery")
	}
	// Complete the already-prepared replacement, never discard the old files.
	if err := m.define(ctx, &a.Next, "", ""); err != nil {
		return err
	}
	if err := m.Store.SaveCredentials(r.Name, a.Credentials); err != nil {
		return err
	}
	if err := m.Store.Save(&a.Next); err != nil {
		return err
	}
	*r = a.Next
	_ = os.Remove(filepath.Join(m.Store.Dir(r.Name), "known_hosts"))
	return os.Remove(path)
}

func (m *Manager) customize(ctx context.Context, r *Record, disk string, c Credentials) error {
	public, err := os.ReadFile(c.Key + ".pub")
	if err != nil {
		return err
	}
	script := fmt.Sprintf(`set -eu
printf '%%s\n' %s >/etc/hostname
printf '%%s\n' %s >/etc/machine-id
rm -f /var/lib/dbus/machine-id /etc/ssh/ssh_host_*
ln -s /etc/machine-id /var/lib/dbus/machine-id
ssh-keygen -A
rm -f /var/lib/systemd/random-seed
rm -f /var/lib/NetworkManager/*lease* /var/lib/NetworkManager/secret_key
printf '%%s\n' %s | chpasswd
passwd -l root
rm -f /home/omarchy/.ssh/authorized_keys
printf '%%s\n' %s >/home/backstage-admin/.ssh/authorized_keys
chown backstage-admin:backstage-admin /home/backstage-admin/.ssh/authorized_keys
chmod 600 /home/backstage-admin/.ssh/authorized_keys
`, quote(r.Name), quote(randomID()), quote("omarchy:"+c.Password), quote(strings.TrimSpace(string(public))))
	path := filepath.Join(m.Store.Dir(r.Name), "personalize.sh")
	if err := atomicWrite(path, []byte(script), 0o600); err != nil {
		return err
	}
	defer os.Remove(path)
	// Mount our known GPT/Btrfs layout explicitly. Automatic inspection sees
	// Omarchy's Btrfs snapshots as multiple operating systems and refuses -i.
	// Network is disabled by default.
	// Upload the protected script instead of placing generated passwords in argv.
	remote := "/tmp/backstage-personalize-" + randomID() + ".sh"
	commands := "run\nmount-options subvol=@ /dev/sda2 /\nmount-options subvol=@home /dev/sda2 /home\nmount-options subvol=@log /dev/sda2 /var/log\nmount-options subvol=@pkg /dev/sda2 /var/cache/pacman/pkg\nmount /dev/sda1 /boot\n" +
		"upload " + strconv.Quote(path) + " " + remote + "\nsh " + strconv.Quote("bash "+remote) + "\nrm " + remote + "\n"
	_, err = m.Runner.Run(ctx, strings.NewReader(commands), "guestfish", "--rw", "-a", disk)
	return err
}

func (m *Manager) Clone(ctx context.Context, source *Record, name, snapshot string) (r *Record, err error) {
	if err = ValidateName(name); err != nil {
		return nil, err
	}
	id, ok := source.Snapshots[snapshot]
	if !ok {
		return nil, fmt.Errorf("snapshot %s not found", snapshot)
	}
	i, err := m.Store.Image(id)
	if err != nil {
		return nil, err
	}
	r, err = m.Store.Load(name)
	if err == nil {
		if r.Status == "ready" || r.Source.Image != i.ID {
			return nil, errors.New("target stage already exists; only a failed clone of this snapshot can be retried")
		}
		if err = m.Recover(ctx, r); err != nil {
			return r, err
		}
	} else {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		r = &Record{Schema: Schema, ID: randomID(), Name: name, Domain: fmt.Sprintf("backstage-%d-%s", os.Getuid(), name), URI: m.URI, Spec: i.Spec, Source: i.Source, Status: "building", Created: time.Now().UTC(), Snapshots: map[string]string{}}
		r.Source.Image = i.ID
		if _, e := m.virsh(ctx, "domuuid", r.Domain); e == nil {
			return nil, errors.New("target domain already exists")
		}
	}
	if err = m.Store.Save(r); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			r.Status = "failed"
			r.LastError = err.Error()
			_ = m.log(r, r.LastError)
			_ = m.Store.Save(r)
		}
	}()
	err = m.materialize(ctx, r, i)
	return r, err
}

func (m *Manager) Delete(ctx context.Context, r *Record) error {
	if _, err := m.virsh(ctx, "domuuid", r.Domain); err == nil {
		if err := m.Stop(ctx, r, false); err != nil {
			return err
		}
		if _, err := m.virsh(ctx, "undefine", r.Domain, "--nvram"); err != nil {
			return err
		}
	} else {
		// A connection failure must never be interpreted as an absent domain.
		if _, err := m.virsh(ctx, "list", "--all", "--uuid"); err != nil {
			return err
		}
	}
	files, err := filepath.Glob(filepath.Join(m.Store.Storage, r.ID+"-*"))
	if err != nil {
		return err
	}
	for _, path := range files {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.RemoveAll(m.Store.Dir(r.Name)); err != nil {
		return err
	}
	return m.Collect()
}

// Collect removes only catalogued images with no stage or base-cache reference.
// Called while holding image-catalog, after the stage registry mutation commits.
func (m *Manager) Collect() error {
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
		i, err := m.Store.Image(id)
		if err != nil {
			return err
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
