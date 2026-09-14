package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestPathSitsBesideTheClip(t *testing.T) {
	if got := Path("/rec/01-take.mp4"); got != "/rec/01-take.facts.json" {
		t.Fatalf("Path = %s", got)
	}
}
