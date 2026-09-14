package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
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
	if playCmd().Flags().Lookup("with-deps") == nil || rehearseCmd().Flags().Lookup("with-deps") == nil {
		t.Fatal("play and rehearse need --with-deps")
	}
}

func TestConfirmSnapshotName(t *testing.T) {
	var errOut bytes.Buffer
	if err := confirmSnapshotName(context.Background(), strings.NewReader("ready\n"), &errOut, "ready"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "Type its name") {
		t.Fatalf("prompt: %s", errOut.String())
	}
	if err := confirmSnapshotName(context.Background(), strings.NewReader("other\n"), &errOut, "ready"); err == nil {
		t.Fatal("accepted the wrong name")
	}
}

func TestConfirmSnapshotNameRespectsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &blockReader{started: make(chan struct{}), block: make(chan struct{})}
	defer close(r.block)
	errCh := make(chan error, 1)
	go func() {
		errCh <- confirmSnapshotName(ctx, r, io.Discard, "ready")
	}()
	select {
	case <-r.started:
	case <-time.After(2 * time.Second):
		t.Fatal("reader never started")
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v", err)
		}
		if ExitStatus(err) != 130 {
			t.Fatalf("exit %d, want 130", ExitStatus(err))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("confirm ignored cancel")
	}
}

type blockReader struct {
	started chan struct{}
	block   chan struct{}
	once    sync.Once
}

func (r *blockReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.block
	return 0, io.EOF
}
