package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestPlayAdoptAndRehearseReplaceStateFlags(t *testing.T) {
	if playCmd().Flags().Lookup("adopt") == nil {
		t.Fatal("play needs --adopt")
	}
	if rehearseCmd().Flags().Lookup("replace-state") == nil {
		t.Fatal("rehearse needs --replace-state")
	}
	if playCmd().Flags().Lookup("replace-state") != nil {
		t.Fatal("play must not take --replace-state")
	}
	if rehearseCmd().Flags().Lookup("adopt") != nil {
		t.Fatal("rehearse must not take --adopt")
	}
}

func TestConfirmSnapshotName(t *testing.T) {
	var errOut bytes.Buffer
	if err := confirmSnapshotName(strings.NewReader("ready\n"), &errOut, "ready"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "Type its name") {
		t.Fatalf("prompt: %s", errOut.String())
	}
	if err := confirmSnapshotName(strings.NewReader("other\n"), &errOut, "ready"); err == nil {
		t.Fatal("accepted the wrong name")
	}
}
