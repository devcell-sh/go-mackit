//go:build darwin && arm64

package vz_test

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/devcell-sh/go-mackit/internal/testutil"
	"github.com/devcell-sh/go-mackit/vm"
	"github.com/devcell-sh/go-mackit/vm/vz"
)

// helperBinPath returns the path to the mackit-vz helper binary.
// Uses MACKIT_VZ_HELPER if set, otherwise expects ./bin/mackit-vz
// (built by task build:vz).
func helperBinPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("MACKIT_VZ_HELPER"); p != "" {
		return p
	}
	root := projectRoot(t)
	p := filepath.Join(root, "bin", "mackit-vz")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("mackit-vz binary not found at %s; run 'task build:vz' first", p)
	}
	return p
}

func projectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (no go.mod)")
		}
		dir = parent
	}
}

// vzWorkDir returns a persistent work directory for VZ tests.
// Checks MACKIT_VZ_WORKDIR first, then test/testdata/vz-installed
// (a pre-installed disk fixture), and falls back to a fresh result dir.
func vzWorkDir(t *testing.T) string {
	t.Helper()
	if d := os.Getenv("MACKIT_VZ_WORKDIR"); d != "" {
		return d
	}
	fixture := filepath.Join(projectRoot(t), "test", "testdata", "vz-installed")
	if info, err := os.Stat(filepath.Join(fixture, "disk.raw")); err == nil && info.Size() > 10*1024*1024*1024 {
		t.Logf("using fixture disk from %s", fixture)
		return fixture
	}
	return testutil.ResultDir(t)
}

func newBackend(t *testing.T, workDir string) *vz.HelperBackend {
	t.Helper()
	return &vz.HelperBackend{
		Logger:     testutil.TestLogger(t, workDir),
		HelperPath: helperBinPath(t),
	}
}

// TestVZInstallAndBoot installs macOS from an IPSW, boots the disk,
// and verifies the display is alive via VNC.
//
//	task test:vz:install
//	MACKIT_IPSW=~/Downloads/UniversalMac_15.0_Restore.ipsw task test:vz:install
func TestVZInstallAndBoot(t *testing.T) {
	if os.Getenv("MACKIT_VZ_TEST") == "" {
		t.Skip("set MACKIT_VZ_TEST=1 to run VZ integration tests")
	}

	ipsw := os.Getenv("MACKIT_IPSW")
	if ipsw == "" {
		ipsw = "latest"
	}

	forceInstall := os.Getenv("MACKIT_VZ_FORCE_INSTALL") != ""
	workDir := vzWorkDir(t)
	if forceInstall {
		workDir = testutil.ResultDir(t)
	}
	t.Logf("work dir: %s", workDir)

	backend := newBackend(t, workDir)
	diskPath := filepath.Join(workDir, "disk.raw")

	// --- Install (skip if disk already exists) ---
	if info, err := os.Stat(diskPath); err == nil && info.Size() > 10*1024*1024*1024 && !forceInstall {
		t.Logf("disk already installed (%d GB), skipping install", info.Size()/(1024*1024*1024))
	} else {
		t.Log("creating disk...")
		require.NoError(t, backend.CreateDisk(diskPath, 64, vm.DiskFormatRaw))

		installCtx, installCancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer installCancel()

		t.Logf("installing from IPSW %q...", ipsw)
		installer, err := backend.StartInstall(installCtx, vm.VMInstallConfig{
			InstallerDMG: ipsw,
			DiskPath:     diskPath,
			DiskSizeGB:   64,
			CPUs:         4,
			MemoryGB:     8,
			OutputDir:    workDir,
		})
		require.NoError(t, err, "StartInstall")

		select {
		case <-installer.Done():
			t.Log("install completed, stopping install VM...")
			installer.Stop()
			t.Log("install VM stopped")
		case <-installCtx.Done():
			t.Fatal("timeout waiting for install")
		}

		info, err := os.Stat(diskPath)
		require.NoError(t, err)
		require.Greater(t, info.Size(), int64(10*1024*1024*1024), "installed disk must be >10 GB")
		t.Logf("disk size: %d GB", info.Size()/(1024*1024*1024))
	}

	// --- Boot ---
	bootCtx, bootCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer bootCancel()

	t.Log("booting installed disk...")
	machine, err := backend.StartRun(bootCtx, vm.VMRunConfig{
		DiskPath:      diskPath,
		OutputDir:     workDir,
		CPUs:          4,
		MemoryGB:      8,
		GuestUser:     "mackit",
		GuestPassword: "mackit",
		EnableSSH:     true,
	})
	require.NoError(t, err, "StartRun")
	defer machine.Stop()

	// Wait for VM to settle before checking VNC
	time.Sleep(10 * time.Second)

	port := vz.GetVNCPort(machine)
	t.Logf("VNC server on port %d", port)

	if port > 0 {
		client, err := vz.DialVNC(fmt.Sprintf("127.0.0.1:%d", port))
		require.NoError(t, err, "VNC dial")
		defer client.Close()

		shotDir := filepath.Join(workDir, "screenshots")
		img := captureAndSave(t, bootCtx, client, shotDir, "boot.png")
		require.False(t, isBlackScreen(img), "display must not be a black screen; check screenshots/boot.png")
		t.Log("boot verified: display is not black")
	} else {
		t.Log("VNC not available, skipping display verification")
	}

	// --- SSH (only works if guest provisioning created the user) ---
	if sshAddr := machine.SSHAddr(); sshAddr != "" {
		t.Logf("trying SSH at %s...", sshAddr)
		if err := trySSH(t, bootCtx, sshAddr, "mackit", "mackit"); err != nil {
			t.Logf("SSH not reachable (provisioning may not be supported): %v", err)
		} else {
			t.Log("SSH verified: connected as mackit")
		}
	} else {
		t.Log("no SSH address, skipping SSH verification")
	}

	if os.Getenv("MACKIT_NO_TEARDOWN") == "1" {
		t.Logf("MACKIT_NO_TEARDOWN=1: VM left running, VNC on 127.0.0.1:%d", port)
		<-machine.Done()
	}
}

// TestVZProvisionAndSSH installs macOS from IPSW (if needed), boots with
// guest provisioning (macOS 27+), and verifies SSH access.
//
//	task test:vz
//	MACKIT_VZ_FORCE_INSTALL=1 task test:vz
func TestVZProvisionAndSSH(t *testing.T) {
	if os.Getenv("MACKIT_VZ_TEST") == "" {
		t.Skip("set MACKIT_VZ_TEST=1 to run VZ integration tests")
	}

	ipsw := os.Getenv("MACKIT_IPSW")
	if ipsw == "" {
		ipsw = "latest"
	}

	forceInstall := os.Getenv("MACKIT_VZ_FORCE_INSTALL") != ""
	workDir := vzWorkDir(t)
	if forceInstall {
		workDir = testutil.ResultDir(t)
	}
	t.Logf("work dir: %s", workDir)

	backend := newBackend(t, workDir)
	diskPath := filepath.Join(workDir, "disk.raw")

	// --- Install (skip if disk already exists) ---
	if info, err := os.Stat(diskPath); err == nil && info.Size() > 10*1024*1024*1024 && !forceInstall {
		t.Logf("disk already installed (%d GB), skipping install", info.Size()/(1024*1024*1024))
	} else {
		t.Log("creating disk...")
		require.NoError(t, backend.CreateDisk(diskPath, 64, vm.DiskFormatRaw))

		installCtx, installCancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer installCancel()

		t.Logf("installing from IPSW %q...", ipsw)
		installer, err := backend.StartInstall(installCtx, vm.VMInstallConfig{
			InstallerDMG: ipsw,
			DiskPath:     diskPath,
			DiskSizeGB:   64,
			CPUs:         4,
			MemoryGB:     8,
			OutputDir:    workDir,
		})
		require.NoError(t, err, "StartInstall")

		select {
		case <-installer.Done():
			t.Log("install completed, stopping install VM...")
			installer.Stop()
			t.Log("install VM stopped")
		case <-installCtx.Done():
			t.Fatal("timeout waiting for install")
		}

		info, err := os.Stat(diskPath)
		require.NoError(t, err)
		require.Greater(t, info.Size(), int64(10*1024*1024*1024), "installed disk must be >10 GB")
		t.Logf("disk size: %d GB", info.Size()/(1024*1024*1024))
	}

	// --- Boot with provisioning ---
	bootCtx, bootCancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer bootCancel()

	t.Log("booting with guest provisioning (user=mackit, ssh=true)...")
	machine, err := backend.StartRun(bootCtx, vm.VMRunConfig{
		DiskPath:      diskPath,
		OutputDir:     workDir,
		CPUs:          4,
		MemoryGB:      8,
		GuestUser:     "mackit",
		GuestPassword: "mackit",
		EnableSSH:     true,
	})
	require.NoError(t, err, "StartRun with provisioning")
	defer func() {
		bootCancel()
		machine.Stop()
	}()

	// Periodic screenshots for debugging
	shotDir := filepath.Join(workDir, "screenshots")
	shotStop := make(chan struct{})
	shotDone := make(chan struct{})
	go vz.CaptureScreenshots(machine, shotDir, 15*time.Second, shotStop, shotDone,
		testutil.TestLogger(t, workDir))
	defer func() {
		close(shotStop)
		<-shotDone
	}()

	// --- Discover guest IP ---
	mac := vz.GetMACAddress(machine)
	require.NotEmpty(t, mac, "helper must report MAC address")
	t.Logf("guest MAC: %s", mac)

	select {
	case <-machine.Done():
		t.Fatal("VM exited immediately after boot (helper crashed)")
	case <-time.After(3 * time.Second):
	}

	t.Log("waiting for guest IP (DHCP/ARP lookup, up to 3 min)...")
	guestIP, err := vz.GuestIP(mac, 3*time.Minute)
	if err != nil {
		select {
		case <-machine.Done():
			t.Fatalf("VM died during IP lookup: %v", err)
		default:
		}
		if forceInstall {
			t.Fatalf("guest IP not found after fresh install: %v", err)
		}
		t.Fatalf("guest IP not found (disk may need fresh install: MACKIT_VZ_FORCE_INSTALL=1 task test:vz): %v", err)
	}
	t.Logf("guest IP: %s", guestIP)

	// --- SSH ---
	sshAddr := net.JoinHostPort(guestIP, "22")
	t.Logf("trying SSH at %s (up to 3 min)...", sshAddr)
	err = trySSH(t, bootCtx, sshAddr, "mackit", "mackit")
	if err != nil {
		t.Fatalf("SSH failed: %v", err)
	}
	t.Log("SSH verified: connected as mackit user")
}

// TestVZSetupAssistant installs (if needed), boots, and idles with VNC up
// for developing the Setup Assistant flow.
//
//	task test:vz:setup-assistant
//	MACKIT_VZ_WORKDIR=<workdir> task test:vz:setup-assistant
func TestVZSetupAssistant(t *testing.T) {
	if os.Getenv("MACKIT_VZ_TEST") == "" {
		t.Skip("set MACKIT_VZ_TEST=1 to run VZ integration tests")
	}

	workDir := vzWorkDir(t)
	t.Logf("work dir: %s", workDir)

	backend := newBackend(t, workDir)
	diskPath := filepath.Join(workDir, "disk.raw")

	// --- Install (skip if disk already exists) ---
	if info, err := os.Stat(diskPath); err == nil && info.Size() > 10*1024*1024*1024 {
		t.Logf("disk already installed (%d GB), skipping install", info.Size()/(1024*1024*1024))
	} else {
		ipsw := os.Getenv("MACKIT_IPSW")
		if ipsw == "" {
			ipsw = "latest"
		}

		t.Log("creating disk...")
		require.NoError(t, backend.CreateDisk(diskPath, 64, vm.DiskFormatRaw))

		installCtx, installCancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer installCancel()

		t.Logf("installing from IPSW %q...", ipsw)
		installer, err := backend.StartInstall(installCtx, vm.VMInstallConfig{
			InstallerDMG: ipsw,
			DiskPath:     diskPath,
			DiskSizeGB:   64,
			CPUs:         4,
			MemoryGB:     8,
			OutputDir:    workDir,
		})
		require.NoError(t, err, "StartInstall")

		select {
		case <-installer.Done():
			t.Log("install completed, stopping install VM...")
			installer.Stop()
			t.Log("install VM stopped")
		case <-installCtx.Done():
			t.Fatal("timeout waiting for install")
		}

		info, err := os.Stat(diskPath)
		require.NoError(t, err)
		require.Greater(t, info.Size(), int64(10*1024*1024*1024), "installed disk must be >10 GB")
	}

	// --- Boot ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t.Log("booting for Setup Assistant...")
	machine, err := backend.StartRun(ctx, vm.VMRunConfig{
		DiskPath:  diskPath,
		OutputDir: workDir,
		CPUs:      4,
		MemoryGB:  8,
	})
	require.NoError(t, err, "StartRun")
	defer machine.Stop()

	time.Sleep(10 * time.Second)

	port := vz.GetVNCPort(machine)
	t.Logf("VNC server on port %d", port)

	if port > 0 {
		client, err := vz.DialVNC(fmt.Sprintf("127.0.0.1:%d", port))
		require.NoError(t, err, "VNC dial")
		defer client.Close()

		shotDir := filepath.Join(workDir, "screenshots")
		img := captureAndSave(t, ctx, client, shotDir, "setup-assistant.png")
		require.False(t, isBlackScreen(img), "display must not be a black screen")

		t.Logf("Setup Assistant screen captured, VM running, VNC on 127.0.0.1:%d", port)
		t.Log("connect with: open vnc://127.0.0.1:" + fmt.Sprint(port))
	} else {
		t.Log("VNC not available (private API may have changed), skipping screenshot")
	}

	t.Log("VM booted successfully, waiting for exit...")
	<-machine.Done()
}

// TestVZBoot_Recovery boots an installed disk into Recovery and verifies
// it stays alive for 30s.
//
//	task test:vz TEST_RUN=TestVZBoot_Recovery
func TestVZBoot_Recovery(t *testing.T) {
	if os.Getenv("MACKIT_VZ_TEST") == "" {
		t.Skip("set MACKIT_VZ_TEST=1 to run VZ integration tests")
	}

	workDir := vzWorkDir(t)
	diskPath := filepath.Join(workDir, "disk.raw")
	requireInstalled(t, workDir, diskPath)
	logger := testutil.TestLogger(t, workDir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	t.Log("booting Recovery...")
	machine, err := vz.StartRecovery(ctx, vm.VMRunConfig{
		DiskPath:  diskPath,
		OutputDir: workDir,
		CPUs:      4,
		MemoryGB:  8,
	}, logger)
	require.NoError(t, err, "StartRecovery")
	defer machine.Stop()

	time.Sleep(3 * time.Second)

	t.Log("VM booted into Recovery, waiting 30s to verify stability...")
	select {
	case <-machine.Done():
		t.Fatal("VM exited unexpectedly")
	case <-time.After(30 * time.Second):
		t.Log("VM stable after 30s")
	}

	t.Log("Recovery boot verified")
	cancel()
	machine.Stop()
}

// TestVZVNC_Capture boots Recovery, starts VNC, captures a screenshot,
// and sends a keypress.
//
//	MACKIT_VZ_WORKDIR=<workdir> task test:vz TEST_RUN=TestVZVNC_Capture
func TestVZVNC_Capture(t *testing.T) {
	if os.Getenv("MACKIT_VZ_TEST") == "" {
		t.Skip("set MACKIT_VZ_TEST=1 to run VZ integration tests")
	}

	workDir := vzWorkDir(t)
	diskPath := filepath.Join(workDir, "disk.raw")
	requireInstalled(t, workDir, diskPath)
	logger := testutil.TestLogger(t, workDir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	t.Log("booting Recovery for VNC test...")
	machine, err := vz.StartRecovery(ctx, vm.VMRunConfig{
		DiskPath:  diskPath,
		OutputDir: workDir,
		CPUs:      4,
		MemoryGB:  8,
	}, logger)
	require.NoError(t, err)
	defer machine.Stop()

	t.Log("waiting 10s for boot progress...")
	time.Sleep(10 * time.Second)

	port := vz.GetVNCPort(machine)
	if port == 0 {
		cancel()
		machine.Stop()
		t.Skip("VNC not available (helper did not assign a port)")
	}
	t.Logf("VNC server on port %d", port)

	t.Logf("connecting VNC to 127.0.0.1:%d...", port)
	client, err := vz.DialVNC(fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err, "VNC dial")
	defer client.Close()

	t.Log("capturing screenshot...")
	img, err := client.Capture(ctx)
	require.NoError(t, err, "VNC capture")
	require.NotNil(t, img)

	bounds := img.Bounds()
	t.Logf("screenshot: %dx%d", bounds.Dx(), bounds.Dy())
	require.Greater(t, bounds.Dx(), 0)
	require.Greater(t, bounds.Dy(), 0)

	shotDir := filepath.Join(workDir, "screenshots")
	os.MkdirAll(shotDir, 0o755)
	shotPath := filepath.Join(shotDir, "vnc-capture.png")
	f, err := os.Create(shotPath)
	require.NoError(t, err)
	require.NoError(t, png.Encode(f, img))
	f.Close()
	t.Logf("screenshot saved: %s", shotPath)

	input := vz.NewVNCInput(client)
	t.Log("sending Enter key...")
	require.NoError(t, input.SendKeys(ctx, "ret"))
	t.Log("VNC capture + input pipeline verified")
}

func requireInstalled(t *testing.T, workDir, diskPath string) {
	t.Helper()
	require.FileExists(t, diskPath, "disk.raw must exist from a prior install run")
	require.FileExists(t, filepath.Join(workDir, "platform.json"), "platform.json must exist")
	require.FileExists(t, filepath.Join(workDir, "nvram.bin"), "nvram.bin must exist")
}

func isBlackScreen(img image.Image) bool {
	bounds := img.Bounds()
	samples := 0
	nonBlack := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y += bounds.Dy() / 20 {
		for x := bounds.Min.X; x < bounds.Max.X; x += bounds.Dx() / 20 {
			r, g, b, _ := img.At(x, y).RGBA()
			samples++
			if r > 0x1000 || g > 0x1000 || b > 0x1000 {
				nonBlack++
			}
		}
	}
	return nonBlack < samples/10
}

func saveScreenshot(t *testing.T, img image.Image, dir, name string) string {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	require.NoError(t, err)
	require.NoError(t, png.Encode(f, img))
	f.Close()
	return p
}

func captureAndSave(t *testing.T, ctx context.Context, client *vz.VNCClient, dir, name string) image.Image {
	t.Helper()
	img, err := client.Capture(ctx)
	require.NoError(t, err, "VNC capture")
	p := saveScreenshot(t, img, dir, name)
	t.Logf("screenshot: %s", p)
	return img
}

func trySSH(t *testing.T, ctx context.Context, addr, user, password string) error {
	t.Helper()
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(2 * time.Minute)
	}
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			lastErr = err
			time.Sleep(3 * time.Second)
			continue
		}
		sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
		if err != nil {
			conn.Close()
			lastErr = err
			time.Sleep(3 * time.Second)
			continue
		}
		client := ssh.NewClient(sshConn, chans, reqs)
		session, err := client.NewSession()
		if err != nil {
			client.Close()
			return fmt.Errorf("ssh session: %w", err)
		}
		out, err := session.CombinedOutput("whoami")
		session.Close()
		client.Close()
		if err != nil {
			return fmt.Errorf("ssh whoami: %w", err)
		}
		t.Logf("SSH whoami: %s", string(out))
		return nil
	}
	return fmt.Errorf("SSH not reachable after retries: %w", lastErr)
}
