package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadReturnsWrittenFacts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.facts.json")
	want := Facts{Result: ResultOK, Backstage: "1.2.3", InputsSHA256: "abc", EndState: &EndState{Snapshot: "ready", Image: "img"}}
	if err := Write(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Result != want.Result || got.Backstage != want.Backstage || got.InputsSHA256 != want.InputsSHA256 {
		t.Fatalf("read %+v", got)
	}
	if got.EndState == nil || got.EndState.Snapshot != "ready" || got.EndState.Image != "img" {
		t.Fatalf("end-state: %+v", got.EndState)
	}
}

func TestWriteReplacesTheSidecarWhole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "01-take.facts.json")
	if err := Write(path, Facts{Result: ResultOK, Backstage: "old", Domain: "keep-me"}); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, Facts{Result: ResultShort, Backstage: "new"}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Facts
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("incomplete replacement: %v\n%s", err, body)
	}
	if got.Result != ResultShort || got.Backstage != "new" {
		t.Fatalf("replaced facts: %+v", got)
	}
	if got.Domain != "" {
		t.Fatalf("old domain leaked into the replacement: %s", got.Domain)
	}
	if strings.Contains(string(body), "keep-me") {
		t.Fatalf("old file mixed into the new sidecar:\n%s", body)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".facts-") && strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestTimingsJSONNamesAndOmitEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "timed.facts.json")
	shutdown, boot := 1.234, 0.0
	bytes := int64(512)
	want := Facts{Result: ResultOK, Timings: &Timings{
		ShutdownSeconds: &shutdown,
		BootSeconds:     &boot,
		CaptureBytes:    &bytes,
		StagePhases:     map[string]float64{"up": 2.5, "omarchy": 0.1},
	}}
	if err := Write(path, want); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(body)
	for _, name := range []string{
		`"shutdown-seconds"`, `"boot-seconds"`, `"capture-bytes"`, `"stage-phases"`,
	} {
		if !strings.Contains(raw, name) {
			t.Fatalf("missing %s in %s", name, raw)
		}
	}
	for _, absent := range []string{
		`"capture-seconds"`, `"restore-stop-seconds"`, `"session-seconds"`,
		`"capture-mode"`, `"image-depth"`, `"capture-fallback"`,
	} {
		if strings.Contains(raw, absent) {
			t.Fatalf("omitted field present: %s\n%s", absent, raw)
		}
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Timings == nil || got.Timings.ShutdownSeconds == nil || *got.Timings.ShutdownSeconds != 1.234 {
		t.Fatalf("timings: %+v", got.Timings)
	}
	if got.Timings.BootSeconds == nil || *got.Timings.BootSeconds != 0 {
		t.Fatal("zero boot must be kept")
	}
	if !(Timings{}).Empty() {
		t.Fatal("empty timings")
	}
}

func TestPathSitsBesideTheClip(t *testing.T) {
	if got := Path("/rec/01-take.mp4"); got != "/rec/01-take.facts.json" {
		t.Fatalf("Path = %s", got)
	}
}
