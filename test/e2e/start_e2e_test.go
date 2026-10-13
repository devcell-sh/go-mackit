package e2e

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mackit "github.com/devcell-sh/go-mackit"
	"github.com/devcell-sh/go-mackit/internal/testutil"
	"github.com/devcell-sh/go-mackit/vm/qemu"
)

// testdataCatalinaDir is the pre-built Catalina image used by boot tests.
// It contains macos-catalina.qcow2, OpenCore.qcow2, and vars.fd, produced
// by a previous TestExampleFull_Install MACKIT_INSTALL_UNTIL=all run and
// copied there.
const testdataCatalinaDir = "test/testdata/macos/catalina"

// catalinaImage returns the absolute path to the pre-built Catalina image,
// skipping the test when it is not present.
func catalinaImage(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", testdataCatalinaDir))
	require.NoError(t, err)
	img := filepath.Join(dir, "macos-catalina.qcow2")
	if _, err := os.Stat(img); err != nil {
		t.Skipf("pre-built Catalina image not found at %s: copy one from a successful full install", img)
	}
	return img
}

// startAndCapture starts a macOS VM via mackit.Start and captures periodic
// screenshots to resultDir/screenshots/ via QMP screendump. It returns the
// VM and a stop function that ends the screenshot goroutine.
func startAndCapture(t *testing.T, ctx context.Context, opts mackit.StartOpts, resultDir string) (machine interface {
	Stop() error
	PID() int
	Done() <-chan struct{}
}, stopShots func()) {
	t.Helper()
	m, err := mackit.Start(ctx, opts)
	require.NoError(t, err, "image must boot via mackit.Start")

	qmpSock := qemu.QMPSocketFromVM(m)
	shotDir := filepath.Join(resultDir, "screenshots")
	os.MkdirAll(shotDir, 0o755)

	stop := make(chan struct{})
	done := make(chan struct{})
	go qemu.CaptureScreenshots(
		qmpSock, shotDir, 15*time.Second,
		stop, done,
		func(f string, a ...any) { t.Logf("screenshot: "+f, a...) },
	)
	return m, func() {
		close(stop)
		<-done
	}
}

// waitSSHOrFail polls for an SSH banner until it appears, the VM exits,
// or the deadline is hit.
func waitSSHOrFail(t *testing.T, sshAddr string, timeout time.Duration, machine interface {
	Done() <-chan struct{}
}, ctx context.Context) {
	t.Helper()
	deadline := time.After(timeout)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatalf("SSH did not become available at %s within %v", sshAddr, timeout)
		case <-machine.Done():
			t.Fatal("VM exited before SSH became available")
		case <-ctx.Done():
			t.Fatalf("context expired waiting for SSH: %v", ctx.Err())
		case <-ticker.C:
			if checkSSHBanner(sshAddr) {
				t.Logf("SSH is reachable at %s", sshAddr)
				return
			}
		}
	}
}

// TestStart_CatalinaSSH boots the pre-built Catalina image from testdata
// and verifies that SSH becomes reachable. This confirms the installed
// system boots correctly through OpenCore without install media.
//
//	MACKIT_E2E=1 go test -run TestStart_CatalinaSSH -timeout 30m -v ./test/e2e/
//	MACKIT_E2E=1 MACKIT_NO_TEARDOWN=1 go test -run TestStart_CatalinaSSH -timeout 30m -v ./test/e2e/
func TestStart_CatalinaSSH(t *testing.T) {
	skipUnlessE2E(t)
	image := catalinaImage(t)

	resultDir := testutil.ResultDir(t)
	t.Logf("results: %s", resultDir)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	sshPort := qemu.FreePort()
	vncPort := qemu.FreePort()

	machine, stopShots := startAndCapture(t, ctx, mackit.StartOpts{
		Image:    image,
		Name:     "e2e-catalina-boot",
		StateDir: filepath.Join(resultDir, "state"),
		SSHPort:  sshPort,
		VNCPort:  vncPort,
		Logger:   testLogger(t, resultDir),
		NIC:      "vmxnet3",
		MemoryGB: startMemoryGB(t),
	}, resultDir)
	defer stopShots()
	if !noTeardown() {
		defer machine.Stop()
	} else {
		t.Logf("MACKIT_NO_TEARDOWN=1: VM left running (PID %d, VNC vnc://127.0.0.1:%d)", machine.PID(), vncPort)
	}

	t.Logf("VM started: PID=%d SSH=127.0.0.1:%d VNC=127.0.0.1:%d", machine.PID(), sshPort, vncPort)

	sshAddr := fmt.Sprintf("127.0.0.1:%d", sshPort)
	waitSSHOrFail(t, sshAddr, 20*time.Minute, machine, ctx)

	// Verify screenshots were captured.
	shotDir := filepath.Join(resultDir, "screenshots")
	pngs, _ := filepath.Glob(filepath.Join(shotDir, "*.png"))
	t.Logf("captured %d screenshots in %s", len(pngs), shotDir)
	require.Greater(t, len(pngs), 0, "at least one screenshot must be captured during boot")

	idleUntilInterrupt(t, ctx, machine)
}

// TestExampleFull_Boot boots the image produced by a full install and
// verifies SSH. Pass MACKIT_IMAGE to an explicit path, or it scans the
// test/results directory for the latest TestExampleFull_Install output.
//
//	MACKIT_E2E=1 MACKIT_IMAGE=path/to/macos-catalina.qcow2 go test -run TestExampleFull_Boot -timeout 30m -v ./test/e2e/
//	MACKIT_E2E=1 go test -run TestExampleFull_Boot -timeout 30m -v ./test/e2e/
func TestExampleFull_Boot(t *testing.T) {
	skipUnlessE2E(t)

	imagePath := os.Getenv("MACKIT_IMAGE")
	if imagePath == "" {
		// Scan test/results for the latest full install result.
		root, err := filepath.Abs(filepath.Join("..", ".."))
		require.NoError(t, err)
		resultBase := filepath.Join(root, "test", "results", "*-TestExampleFull_Install")
		matches, _ := filepath.Glob(resultBase)
		if len(matches) > 0 {
			candidate := filepath.Join(matches[len(matches)-1], "macos-catalina.qcow2")
			if _, err := os.Stat(candidate); err == nil {
				imagePath = candidate
			}
		}
	}
	if imagePath == "" {
		t.Skip("no built image found: run TestExampleFull_Install with MACKIT_INSTALL_UNTIL=all first, or set MACKIT_IMAGE")
	}
	if _, err := os.Stat(imagePath); err != nil {
		t.Skipf("image not found: %s", imagePath)
	}
	t.Logf("booting image: %s", imagePath)

	resultDir := testutil.ResultDir(t)
	t.Logf("results: %s", resultDir)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	sshPort := qemu.FreePort()
	vncPort := qemu.FreePort()

	machine, stopShots := startAndCapture(t, ctx, mackit.StartOpts{
		Image:    imagePath,
		Name:     "e2e-full-boot",
		StateDir: filepath.Join(resultDir, "state"),
		SSHPort:  sshPort,
		VNCPort:  vncPort,
		Logger:   testLogger(t, resultDir),
		NIC:      "vmxnet3",
		MemoryGB: startMemoryGB(t),
	}, resultDir)
	defer stopShots()
	if !noTeardown() {
		defer machine.Stop()
	} else {
		t.Logf("MACKIT_NO_TEARDOWN=1: VM left running (PID %d, VNC vnc://127.0.0.1:%d)", machine.PID(), vncPort)
	}

	t.Logf("VM started: PID=%d SSH=127.0.0.1:%d VNC=127.0.0.1:%d", machine.PID(), sshPort, vncPort)

	sshAddr := fmt.Sprintf("127.0.0.1:%d", sshPort)
	waitSSHOrFail(t, sshAddr, 20*time.Minute, machine, ctx)

	// Verify screenshots were captured.
	shotDir := filepath.Join(resultDir, "screenshots")
	pngs, _ := filepath.Glob(filepath.Join(shotDir, "*.png"))
	t.Logf("captured %d screenshots in %s", len(pngs), shotDir)
	require.Greater(t, len(pngs), 0, "at least one screenshot must be captured during boot")

	requireLogged(t, resultDir, "starting VM")

	idleUntilInterrupt(t, ctx, machine)
}

// startMemoryGB reads MACKIT_VM_MEMORY_GB for the boot tests, defaulting
// to 8 GB.
func startMemoryGB(t *testing.T) uint64 {
	t.Helper()
	n := envInt(t, "MACKIT_VM_MEMORY_GB")
	if n == 0 {
		return 8
	}
	return uint64(n)
}

// idleUntilInterrupt blocks until Ctrl-C, context expiry, or VM exit when
// MACKIT_NO_TEARDOWN=1. This keeps the test process alive so screenshot
// capture and log tailing goroutines keep running. Without NO_TEARDOWN
// it returns immediately.
func idleUntilInterrupt(t *testing.T, ctx context.Context, machine interface{ Done() <-chan struct{} }) {
	t.Helper()
	if !noTeardown() {
		return
	}
	t.Log("MACKIT_NO_TEARDOWN=1: idling until Ctrl-C or timeout (screenshots and logs still running)")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)

	select {
	case <-sig:
		t.Log("interrupted, shutting down")
	case <-ctx.Done():
		t.Log("context expired, shutting down")
	case <-machine.Done():
		t.Log("VM exited")
	}
}
