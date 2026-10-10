package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mackit "github.com/devcell-sh/go-mackit"
	"github.com/devcell-sh/go-mackit/build"
	"github.com/devcell-sh/go-mackit/build/buildopts"
	"github.com/devcell-sh/go-mackit/cache"
	"github.com/devcell-sh/go-mackit/installer/flow"
	"github.com/devcell-sh/go-mackit/internal/config"
	"github.com/devcell-sh/go-mackit/internal/testutil"
	"github.com/devcell-sh/go-mackit/vm/qemu"
)

func loadExampleOpts(t *testing.T, name string) (string, *buildopts.BuildOpts) {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "examples", name))
	require.NoError(t, err)
	cfg, err := config.Load(filepath.Join(dir, "mackit.yaml"))
	require.NoError(t, err, "example config must load")
	require.NoError(t, cfg.Validate(), "example config must validate")
	opts, err := cfg.ToBuildOpts()
	require.NoError(t, err)
	return dir, opts
}

func noTeardown() bool {
	return os.Getenv("MACKIT_NO_TEARDOWN") == "1"
}

// TestExampleRecovery_Build builds the recovery example: fetches
// BaseSystem.dmg from Apple catalog, downloads OpenCore, and boots
// QEMU into macOS Recovery.
//
//	MACKIT_E2E=1 go test -run TestExampleRecovery_Build -timeout 30m -v ./test/e2e/
//	MACKIT_E2E=1 MACKIT_NO_TEARDOWN=1 go test -run TestExampleRecovery_Build -timeout 30m -v ./test/e2e/
func TestExampleRecovery_Build(t *testing.T) {
	skipUnlessE2E(t)

	_, opts := loadExampleOpts(t, "recovery")
	require.True(t, opts.Recovery, "recovery example must select recovery mode")

	resultDir := testutil.ResultDir(t)
	t.Logf("results: %s", resultDir)

	dest := filepath.Join(resultDir, "mackit-recovery.qcow2")
	workDir := filepath.Join(resultDir, "work")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	logger := testLogger(t, resultDir)

	err := build.Recovery(ctx, build.Config{
		Dest:     dest,
		CacheDir: cache.Dir(),
		WorkDir:  workDir,
		Opts:     opts,
		Logger:   logger,
	})
	require.NoError(t, err, "recovery example must build")

	shotDir := filepath.Join(workDir, "screenshots")
	pngs, _ := filepath.Glob(filepath.Join(shotDir, "*.png"))
	t.Logf("captured %d screenshots in %s", len(pngs), shotDir)
	require.Greater(t, len(pngs), 0, "at least one screenshot must be captured")

	info, statErr := os.Stat(dest)
	require.NoError(t, statErr, "recovery image must exist")
	t.Logf("recovery image: %s (%.1f MB)", dest, float64(info.Size())/(1024*1024))

	requireLogged(t, resultDir, "macOS Recovery reached")
}

// TestRecoveryFlow_OpenTerminal boots the recovery example and drives the
// Recovery UI with OCR: it opens Terminal from the Utilities menu and
// expects the Terminal screen to be recognized.
//
//	MACKIT_E2E=1 go test -run TestRecoveryFlow_OpenTerminal -timeout 50m -v ./test/e2e/
func TestRecoveryFlow_OpenTerminal(t *testing.T) {
	skipUnlessE2E(t)

	_, opts := loadExampleOpts(t, "recovery")
	resultDir := testutil.ResultDir(t)
	t.Logf("results: %s", resultDir)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	err := build.Recovery(ctx, build.Config{
		Dest:          filepath.Join(resultDir, "mackit-recovery.qcow2"),
		CacheDir:      cache.Dir(),
		WorkDir:       filepath.Join(resultDir, "work"),
		Opts:          opts,
		Logger:        testLogger(t, resultDir),
		RecoverySteps: flow.OpenTerminal,
	})
	require.NoError(t, err, "recovery build with terminal flow")

	requireLogged(t, resultDir, "macOS Recovery reached")
	requireLogged(t, resultDir, `"screen":"recovery.terminal"`)
}

// TestRecoveryFlow_Reinstall boots the recovery example and clicks through
// "Reinstall macOS" by element selectors (text:Reinstall macOS,
// button:Continue, button:Agree) up to the target-disk screen.
//
//	MACKIT_E2E=1 go test -run TestRecoveryFlow_Reinstall -timeout 50m -v ./test/e2e/
func TestRecoveryFlow_Reinstall(t *testing.T) {
	skipUnlessE2E(t)

	_, opts := loadExampleOpts(t, "recovery")
	resultDir := testutil.ResultDir(t)
	t.Logf("results: %s", resultDir)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	err := build.Recovery(ctx, build.Config{
		Dest:          filepath.Join(resultDir, "mackit-recovery.qcow2"),
		CacheDir:      cache.Dir(),
		WorkDir:       filepath.Join(resultDir, "work"),
		Opts:          opts,
		Logger:        testLogger(t, resultDir),
		RecoverySteps: flow.CatalinaReinstall,
	})
	require.NoError(t, err, "recovery build with reinstall flow")

	requireLogged(t, resultDir, "macOS Recovery reached")
	requireLogged(t, resultDir, `"step":"target disk"`)
}

// requireLogged asserts that build.jsonl in resultDir contains needle.
func requireLogged(t *testing.T, resultDir, needle string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(resultDir, "build.jsonl"))
	require.NoError(t, err)
	require.Contains(t, string(data), needle)
}

// TestExamplePlain_Build builds the plain example: full macOS install.
// Since macOS install is interactive (user must complete via VNC), this
// test verifies the pipeline up to QEMU launch and screenshot capture.
//
//	MACKIT_E2E=1 go test -run TestExamplePlain_Build -timeout 30m -v ./test/e2e/
func TestExamplePlain_Build(t *testing.T) {
	skipUnlessE2E(t)

	_, opts := loadExampleOpts(t, "plain")

	resultDir := testutil.ResultDir(t)
	t.Logf("results: %s", resultDir)

	dest := filepath.Join(resultDir, "mackit-full.qcow2")
	workDir := filepath.Join(resultDir, "work")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	logger := testLogger(t, resultDir)

	err := build.Full(ctx, build.Config{
		Dest:     dest,
		CacheDir: cache.Dir(),
		WorkDir:  workDir,
		Opts:     opts,
		Logger:   logger,
	})
	// Full builds time out waiting for interactive install. Expected.
	if err != nil {
		t.Logf("build returned (expected timeout): %v", err)
	}

	shotDir := filepath.Join(workDir, "screenshots")
	pngs, _ := filepath.Glob(filepath.Join(shotDir, "*.png"))
	t.Logf("captured %d screenshots in %s", len(pngs), shotDir)
	require.Greater(t, len(pngs), 0, "at least one screenshot must be captured")
}

// TestExamplePlain_Start boots a previously built image with mackit.Start
// and verifies the VM comes up. Requires MACKIT_IMAGE env var.
//
//	MACKIT_E2E=1 MACKIT_IMAGE=./mackit-full.qcow2 go test -run TestExamplePlain_Start -timeout 30m -v ./test/e2e/
func TestExamplePlain_Start(t *testing.T) {
	skipUnlessE2E(t)

	imagePath := os.Getenv("MACKIT_IMAGE")
	if imagePath == "" {
		t.Skip("set MACKIT_IMAGE to a built qcow2 to run this test")
	}
	if _, err := os.Stat(imagePath); err != nil {
		t.Skipf("image not found: %s", imagePath)
	}

	resultDir := testutil.ResultDir(t)
	t.Logf("results: %s", resultDir)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	sshPort := qemu.FreePort()
	vncPort := qemu.FreePort()

	machine, err := mackit.Start(ctx, mackit.StartOpts{
		Image:    imagePath,
		Name:     "e2e-start",
		StateDir: filepath.Join(resultDir, "state"),
		SSHPort:  sshPort,
		VNCPort:  vncPort,
	})
	require.NoError(t, err, "image must boot via mackit.Start")
	if !noTeardown() {
		defer machine.Stop()
	} else {
		t.Logf("MACKIT_NO_TEARDOWN=1: VM will keep running after test (PID %d)", machine.PID())
	}

	t.Logf("VM started: PID=%d SSH=127.0.0.1:%d VNC=127.0.0.1:%d",
		machine.PID(), sshPort, vncPort)

	timer := time.NewTimer(2 * time.Minute)
	select {
	case <-timer.C:
		t.Log("VM survived 2 minutes")
	case <-machine.Done():
		t.Log("VM exited during wait")
	case <-ctx.Done():
		t.Log("context expired")
	}

	sshAddr := fmt.Sprintf("127.0.0.1:%d", sshPort)
	if checkSSHBanner(sshAddr) {
		t.Logf("SSH banner detected at %s", sshAddr)
	} else {
		t.Logf("SSH not yet available at %s (expected for fresh installs)", sshAddr)
	}
}

func skipUnlessE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("MACKIT_E2E") != "1" {
		t.Skip("opt in with MACKIT_E2E=1 (needs QEMU, network, ~30min)")
	}
}

func checkSSHBanner(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		return false
	}
	return strings.HasPrefix(string(buf[:n]), "SSH-")
}

type tlogHandler struct {
	t     *testing.T
	file  *os.File
	attrs []slog.Attr
}

func (h *tlogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *tlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &tlogHandler{t: h.t, file: h.file, attrs: append(h.attrs[:len(h.attrs):len(h.attrs)], attrs...)}
}
func (h *tlogHandler) WithGroup(string) slog.Handler { return h }
func (h *tlogHandler) Handle(_ context.Context, r slog.Record) error {
	msg := r.Message
	attrs := make(map[string]string)
	r.Attrs(func(a slog.Attr) bool { attrs[a.Key] = a.Value.String(); return true })
	for _, a := range h.attrs {
		attrs[a.Key] = a.Value.String()
	}
	line := msg
	for k, v := range attrs {
		line += " " + k + "=" + v
	}
	h.t.Log(line)
	if h.file != nil {
		entry := map[string]string{
			"ts":  r.Time.UTC().Format(time.RFC3339Nano),
			"msg": msg,
		}
		for k, v := range attrs {
			entry[k] = v
		}
		if b, err := json.Marshal(entry); err == nil {
			h.file.Write(append(b, '\n'))
		}
	}
	return nil
}

func testLogger(t *testing.T, resultDir string) *slog.Logger {
	var f *os.File
	if resultDir != "" {
		var err error
		f, err = os.OpenFile(filepath.Join(resultDir, "build.jsonl"),
			os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			t.Cleanup(func() { f.Close() })
		}
	}
	return slog.New(&tlogHandler{t: t, file: f})
}
