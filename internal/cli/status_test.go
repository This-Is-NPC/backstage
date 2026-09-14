package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

)

func TestStatusCommandExposesJSON(t *testing.T) {
	cmd := statusCmd()
	if cmd.Flags().Lookup("json") == nil {
		t.Fatal("status needs --json")
	}
	if cmd.Use != "status [DIR]" {
		t.Fatalf("use: %s", cmd.Use)
	}
	if !cmd.SilenceUsage || !cmd.SilenceErrors {
		t.Fatal("status must silence cobra usage and Error: reprints")
	}
}

func TestStatusConflictUsesCommandWriter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}},
		"vms": {"laptop": {"stage": "demo"}}
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "one.json"), `{"name":"one","layout":"solo","vm":"laptop","vm-start":{"mode":"clean"},"vm-end":{"snapshot":"ready"},"steps":[{"action":"wait","delay-after":0.05}]}`)
	writeFile(t, filepath.Join(dir, "scenes", "two.json"), `{"name":"two","layout":"solo","vm":"laptop","vm-start":{"mode":"clean"},"vm-end":{"snapshot":"ready"},"steps":[{"action":"wait","delay-after":0.05}]}`)

	cmd := statusCmd()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{dir})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("duplicate producers must fail")
	}
	if !strings.Contains(err.Error(), "two scenes") && !strings.Contains(out.String(), "two scenes") {
		t.Fatalf("conflict not on the command writer:\nout=%q\nerr=%v\nstderr=%q", out.String(), err, errBuf.String())
	}
	if !strings.Contains(out.String(), "two scenes") {
		t.Fatalf("conflict must be written to the command writer, got %q", out.String())
	}
	if strings.Contains(errBuf.String(), "Error:") || strings.Contains(errBuf.String(), "Usage:") {
		t.Fatalf("cobra reprinted the error: %q", errBuf.String())
	}
	if strings.Contains(errBuf.String(), "two scenes") || strings.Contains(errBuf.String(), "cycle") {
		t.Fatalf("conflict text also on stderr: %q", errBuf.String())
	}
	if strings.Contains(out.String(), "Usage:") {
		t.Fatalf("usage leaked onto stdout: %q", out.String())
	}
}

func TestStatusWarningsKeepExitZero(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}}
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "host.json"), `{"name":"host","layout":"solo","steps":[{"action":"wait","delay-after":0.05}]}`)
	writeFile(t, filepath.Join(dir, "vendor", "x", "backstage.json"), "{")
	cmd := statusCmd()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("warnings must keep exit 0: %v\nstdout=%q\nstderr=%q", err, out.String(), errBuf.String())
	}
	if !strings.Contains(out.String(), "Warnings:") {
		t.Fatalf("expected warnings on stdout: %q", out.String())
	}
}

func TestStatusDuplicateSceneNameExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}}
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "c.json"), `{"name":"c","layout":"solo","steps":[{"action":"wait","delay-after":0.05}]}`)
	writeFile(t, filepath.Join(dir, "scenes", "c-copy.json"), `{"name":"c","layout":"solo","steps":[{"action":"wait","delay-after":0.05}]}`)
	cmd := statusCmd()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{dir})
	if err := cmd.Execute(); err == nil {
		t.Fatal("duplicate scene name must exit non-zero")
	}
	if !strings.Contains(out.String(), "duplicate scene name c") || !strings.Contains(out.String(), "c-copy.json") {
		t.Fatalf("report missing duplicate name: %q", out.String())
	}
}

func TestStatusSceneErrorExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "backstage.json"), `{
		"layouts": {"solo": {"panes": [{"name": "t", "cmd": "bash"}]}}
	}`)
	writeFile(t, filepath.Join(dir, "scenes", "host.json"), `{"name":"host","layout":"solo","steps":[{"action":"wait","delay-after":0.05}]}`)
	if err := os.MkdirAll(filepath.Join(dir, "recordings", ".takes", "host"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recordings", ".takes", "host", "lock"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recordings", "host.take.json"), []byte(`{"version":9}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := statusCmd()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{dir})
	if err := cmd.Execute(); err == nil {
		t.Fatal("scene error must exit non-zero")
	}
	if !strings.Contains(out.String(), "error") {
		t.Fatalf("report missing error row: %q", out.String())
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
