package e2e

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/devcell-sh/go-mackit/build"
	"github.com/devcell-sh/go-mackit/cache"
	"github.com/devcell-sh/go-mackit/internal/testutil"
)

// installFrontier is the last flow step verified against a live Catalina
// VM. TestExampleFull_Install runs the build up to it and stops; move it
// forward as the next step is confirmed from the saved frames, OCR
// readings, and the VM over VNC. MACKIT_INSTALL_UNTIL=<step> overrides it
// for a run (a step of flow.CatalinaInstall or flow.HeadlessSetup), and
// MACKIT_INSTALL_UNTIL=all runs the whole build to the flattened image.
const installFrontier = "install"

// TestExampleFull_Install runs a Full build of macOS Catalina one verified
// step at a time: boot to Recovery, then the install flow
// (flow.CatalinaInstall) up to installFrontier. The build then idles with
// the VM up; the test cancels it there and, with MACKIT_NO_TEARDOWN=1,
// leaves the VM running so the next step can be worked out against it
// over VNC (the port is in build.jsonl, "VM left running"). With
// MACKIT_INSTALL_UNTIL=all the build runs through the installer's
// download, install, and reboots, the headless setup from Recovery
// (flow.HeadlessSetup: user, Setup Assistant skipped, Remote Login), and
// the installed system's first boot to SSH, which needs guest network
// and, under TCG, about four hours.
//
//	MACKIT_E2E=1 MACKIT_NO_TEARDOWN=1 go test -run TestExampleFull_Install -timeout 60m -v ./test/e2e/
//	MACKIT_E2E=1 MACKIT_INSTALL_UNTIL=all go test -run TestExampleFull_Install -timeout 420m -v ./test/e2e/
//
// MACKIT_VM_MEMORY_GB overrides the VM memory (default 8): in a container
// with a 4 GiB cgroup limit use 3, or the kernel kills QEMU mid-download.
func TestExampleFull_Install(t *testing.T) {
	skipUnlessE2E(t)

	_, opts := loadExampleOpts(t, "full-catalina")
	require.False(t, opts.Recovery, "full-catalina example must be a Full build")

	stopAfter := installFrontier
	if v := os.Getenv("MACKIT_INSTALL_UNTIL"); v == "all" {
		stopAfter = ""
	} else if v != "" {
		stopAfter = v
	}
	stopAt := "install flow stopped, VM idle for inspection"
	if stopAfter == "" {
		stopAt = "build complete"
	}

	resultDir := testutil.ResultDir(t)
	t.Logf("results: %s", resultDir)
	workDir := filepath.Join(resultDir, "work")

	ctx, cancel := context.WithTimeout(context.Background(), 410*time.Minute)
	defer cancel()

	done := &milestoneHandler{next: testLogger(t, resultDir).Handler(), stopAt: stopAt, onStop: cancel, mu: &sync.Mutex{}}
	err := build.Full(ctx, build.Config{
		Dest:          filepath.Join(resultDir, "macos-catalina.qcow2"),
		CacheDir:      cache.Dir(),
		WorkDir:       workDir,
		Opts:          opts,
		Logger:        slog.New(done),
		StopAfterStep: stopAfter,
		KeepVM:        noTeardown(),
		MemoryGB:      envInt(t, "MACKIT_VM_MEMORY_GB"),
	})
	t.Logf("build returned: %v", err)
	require.True(t, done.reached(), "the build must reach %q; see build.jsonl and work/screenshots", stopAt)
	if noTeardown() {
		t.Log("MACKIT_NO_TEARDOWN=1: VM left running, see \"VM left running\" in build.jsonl for the VNC port")
	}

	// Milestones, in order of the flow, up to the stop step.
	requireLogged(t, resultDir, "macOS Recovery reached")
	if stopAfter != "" {
		requireLogged(t, resultDir, `"step":"`+stopAfter+`"`)
		return
	}
	require.NoError(t, err)
	requireLogged(t, resultDir, `"step":"install"`)
	requireLogged(t, resultDir, `"screen":"installer.progress"`)
	requireLogged(t, resultDir, `"step":"installed system booted"`)
	requireLogged(t, resultDir, "macOS installed, installed system booted")
	requireLogged(t, resultDir, `"step":"headless setup done"`)
	requireLogged(t, resultDir, "headless setup done, Setup Assistant skipped and Remote Login enabled")
	requireLogged(t, resultDir, "SSH is reachable")
	requireLogged(t, resultDir, "build complete")
	if !noTeardown() {
		require.FileExists(t, filepath.Join(resultDir, "macos-catalina.qcow2"))
	}
}

// milestoneHandler cancels the build once a log message is seen.
type milestoneHandler struct {
	next   slog.Handler
	stopAt string
	onStop func()
	mu     *sync.Mutex
	seen   bool
}

func (h *milestoneHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}
func (h *milestoneHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &milestoneHandler{next: h.next.WithAttrs(attrs), stopAt: h.stopAt, onStop: h.onStop, mu: h.mu, seen: h.seen}
}
func (h *milestoneHandler) WithGroup(name string) slog.Handler {
	return &milestoneHandler{next: h.next.WithGroup(name), stopAt: h.stopAt, onStop: h.onStop, mu: h.mu, seen: h.seen}
}
func (h *milestoneHandler) Handle(ctx context.Context, r slog.Record) error {
	err := h.next.Handle(ctx, r)
	if r.Message == h.stopAt {
		h.mu.Lock()
		first := !h.seen
		h.seen = true
		h.mu.Unlock()
		if first {
			h.onStop()
		}
	}
	return err
}

func (h *milestoneHandler) reached() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seen
}

// envInt reads an integer environment variable, 0 when unset.
func envInt(t *testing.T, name string) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	require.NoError(t, err, "%s must be an integer", name)
	return n
}
