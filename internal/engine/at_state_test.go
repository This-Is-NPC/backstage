package engine

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/This-Is-NPC/backstage/internal/guest"
	"github.com/This-Is-NPC/backstage/internal/machine"
	"github.com/This-Is-NPC/backstage/internal/scene"
)

type engineRunner func(context.Context, io.Reader, string, ...string) (string, error)

func (f engineRunner) Run(ctx context.Context, in io.Reader, name string, args ...string) (string, error) {
	return f(ctx, in, name, args...)
}

func liveManaged(t *testing.T) (*machine.Manager, string) {
	t.Helper()
	root := t.TempDir()
	store := &machine.Store{Root: filepath.Join(root, "reg"), Cache: filepath.Join(root, "cache"), Storage: filepath.Join(root, "storage")}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.Storage, 0o700); err != nil {
		t.Fatal(err)
	}
	m := &machine.Manager{Store: store, URI: "qemu:///system", Timeout: time.Second, Output: io.Discard}
	r := &machine.Record{
		Schema:    machine.Schema,
		ID:        strings.Repeat("ab", 16),
		Name:      "demo",
		Domain:    "backstage-test-demo",
		URI:       m.URI,
		Spec:      machine.DefaultSpec(),
		Status:    "ready",
		Video:     "bochs",
		Firmware:  "firmware",
		Snapshots: map[string]string{},
	}
	r.Disk = filepath.Join(store.Storage, r.ID+".qcow2")
	r.NVRAM = filepath.Join(store.Storage, r.ID+".fd")
	for _, p := range []string{r.Disk, r.NVRAM} {
		if err := os.WriteFile(p, []byte("disk"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	initial := strings.Repeat("11", 16)
	r.Snapshots["initial"] = initial
	r.Source.Image = initial
	if err := writeEngineImage(store, initial); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(store.Dir(r.Name), "id_ed25519")
	if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key+".pub", []byte("pub"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(r); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCredentials(r.Name, machine.Credentials{Password: "pw", Key: key}); err != nil {
		t.Fatal(err)
	}
	m.Runner = engineRunner(func(_ context.Context, _ io.Reader, bin string, args ...string) (string, error) {
		if bin == "qemu-img" {
			if len(args) > 0 && args[0] == "info" {
				return `[{"filename":"` + r.Disk + `"}]`, nil
			}
			return "", os.WriteFile(args[len(args)-1], []byte("overlay"), 0o600)
		}
		if len(args) > 2 {
			switch args[2] {
			case "domuuid":
				return r.ID[:8] + "-" + r.ID[8:12] + "-" + r.ID[12:16] + "-" + r.ID[16:20] + "-" + r.ID[20:], nil
			case "domstate":
				return "shut off", nil
			}
		}
		return "", nil
	})
	return m, initial
}

func writeEngineImage(store *machine.Store, id string) error {
	keyDir := filepath.Join(store.Root, "images", id)
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return err
	}
	key := filepath.Join(keyDir, "id_ed25519")
	if err := os.WriteFile(key, []byte("key"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(key+".pub", []byte("pub"), 0o600); err != nil {
		return err
	}
	i := machine.Image{
		Schema: machine.ImageSchema, ID: id,
		Disk: filepath.Join(store.Storage, id+"-image.qcow2"), NVRAM: filepath.Join(store.Storage, id+"-image.fd"),
		Firmware:    "firmware",
		Credentials: machine.Credentials{Password: "pw", Key: key},
	}
	if err := os.WriteFile(i.Disk, []byte("disk"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(i.NVRAM, []byte("vars"), 0o600); err != nil {
		return err
	}
	dir := filepath.Join(store.Root, "images")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	body, err := json.Marshal(i)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, id+".json"), append(body, '\n'), 0o600)
}

func enginePlay(t *testing.T, dir string, m *machine.Manager) *Engine {
	t.Helper()
	ord := []string{}
	g := guest.New("dom", "user", "", "", "")
	g.StageName = "demo"
	e := playEngine(t, dir, nil)
	e.Stager = &fakeVMStager{
		fakeStager: fakeStager{order: &ord, m: &scene.Manifest{Panes: map[string]string{"t": "%1"}, Order: []string{"t"}}},
		g:          g,
		omarchy:    "1.0",
		phases:     map[string]float64{"up": 0.1},
		session:    fptr(0.5),
	}
	e.Managed = m
	e.ManagedName = "demo"
	return e
}

func TestProducerTakeThenConsumerSkipsRestore(t *testing.T) {
	var boots atomic.Int32
	machine.SetBootGuest(func(*guest.Guest, time.Duration) error {
		boots.Add(1)
		return nil
	})
	t.Cleanup(func() { machine.SetBootGuest(nil) })

	m, initial := liveManaged(t)
	dir := t.TempDir()
	producer := enginePlay(t, dir, m)
	prod := &scene.Scene{
		Name:    "make-ready",
		Layout:  "solo",
		VMStart: &scene.VMStart{Mode: "clean", Snapshot: "initial"},
		VMEnd:   &scene.VMEnd{Snapshot: "ready"},
		Steps:   []scene.Step{{Action: "wait"}},
	}
	if err := producer.Run(prod, Options{Record: true, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	if boots.Load() != 1 {
		t.Fatalf("producer boots %d; something started the guest after vm-end", boots.Load())
	}
	rec, err := m.Store.Load("demo")
	if err != nil {
		t.Fatal(err)
	}
	if rec.AtState == nil {
		t.Fatal("producer did not write at-state")
	}
	if err := assertPrintsMatch(rec); err != nil {
		t.Fatalf("Finish/hooks/publish touched the stage files: %v", err)
	}
	captured := rec.Snapshots["ready"]
	if captured == "" || captured == initial || captured == rec.Source.Image {
		t.Fatalf("captured %s initial %s source %s", captured, initial, rec.Source.Image)
	}

	consumer := enginePlay(t, dir, m)
	cons := &scene.Scene{
		Name:    "use-ready",
		Layout:  "solo",
		VMStart: &scene.VMStart{Mode: "clean", Snapshot: "ready"},
		Steps:   []scene.Step{{Action: "wait"}},
	}
	clip := filepath.Join(dir, "consumer.mp4")
	if err := consumer.Run(cons, Options{Record: true, OutPath: clip, Speed: 0.0001, Version: "v"}); err != nil {
		t.Fatal(err)
	}
	got := readFacts(t, clip)
	if got.StartImage != captured {
		t.Fatalf("start-image %q want captured %q (source %q)", got.StartImage, captured, rec.Source.Image)
	}
	if got.Timings == nil || got.Timings.RestoreSkipped == nil || !*got.Timings.RestoreSkipped {
		t.Fatalf("consumer timings: %+v", got.Timings)
	}
	if got.Timings.RestoreStopSeconds != nil || got.Timings.RestoreActivateSeconds != nil {
		t.Fatalf("consumer restored: %+v", got.Timings)
	}
}

func assertPrintsMatch(r *machine.Record) error {
	disk, err := statPrint(r.Disk)
	if err != nil {
		return err
	}
	nvram, err := statPrint(r.NVRAM)
	if err != nil {
		return err
	}
	if r.AtState.Disk != disk || r.AtState.NVRAM != nvram {
		return errPrintMismatch
	}
	return nil
}

var errPrintMismatch = errString("fingerprint drifted")

type errString string

func (e errString) Error() string { return string(e) }

func statPrint(path string) (machine.FilePrint, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return machine.FilePrint{}, err
	}
	return machine.FilePrint{Path: path, Inode: st.Ino, Size: st.Size, MtimeNs: st.Mtim.Nano(), CtimeNs: st.Ctim.Nano()}, nil
}
