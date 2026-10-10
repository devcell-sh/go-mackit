package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/devcell-sh/go-mackit/build"
	"github.com/devcell-sh/go-mackit/cache"
	"github.com/devcell-sh/go-mackit/internal/testutil"
)

// TestBoot_VerboseSerial is the short boot check for the OpenCore patch:
// the kernel's verbose console must show on screen (OCR identifies
// boot.verbose) and firmware and kernel console lines must be piped from
// the serial port into build.jsonl, each tagged with its source. The
// build is cancelled as soon as both signals were seen, so this finishes
// well before Recovery's UI appears. OpenCore itself (RELEASE build) only
// logs warnings and errors, so a clean boot has no opencore lines.
//
//	MACKIT_E2E=1 go test -run TestBoot_VerboseSerial -timeout 20m -v ./test/e2e/
func TestBoot_VerboseSerial(t *testing.T) {
	skipUnlessE2E(t)

	_, opts := loadExampleOpts(t, "recovery")
	resultDir := testutil.ResultDir(t)
	t.Logf("results: %s", resultDir)
	workDir := filepath.Join(resultDir, "work")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	signals := &signalHandler{next: testLogger(t, resultDir).Handler(), onAll: cancel, mu: &sync.Mutex{}, seen: map[string]bool{}}
	logger := slog.New(signals)

	err := build.Recovery(ctx, build.Config{
		Dest:            filepath.Join(resultDir, "mackit-recovery.qcow2"),
		CacheDir:        cache.Dir(),
		WorkDir:         workDir,
		Opts:            opts,
		Logger:          logger,
		RecoveryTimeout: 14 * time.Minute,
	})
	t.Logf("build returned: %v", err)
	require.True(t, signals.all(), "missing boot signals: %v", signals.missing())

	requireLogged(t, resultDir, "patching OpenCore config.plist")
	requireLogged(t, resultDir, `"screen":"boot.verbose"`)
	requireLogged(t, resultDir, `"service":"uefi"`)
	requireLogged(t, resultDir, `"service":"xnu"`)
	requireLogged(t, resultDir, "Darwin Kernel Version")

	// Every record names the component that wrote it.
	f, err := os.Open(filepath.Join(resultDir, "build.jsonl"))
	require.NoError(t, err)
	defer f.Close()
	services := map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		var rec map[string]string
		require.NoError(t, json.Unmarshal(sc.Bytes(), &rec), sc.Text())
		require.NotEmpty(t, rec["service"], "record without service: %s", sc.Text())
		services[rec["service"]]++
	}
	t.Logf("records per service: %v", services)
	require.Greater(t, services["mackit"], 0)
	require.Greater(t, services["uefi"], 0)
	require.Greater(t, services["xnu"], 50, "the kernel's -v console must be streaming")
}

// signalHandler watches the build log for the boot signals and calls
// onAll once every one has been seen.
type signalHandler struct {
	next  slog.Handler
	onAll func()
	mu    *sync.Mutex
	seen  map[string]bool
}

var bootSignals = []string{"screen:boot.verbose", "serial:xnu"}

func (h *signalHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}
func (h *signalHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &signalHandler{next: h.next.WithAttrs(attrs), onAll: h.onAll, mu: h.mu, seen: h.seen}
}
func (h *signalHandler) WithGroup(name string) slog.Handler {
	return &signalHandler{next: h.next.WithGroup(name), onAll: h.onAll, mu: h.mu, seen: h.seen}
}

func (h *signalHandler) Handle(ctx context.Context, r slog.Record) error {
	attrs := map[string]string{}
	r.Attrs(func(a slog.Attr) bool { attrs[a.Key] = a.Value.String(); return true })
	h.mu.Lock()
	before := len(h.seen)
	if r.Message == "screen changed" && attrs["screen"] == "boot.verbose" {
		h.seen["screen:boot.verbose"] = true
	}
	if attrs["stream"] == "serial" && attrs["service"] == "xnu" {
		h.seen["serial:xnu"] = true
	}
	fire := len(h.seen) == len(bootSignals) && before != len(h.seen)
	h.mu.Unlock()
	err := h.next.Handle(ctx, r)
	if fire {
		h.onAll()
	}
	return err
}

func (h *signalHandler) all() bool { return len(h.missing()) == 0 }

func (h *signalHandler) missing() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, s := range bootSignals {
		if !h.seen[s] {
			out = append(out, s)
		}
	}
	return out
}
