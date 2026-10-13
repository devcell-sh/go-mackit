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

// sequoiaFrontier is the last flow step verified against a live Sequoia
// VM. Move it forward as each step is confirmed from saved screenshots
// and OCR readings. MACKIT_INSTALL_UNTIL=<step> overrides it, and
// MACKIT_INSTALL_UNTIL=all runs the whole build.
const sequoiaFrontier = "select language"

// TestExampleFull_SequoiaInstall runs a Full build of macOS Sequoia one
// verified step at a time for each guest architecture. The x86_64 guest
// boots via OpenCore; the arm64 guest is skipped until a non-OpenCore
// boot chain is available.
//
//	MACKIT_E2E=1 MACKIT_INSTALL_UNTIL="select language" MACKIT_VM_MEMORY_GB=3 go test -run TestExampleFull_SequoiaInstall -timeout 60m -v ./test/e2e/
//	MACKIT_E2E=1 MACKIT_INSTALL_UNTIL=all go test -run TestExampleFull_SequoiaInstall -timeout 420m -v ./test/e2e/
func TestExampleFull_SequoiaInstall(t *testing.T) {
	skipUnlessE2E(t)

	cases := []struct {
		name            string
		arch            string
		skip            string
		recoveryTimeout time.Duration
	}{
		{
			name:            "x86_64",
			arch:            "x86_64",
			recoveryTimeout: 45 * time.Minute,
		},
		{
			name: "arm64",
			arch: "arm64",
			skip: "arm64 guest requires a boot chain without OpenCore (not yet implemented)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skip != "" {
				t.Skip(tc.skip)
			}
			runSequoiaInstall(t, tc.arch, tc.recoveryTimeout)
		})
	}
}

func runSequoiaInstall(t *testing.T, guestArch string, recoveryTimeout time.Duration) {
	t.Helper()

	_, opts := loadExampleOpts(t, "full-sequoia")
	require.False(t, opts.Recovery, "full-sequoia example must be a Full build")

	stopAfter := sequoiaFrontier
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
		Dest:            filepath.Join(resultDir, "macos-sequoia.qcow2"),
		CacheDir:        cache.Dir(),
		WorkDir:         workDir,
		Opts:            opts,
		Logger:          slog.New(done),
		GuestArch:       guestArch,
		StopAfterStep:   stopAfter,
		KeepVM:          noTeardown(),
		MemoryGB:        envInt(t, "MACKIT_VM_MEMORY_GB"),
		RecoveryTimeout: recoveryTimeout,
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
	requireLogged(t, resultDir, `"step":"headless setup done"`)
	requireLogged(t, resultDir, "SSH is reachable")
	requireLogged(t, resultDir, "build complete")
	if !noTeardown() {
		require.FileExists(t, filepath.Join(resultDir, "macos-sequoia.qcow2"))
	}
}
