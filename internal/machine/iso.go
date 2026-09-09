package machine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

var releasePattern = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-\d+)?$`)
var isoLink = regexp.MustCompile(`https://iso\.omarchy\.org/omarchy-(\d+\.\d+\.\d+(?:-\d+)?)\.iso`)

func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 40 * time.Minute}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		return nil, fmt.Errorf("download %s: HTTP %d", url, res.StatusCode)
	}
	return res, nil
}

func resolveISO(ctx context.Context, version string) (Source, error) {
	if version == "latest" {
		res, err := get(ctx, "https://omarchy.org/")
		if err != nil {
			return Source{}, err
		}
		body, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		_ = res.Body.Close()
		if err != nil {
			return Source{}, err
		}
		match := isoLink.FindSubmatch(body)
		if match == nil {
			return Source{}, errors.New("official Omarchy page has no recognized stable ISO link; specify --omarchy VERSION")
		}
		version = string(match[1])
	}
	if !releasePattern.MatchString(version) {
		return Source{}, errors.New("--omarchy must be latest or a stable version, e.g. 4.0.3")
	}
	url := "https://iso.omarchy.org/omarchy-" + version + ".iso"
	res, err := get(ctx, url+".sha256")
	if err != nil {
		return Source{}, err
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 4096))
	_ = res.Body.Close()
	if err != nil {
		return Source{}, err
	}
	fields := strings.Fields(string(body))
	if len(fields) < 1 || !regexp.MustCompile(`^[a-fA-F0-9]{64}$`).MatchString(fields[0]) {
		return Source{}, errors.New("invalid official SHA-256 response")
	}
	return Source{Version: version, URL: url, SHA256: strings.ToLower(fields[0]), Recipe: Recipe}, nil
}

func checksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// downloadVerified never exposes an incomplete or unchecked ISO as a cache hit.
func downloadVerified(ctx context.Context, source Source, path string) error {
	if sum, err := checksum(path); err == nil && sum == source.SHA256 {
		return nil
	}
	res, err := get(ctx, source.URL)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.ContentLength > 0 {
		var fs syscall.Statfs_t
		if err := syscall.Statfs(filepath.Dir(path), &fs); err != nil {
			return err
		}
		if uint64(res.ContentLength) > fs.Bavail*uint64(fs.Bsize) {
			return errors.New("not enough free space for the Omarchy ISO")
		}
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".download-*")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), res.Body); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != source.SHA256 {
		return errors.New("omarchy ISO SHA-256 mismatch; download discarded")
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(f.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func baseKey(spec Spec, source Source) string {
	// CPU and memory affect the VM, not the installed image.
	spec.CPUs = 0
	spec.Memory = 0
	spec.Omarchy = source.Version
	source.Image = ""
	b, _ := json.Marshal(struct {
		Spec   Spec
		Source Source
	}{spec, source})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// installerConfig is the Omarchy configurator's full-disk contract, based on
// omacom/omarchy-iso test/integration.d/base-test.sh (quattro). The installer
// consumes these files directly; cidata does not run generic cloud-init YAML.
func installerConfig(spec Spec, hostname string) map[string]any {
	const mib = uint64(1 << 20)
	position := func(n uint64) map[string]any {
		return map[string]any{"sector_size": map[string]any{"unit": "B", "value": 512}, "unit": "B", "value": n}
	}
	partition := func(start, size uint64, fs string) map[string]any {
		return map[string]any{"btrfs": []any{}, "dev_path": nil, "flags": []string{}, "fs_type": fs, "mount_options": []string{}, "mountpoint": nil, "obj_id": uuid(randomID()), "size": position(size), "start": position(start), "status": "create", "type": "primary"}
	}
	boot := partition(mib, 2<<30, "fat32")
	boot["flags"] = []string{"boot", "esp"}
	boot["mountpoint"] = "/boot"
	root := partition((2<<30)+mib, spec.Disk-(2<<30)-2*mib, "btrfs")
	root["mount_options"] = []string{"compress=zstd"}
	root["btrfs"] = []map[string]string{{"mountpoint": "/", "name": "@"}, {"mountpoint": "/home", "name": "@home"}, {"mountpoint": "/var/log", "name": "@log"}, {"mountpoint": "/var/cache/pacman/pkg", "name": "@pkg"}}
	return map[string]any{
		"app_config": nil, "archinstall-language": "English", "auth_config": map[string]any{}, "audio_config": map[string]string{"audio": "pipewire"},
		"bootloader_config": map[string]any{"bootloader": "Limine", "uki": false, "removable": false}, "custom_commands": []string{},
		"omarchy_install": map[string]any{"mode": "full_disk", "defer_provisioning": false, "target_mount": "/mnt", "boot": map[string]any{"esp_mount": "/boot", "esp_path": "/EFI/limine", "efi_binary": "limine_x64.efi", "enable_fallback": true}, "storage": map[string]string{"kernel": "linux"}},
		"disk_config":     map[string]any{"config_type": "default_layout", "device_modifications": []any{map[string]any{"device": "/dev/vda", "partitions": []any{boot, root}, "wipe": true}}},
		"hostname":        hostname, "kernels": []string{"linux"}, "network_config": map[string]string{"type": "iso"}, "ntp": true, "parallel_downloads": 8, "script": nil, "services": []string{}, "swap": true, "timezone": spec.Timezone,
		"locale_config": map[string]string{"kb_layout": spec.Keyboard, "sys_enc": "UTF-8", "sys_lang": spec.Locale},
		"mirror_config": map[string]any{"custom_repositories": []string{}, "custom_servers": []any{map[string]string{"url": "https://mirror.omarchy.org/$repo/os/$arch"}, map[string]string{"url": "https://geo.mirror.pkgbuild.com/$repo/os/$arch"}}, "mirror_regions": map[string]any{}, "optional_repositories": []string{}},
		"packages":      []string{"base-devel", "git", "omarchy-keyring", "omarchy-settings", "omarchy"}, "profile_config": map[string]any{"gfx_driver": nil, "greeter": nil, "profile": map[string]any{}}, "version": "3.0.9",
	}
}

func (m *Manager) cidata(ctx context.Context, r *Record, c Credentials) (string, error) {
	seed := m.diskPath(r.ID, "-cidata.iso")
	if info, err := os.Stat(seed); err == nil && info.Size() > 0 {
		return seed, nil
	}
	dir := filepath.Join(m.Store.Dir(r.Name), "cidata")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	hash, err := m.Runner.Run(ctx, strings.NewReader(c.Password+"\n"), "openssl", "passwd", "-6", "-stdin")
	if err != nil {
		return "", err
	}
	credentials := map[string]any{"root_enc_password": strings.TrimSpace(hash), "users": []any{map[string]any{"enc_password": strings.TrimSpace(hash), "groups": []string{}, "sudo": true, "username": "omarchy"}}}
	if err := atomicJSON(filepath.Join(dir, "user_configuration.json"), installerConfig(r.Spec, r.Name)); err != nil {
		return "", err
	}
	if err := atomicJSON(filepath.Join(dir, "user_credentials.json"), credentials); err != nil {
		return "", err
	}
	key, err := os.ReadFile(c.Key + ".pub")
	if err != nil {
		return "", err
	}
	if err := atomicWrite(filepath.Join(dir, "authorized_keys"), key, 0o600); err != nil {
		return "", err
	}
	if err := atomicWrite(filepath.Join(dir, "user_encrypt_installation.txt"), []byte("false\n"), 0o600); err != nil {
		return "", err
	}
	tmp := seed + ".building"
	_, err = m.run(ctx, "xorriso", "-as", "mkisofs", "-quiet", "-o", tmp, "-V", "cidata", "-J", "-R", dir)
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return "", err
	}
	return seed, os.Rename(tmp, seed)
}
