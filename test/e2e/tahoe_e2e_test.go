package e2e

import (
	"context"
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

// tahoeFrontier is the last flow step verified against a live Tahoe VM.
// Move it forward as each step is confirmed from saved screenshots and
// OCR readings. MACKIT_INSTALL_UNTIL=<step> overrides it, and
// MACKIT_INSTALL_UNTIL=all runs the whole build through Setup Assistant
// to desktop.
const tahoeFrontier = "select language"

// TestExampleFull_TahoeInstall runs a Full build of macOS Tahoe one
// verified step at a time. Unlike Catalina and Sequoia, this flow drives
// through Setup Assistant instead of bypassing it with HeadlessSetup.
//
// The development loop: advance tahoeFrontier one step at a time, verify
// each screen via VNC and the saved OCR readings in work/ocr/, then
// update the Tahoe catalog and install steps.
//
//	MACKIT_E2E=1 MACKIT_NO_TEARDOWN=1 go test -run TestExampleFull_TahoeInstall -timeout 60m -v ./test/e2e/
//	MACKIT_E2E=1 MACKIT_INSTALL_UNTIL="installed system booted" MACKIT_VM_MEMORY_GB=8 MACKIT_NO_TEARDOWN=1 go test -run TestExampleFull_TahoeInstall -timeout 420m -v ./test/e2e/
//	MACKIT_E2E=1 MACKIT_INSTALL_UNTIL=all go test -run TestExampleFull_TahoeInstall -timeout 420m -v ./test/e2e/
func TestExampleFull_TahoeInstall(t *testing.T) {
	skipUnlessE2E(t)

	_, opts := loadExampleOpts(t, "full-tahoe")
	require.False(t, opts.Recovery, "full-tahoe example must be a Full build")

	stopAfter := tahoeFrontier
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
		Dest:          filepath.Join(resultDir, "macos-tahoe.qcow2"),
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

	if stopAfter != "" {
		requireLogged(t, resultDir, `"step":"`+stopAfter+`"`)
		return
	}
	require.NoError(t, err)
	requireLogged(t, resultDir, `"step":"installed system booted"`)
	requireLogged(t, resultDir, "macOS installed, installed system booted")
	// Setup Assistant milestones (unlike Catalina/Sequoia, no headless setup).
	requireLogged(t, resultDir, `"step":"setup country"`)
	requireLogged(t, resultDir, `"step":"desktop"`)
	requireLogged(t, resultDir, "build complete")
	if !noTeardown() {
		require.FileExists(t, filepath.Join(resultDir, "macos-tahoe.qcow2"))
	}
}
