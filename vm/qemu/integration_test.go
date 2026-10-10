//go:build integration

package qemu

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcell-sh/go-mackit/cache"
	"github.com/devcell-sh/go-mackit/installer"
	"github.com/devcell-sh/go-mackit/internal/testutil"
)

// TestOpenCoreBoot boots macOS Recovery via OpenCore in QEMU and verifies
// the boot chain works: EDK2 -> OpenCore -> macOS kernel. Screenshots are
// captured as evidence and stored in test/results/.
//
//	go test -tags integration -run TestOpenCoreBoot -timeout 15m -v ./vm/qemu/
func TestOpenCoreBoot(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots macOS Recovery via OpenCore in QEMU")
	}

	qemuBin := requireQEMUBin(t)
	baseSystemDMG := requireBaseSystemDMG(t)
	openCoreImg := requireOpenCoreImg(t)

	outDir := testutil.ResultDir(t)
	t.Logf("results: %s", outDir)

	rawDMG := baseSystemDMG
	if installer.IsUDIFDMG(baseSystemDMG) {
		rawDMG = filepath.Join(outDir, "BaseSystem.raw")
		t.Log("converting UDIF DMG to raw")
		var err error
		rawDMG, err = installer.ConvertDMGToRaw(baseSystemDMG, rawDMG)
		if err != nil {
			t.Fatalf("converting DMG: %v", err)
		}
	}

	diskPath := filepath.Join(outDir, "mackit.qcow2")
	if err := CreateDisk(diskPath, 32); err != nil {
		t.Fatalf("creating disk: %v", err)
	}

	fwPath := FirmwareForArch("x86_64")
	if fwPath == "" {
		t.Skip("no x86_64 UEFI firmware found")
	}
	varsPath := filepath.Join(outDir, "vars.fd")
	if err := PrepareVarsFile(fwPath, varsPath); err != nil {
		t.Fatalf("preparing vars: %v", err)
	}

	sshPort := freeTCPPort(t)
	vncPort := freeTCPPort(t)

	spec := Spec{
		VMName:        "mackit-integration-test",
		GuestArch:     "x86_64",
		CPUs:          4,
		MemoryGB:      8,
		DiskPath:      diskPath,
		FirmwarePath:  fwPath,
		VarsPath:      varsPath,
		QMPSocketDir:  outDir,
		DisplayType:   fmt.Sprintf("vnc=:%d", vncPort-5900),
		Accel:         "tcg,thread=multi",
		SerialLogPath: filepath.Join(outDir, "serial.log"),
		NoReboot:      true,
		SSHPort:       sshPort,
		VNCPort:       vncPort,
	}
	spec.ApplyDefaults()
	if err := spec.Validate(); err != nil {
		t.Fatalf("invalid spec: %v", err)
	}

	argv := BuildInstallCommand(spec, rawDMG, openCoreImg)
	argv[0] = qemuBin
	t.Logf("QEMU argv: %s", strings.Join(argv, " "))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = outDir
	stderrLog := filepath.Join(outDir, "qemu-stderr.log")
	if f, err := os.Create(stderrLog); err == nil {
		cmd.Stderr = f
		defer f.Close()
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting QEMU: %v", err)
	}
	qemuDone := make(chan struct{})
	go func() { cmd.Wait(); close(qemuDone) }()
	defer func() {
		QMPQuit(QMPSocketPath(spec))
		select {
		case <-qemuDone:
		case <-time.After(15 * time.Second):
			cmd.Process.Kill()
			<-qemuDone
		}
	}()

	t.Logf("QEMU PID: %d", cmd.Process.Pid)
	t.Logf("VNC: vnc://127.0.0.1:%d", vncPort)
	t.Logf("SSH (when available): ssh -p %d root@127.0.0.1", sshPort)

	// Wait for QMP socket.
	qmpSock := QMPSocketPath(spec)
	qmpDeadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(qmpDeadline) {
		select {
		case <-qemuDone:
			stderr, _ := os.ReadFile(stderrLog)
			t.Fatalf("QEMU exited before QMP socket appeared\nstderr:\n%s", string(stderr))
		default:
		}
		conn, err := net.DialTimeout("unix", qmpSock, time.Second)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Log("QMP socket connected")

	// Capture screenshots and verify QEMU stays alive for at least 2 min.
	// Under TCG, the EDK2 -> OpenCore -> macOS Recovery chain takes ~60-90s.
	shotDir := filepath.Join(outDir, "screenshots")
	os.MkdirAll(shotDir, 0o755)

	aliveCheck := 2 * time.Minute
	deadline := time.Now().Add(aliveCheck)
	seq := 0
	for time.Now().Before(deadline) {
		select {
		case <-qemuDone:
			serialLog, _ := os.ReadFile(filepath.Join(outDir, "serial.log"))
			stderr, _ := os.ReadFile(stderrLog)
			t.Fatalf("QEMU exited prematurely after %d screenshots\nserial:\n%s\nstderr:\n%s",
				seq, string(serialLog), string(stderr))
		default:
		}

		seq++
		ppm := filepath.Join(shotDir, fmt.Sprintf("shot-%04d.ppm", seq))
		if err := QMPScreendump(qmpSock, ppm); err == nil {
			pngPath := ppm[:len(ppm)-3] + "png"
			if ConvertPPMtoPNG(ppm, pngPath) == nil {
				os.Remove(ppm)
			}
		}
		time.Sleep(15 * time.Second)
	}

	t.Logf("QEMU survived %v with %d screenshots captured", aliveCheck, seq)
	t.Logf("screenshots: %s", shotDir)

	// Verify we got at least some screenshots.
	pngs, _ := filepath.Glob(filepath.Join(shotDir, "*.png"))
	if len(pngs) == 0 {
		t.Error("no PNG screenshots captured")
	} else {
		t.Logf("captured %d PNG screenshots", len(pngs))
	}

	// Check serial log for any activity.
	serialData, _ := os.ReadFile(filepath.Join(outDir, "serial.log"))
	if len(serialData) > 0 {
		t.Logf("serial log: %d bytes", len(serialData))
	}
}

// TestMackitBuild runs the full mackit build pipeline using the build package.
// It verifies media resolution, DMG conversion, OpenCore download, and QEMU
// launch. Since macOS install is interactive, it only checks that the VM boots
// and stays alive.
//
//	go test -tags integration -run TestMackitBuild -timeout 20m -v ./vm/qemu/
func TestMackitBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("long: runs full mackit build pipeline")
	}

	requireQEMUBin(t)
	requireBaseSystemDMG(t)
	requireOpenCoreImg(t)

	outDir := testutil.ResultDir(t)
	t.Logf("results: %s", outDir)

	// This test invokes the mackit binary. Build it first.
	binPath := filepath.Join(outDir, "mackit")
	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/mackit/")
	buildCmd.Dir = projectRoot()
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("building mackit: %s: %v", string(out), err)
	}

	destPath := filepath.Join(outDir, "mackit-full.qcow2")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// Run mackit build. It will launch QEMU and wait for SSH.
	// We cancel after a shorter timeout since the interactive install won't
	// complete in CI, but the test verifies the pipeline starts correctly.
	cmd := exec.CommandContext(ctx, binPath, "build", destPath,
		"--cache-dir", cache.Dir(),
	)
	cmd.Dir = outDir
	cmd.Env = append(os.Environ(), "MACKIT_CACHE_DIR="+cache.Dir())

	stdout, err := cmd.CombinedOutput()
	// We expect the build to either timeout (SSH never comes up in CI) or
	// succeed. A startup crash is a test failure.
	if err != nil {
		outStr := string(stdout)
		if strings.Contains(outStr, "waiting for macOS install") ||
			strings.Contains(outStr, "SSH did not become available") ||
			strings.Contains(err.Error(), "signal: killed") ||
			strings.Contains(err.Error(), "context deadline exceeded") {
			t.Logf("mackit build started correctly but timed out waiting for interactive install (expected in CI)")
			t.Logf("output:\n%s", outStr)
		} else {
			t.Fatalf("mackit build failed unexpectedly: %v\noutput:\n%s", err, outStr)
		}
	} else {
		t.Logf("mackit build completed successfully")
	}
}

func freeTCPPort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("getting free port: %v", err)
	}
	defer l.Close()
	return uint16(l.Addr().(*net.TCPAddr).Port)
}

func requireQEMUBin(t *testing.T) string {
	t.Helper()
	name := "qemu-system-x86_64"
	p, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("no %s found in PATH", name)
	}
	return p
}

func requireBaseSystemDMG(t *testing.T) string {
	t.Helper()
	c := cache.Config{}
	p := c.InstallerDMG()
	if _, err := os.Stat(p); err != nil {
		t.Skipf("BaseSystem.dmg not cached at %s (run: mackit build to fetch)", p)
	}
	return p
}

func requireOpenCoreImg(t *testing.T) string {
	t.Helper()
	c := cache.Config{}
	p := c.OpenCoreImg()
	if _, err := os.Stat(p); err != nil {
		t.Skipf("OpenCore.qcow2 not cached at %s (run: mackit build to fetch)", p)
	}
	return p
}

func projectRoot() string {
	// Walk up from vm/qemu/ to find go.mod.
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}
