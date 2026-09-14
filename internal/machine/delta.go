package machine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	CaptureModeDelta    = "delta"
	CaptureModeComplete = "complete"

	fallbackBackingPath      = "backing-path-mismatch"
	fallbackBackingChain     = "backing-chain-mismatch"
	fallbackMissingAncestor  = "missing-ancestor"
	fallbackRepeatedAncestor = "repeated-ancestor"
	fallbackCatalogParent    = "catalog-parent-mismatch"
	fallbackIncompleteChain  = "incomplete-chain"
	fallbackDepthLimit       = "depth-limit"
	fallbackCachedBase       = "cached-base"
	fallbackBaseCacheUnread  = "base-cache-unreadable"
)

type qemuImgInfo struct {
	Filename            string `json:"filename"`
	BackingFilename     string `json:"backing-filename"`
	FullBackingFilename string `json:"full-backing-filename"`
}

func (i qemuImgInfo) backing() string {
	if i.FullBackingFilename != "" {
		return i.FullBackingFilename
	}
	return i.BackingFilename
}

type deltaDecision struct {
	ok         bool
	reason     string
	unreadable error
	parent     *Image
	newDepth   int
}

type capturedImage struct {
	Image    *Image
	Mode     string
	Depth    int
	Fallback string
}

func parseBackingChain(out string) ([]qemuImgInfo, error) {
	body := []byte(strings.TrimSpace(out))
	var chain []qemuImgInfo
	if err := json.Unmarshal(body, &chain); err == nil {
		if len(chain) == 0 {
			return nil, fmt.Errorf("empty backing chain")
		}
		return chain, nil
	}
	var wrap struct {
		Images []qemuImgInfo `json:"images"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, err
	}
	if len(wrap.Images) == 0 {
		return nil, fmt.Errorf("empty backing chain")
	}
	return wrap.Images, nil
}

func (m *Manager) catalogChain(id string) ([]string, string) {
	var ids []string
	seen := map[string]bool{}
	for cur := id; cur != ""; {
		if seen[cur] {
			return nil, fallbackRepeatedAncestor
		}
		seen[cur] = true
		img, err := m.Store.Image(cur)
		if err != nil {
			return nil, fallbackMissingAncestor
		}
		ids = append(ids, cur)
		if img.Parent == "" {
			return ids, ""
		}
		cur = img.Parent
	}
	return nil, fallbackIncompleteChain
}

func (m *Manager) decideDelta(ctx context.Context, r *Record, maxDepth int) deltaDecision {
	if r.Source.Image == "" {
		return deltaDecision{reason: fallbackMissingAncestor}
	}
	if _, err := os.Stat(r.Disk); err != nil {
		return deltaDecision{unreadable: fmt.Errorf("active disk: %w", err)}
	}
	parent, err := m.Store.Image(r.Source.Image)
	if err != nil {
		return deltaDecision{reason: fallbackMissingAncestor}
	}
	out, err := m.run(ctx, "qemu-img", "info", "--backing-chain", "--output=json", r.Disk)
	if err != nil {
		return deltaDecision{unreadable: fmt.Errorf("cannot read backing chain: %w", err)}
	}
	chain, err := parseBackingChain(out)
	if err != nil {
		return deltaDecision{reason: fallbackBackingChain}
	}
	if chain[0].backing() != parent.Disk {
		return deltaDecision{reason: fallbackBackingPath}
	}
	catalog, reason := m.catalogChain(parent.ID)
	if reason != "" {
		return deltaDecision{reason: reason}
	}
	bases, err := m.cachedBaseIDs()
	if err != nil {
		return deltaDecision{reason: fallbackBaseCacheUnread}
	}
	for _, id := range catalog {
		if bases[id] {
			return deltaDecision{reason: fallbackCachedBase}
		}
	}
	last, err := m.Store.Image(catalog[len(catalog)-1])
	if err != nil || last.Parent != "" {
		return deltaDecision{reason: fallbackIncompleteChain}
	}
	if chain[len(chain)-1].backing() != "" {
		return deltaDecision{reason: fallbackIncompleteChain}
	}
	if len(chain)-1 != len(catalog) {
		return deltaDecision{reason: fallbackCatalogParent}
	}
	seenReport := map[string]bool{}
	for i, id := range catalog {
		img, err := m.Store.Image(id)
		if err != nil {
			return deltaDecision{reason: fallbackMissingAncestor}
		}
		reported := chain[i+1]
		if seenReport[reported.Filename] {
			return deltaDecision{reason: fallbackRepeatedAncestor}
		}
		seenReport[reported.Filename] = true
		if reported.Filename != img.Disk {
			return deltaDecision{reason: fallbackBackingChain}
		}
		if i+1 < len(catalog) {
			next, err := m.Store.Image(catalog[i+1])
			if err != nil {
				return deltaDecision{reason: fallbackMissingAncestor}
			}
			if reported.backing() != next.Disk {
				return deltaDecision{reason: fallbackCatalogParent}
			}
		}
	}
	newDepth := len(catalog)
	if newDepth > maxDepth {
		return deltaDecision{reason: fallbackDepthLimit}
	}
	return deltaDecision{ok: true, parent: parent, newDepth: newDepth}
}

var writeImageJSON = atomicJSON

func (m *Manager) capture(ctx context.Context, r *Record) (*Image, error) {
	got, err := m.captureImage(ctx, r, false, 0)
	if err != nil {
		return nil, err
	}
	return got.Image, nil
}

func (m *Manager) captureImage(ctx context.Context, r *Record, allowDelta bool, maxDepth int) (*capturedImage, error) {
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mode := CaptureModeComplete
	fallback := ""
	depth := 0
	var parent *Image
	if allowDelta && maxDepth == 0 {
		fallback = fallbackDepthLimit
		m.printCaptureFallback(r, fallback)
	} else if allowDelta {
		decision := m.decideDelta(ctx, r, maxDepth)
		if decision.unreadable != nil {
			return nil, decision.unreadable
		}
		if decision.ok {
			mode = CaptureModeDelta
			parent = decision.parent
			depth = decision.newDepth
		} else {
			fallback = decision.reason
			m.printCaptureFallback(r, fallback)
		}
	}
	id := randomID()
	i := &Image{Schema: ImageSchema, ID: id, Disk: m.diskPath(id, "-image.qcow2"), NVRAM: m.diskPath(id, "-image.fd"), Firmware: r.Firmware, Spec: r.Spec, Source: r.Source, Created: time.Now().UTC()}
	var created []string
	fail := func(err error) (*capturedImage, error) {
		for n := len(created) - 1; n >= 0; n-- {
			_ = os.RemoveAll(created[n])
		}
		return nil, err
	}
	if mode == CaptureModeDelta {
		if _, err := m.run(ctx, "qemu-img", "convert", "-O", "qcow2", "-B", parent.Disk, "-F", "qcow2", r.Disk, i.Disk); err != nil {
			created = append(created, i.Disk)
			return fail(err)
		}
	} else if _, err := m.run(ctx, "qemu-img", "convert", "-O", "qcow2", r.Disk, i.Disk); err != nil {
		created = append(created, i.Disk)
		return fail(err)
	}
	created = append(created, i.Disk)
	if err := os.Chmod(i.Disk, 0o440); err != nil {
		return fail(err)
	}
	if err := protectCapturedDisk(i.Disk); err != nil {
		return fail(err)
	}
	if err := copyFile(r.NVRAM, i.NVRAM, 0o600); err != nil {
		created = append(created, i.NVRAM)
		return fail(err)
	}
	created = append(created, i.NVRAM)
	keyDir := filepath.Join(m.Store.Root, "images", id)
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return fail(err)
	}
	created = append(created, keyDir)
	i.Credentials = Credentials{Password: c.Password, Key: filepath.Join(keyDir, "id_ed25519")}
	for _, suffix := range []string{"", ".pub"} {
		if err := copyFile(c.Key+suffix, i.Credentials.Key+suffix, 0o600); err != nil {
			return fail(err)
		}
	}
	if mode == CaptureModeDelta {
		promoted := *parent
		promoted.Schema = ImageSchemaV2
		if err := writeImageJSON(m.Store.imageJSON(parent.ID), promoted); err != nil {
			return fail(err)
		}
		i.Schema = ImageSchemaV2
		i.Parent = parent.ID
	}
	jsonPath := m.Store.imageJSON(id)
	if err := writeImageJSON(jsonPath, i); err != nil {
		return fail(err)
	}
	created = append(created, jsonPath)
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return &capturedImage{Image: i, Mode: mode, Depth: depth, Fallback: fallback}, nil
}
