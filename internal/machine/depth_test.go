package machine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageDepthLimitDefault(t *testing.T) {
	m := testManager(t)
	n, origin, err := m.imageDepthLimit()
	if err != nil || n != DefaultMaxImageDepth || origin != depthOriginDefault {
		t.Fatalf("default: %d %s %v", n, origin, err)
	}
}

func TestImageDepthLimitEnv(t *testing.T) {
	m := testManager(t)
	t.Setenv("BACKSTAGE_IMAGE_DEPTH", "0")
	n, origin, err := m.imageDepthLimit()
	if err != nil || n != 0 || origin != depthOriginEnv {
		t.Fatalf("env: %d %s %v", n, origin, err)
	}
}

func TestImageDepthLimitSettings(t *testing.T) {
	m := testManager(t)
	if err := os.WriteFile(filepath.Join(m.Store.Root, "settings.json"), []byte(`{"max-image-depth": 2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	n, origin, err := m.imageDepthLimit()
	if err != nil || n != 2 || origin != depthOriginSettings {
		t.Fatalf("settings: %d %s %v", n, origin, err)
	}
}

func TestImageDepthLimitInvalid(t *testing.T) {
	m := testManager(t)
	t.Setenv("BACKSTAGE_IMAGE_DEPTH", "-1")
	if _, origin, err := m.imageDepthLimit(); err == nil || origin != depthOriginEnv || !strings.Contains(err.Error(), "BACKSTAGE_IMAGE_DEPTH") {
		t.Fatalf("neg env: %v %s", err, origin)
	}
	t.Setenv("BACKSTAGE_IMAGE_DEPTH", "nope")
	if _, _, err := m.imageDepthLimit(); err == nil || !strings.Contains(err.Error(), "BACKSTAGE_IMAGE_DEPTH") {
		t.Fatalf("bad env: %v", err)
	}
	t.Setenv("BACKSTAGE_IMAGE_DEPTH", "")
	if err := os.WriteFile(filepath.Join(m.Store.Root, "settings.json"), []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, origin, err := m.imageDepthLimit(); err == nil || origin != depthOriginSettings || !strings.Contains(err.Error(), "settings.json") {
		t.Fatalf("broken json: %v %s", err, origin)
	}
	if err := os.WriteFile(filepath.Join(m.Store.Root, "settings.json"), []byte(`{"max-image-depth":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.imageDepthLimit(); err == nil || !strings.Contains(err.Error(), "integer") {
		t.Fatalf("string depth: %v", err)
	}
	if err := os.WriteFile(filepath.Join(m.Store.Root, "settings.json"), []byte(`{"max-image-depth":-2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.imageDepthLimit(); err == nil || !strings.Contains(err.Error(), "integer") {
		t.Fatalf("neg settings: %v", err)
	}
}

func TestImageDepthUnknownSettingsKey(t *testing.T) {
	m := testManager(t)
	if err := os.WriteFile(filepath.Join(m.Store.Root, "settings.json"), []byte(`{"max_image_depth": 2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, origin, err := m.imageDepthLimit(); err == nil || origin != depthOriginSettings || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown key: %v %s", err, origin)
	}
	found := false
	for _, c := range m.Doctor(context.Background()) {
		if c.Name != "image-depth" {
			continue
		}
		found = true
		if c.OK || !strings.Contains(c.Detail, "unknown field") {
			t.Fatalf("doctor: %+v", c)
		}
	}
	if !found {
		t.Fatal("doctor omitted image-depth")
	}
	calls := 0
	r := testRecord("demo")
	r.Status = "ready"
	if err := m.Store.Save(r); err != nil {
		t.Fatal(err)
	}
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		calls++
		t.Fatalf("must not touch the VM: %s %v", bin, args)
		return "", nil
	})
	if err := m.Snapshot(context.Background(), r, "hand"); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("snapshot: %v", err)
	}
	if calls != 0 {
		t.Fatal("stopped the guest")
	}
}

func TestImageDepthLimitFieldOverride(t *testing.T) {
	m := testManager(t)
	n := 7
	m.MaxImageDepth = &n
	got, origin, err := m.imageDepthLimit()
	if err != nil || got != 7 || origin != depthOriginSet {
		t.Fatalf("set: %d %s %v", got, origin, err)
	}
	neg := -1
	m.MaxImageDepth = &neg
	if _, _, err := m.imageDepthLimit(); err == nil {
		t.Fatal("accepted negative override")
	}
}

func TestDoctorReportsImageDepth(t *testing.T) {
	m := testManager(t)
	found := false
	for _, c := range m.Doctor(context.Background()) {
		if c.Name != "image-depth" {
			continue
		}
		found = true
		if !c.OK || !strings.Contains(c.Detail, "8 (default)") {
			t.Fatalf("doctor: %+v", c)
		}
	}
	if !found {
		t.Fatal("doctor omitted image-depth")
	}
	t.Setenv("BACKSTAGE_IMAGE_DEPTH", "0")
	for _, c := range m.Doctor(context.Background()) {
		if c.Name == "image-depth" && (!c.OK || !strings.Contains(c.Detail, "0 (env)")) {
			t.Fatalf("env doctor: %+v", c)
		}
	}
	t.Setenv("BACKSTAGE_IMAGE_DEPTH", "")
	if err := os.WriteFile(filepath.Join(m.Store.Root, "settings.json"), []byte(`{"max-image-depth":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range m.Doctor(context.Background()) {
		if c.Name == "image-depth" && (!c.OK || !strings.Contains(c.Detail, "1 (settings)")) {
			t.Fatalf("settings doctor: %+v", c)
		}
	}
	if err := os.WriteFile(filepath.Join(m.Store.Root, "settings.json"), []byte(`{"max-image-depth":-1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range m.Doctor(context.Background()) {
		if c.Name == "image-depth" && (c.OK || !strings.Contains(c.Detail, "integer")) {
			t.Fatalf("invalid doctor: %+v", c)
		}
	}
}

func TestSnapshotInvalidDepthDoesNotStop(t *testing.T) {
	m, r := captureReady(t, "demo")
	t.Setenv("BACKSTAGE_IMAGE_DEPTH", "-1")
	calls := 0
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		calls++
		t.Fatalf("must not touch the VM: %s %v", bin, args)
		return "", nil
	})
	if err := m.Snapshot(context.Background(), r, "hand"); err == nil || !strings.Contains(err.Error(), "BACKSTAGE_IMAGE_DEPTH") {
		t.Fatalf("err: %v", err)
	}
	if calls != 0 {
		t.Fatal("stopped the guest")
	}
	if r.Snapshots["hand"] != "" {
		t.Fatal("mapping changed")
	}
}

func TestReplaceSnapshotInvalidDepthDoesNotStop(t *testing.T) {
	m, r := captureReady(t, "demo")
	t.Setenv("BACKSTAGE_IMAGE_DEPTH", "nope")
	calls := 0
	m.Runner = runnerFunc(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		calls++
		t.Fatalf("must not touch the VM: %s %v", bin, args)
		return "", nil
	})
	if _, err := m.ReplaceSnapshot(context.Background(), r, "ready", testOrigin("/p", "s"), false); err == nil {
		t.Fatal("expected invalid depth")
	}
	if calls != 0 {
		t.Fatal("stopped the guest")
	}
}
