package vz

import (
	"context"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/devcell-sh/go-mackit/vm"
)

func helperPath(t *testing.T) string {
	t.Helper()
	// Build the testhelper binary once per test run.
	bin := filepath.Join(t.TempDir(), "testhelper")
	cmd := exec.Command("go", "build", "-o", bin, "./testhelper/")
	cmd.Dir = filepath.Join(".")
	// Need to find the module root for go build
	modRoot, err := findModRoot()
	require.NoError(t, err)
	cmd.Dir = filepath.Join(modRoot, "vm", "vz")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "building testhelper: %s", out)
	return bin
}

func findModRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

func TestHelperInstall(t *testing.T) {
	bin := helperPath(t)
	backend := &HelperBackend{HelperPath: bin}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	handle, err := backend.StartInstall(ctx, vm.VMInstallConfig{
		InstallerDMG: "latest",
		DiskPath:     "/tmp/test-disk.raw",
		DiskSizeGB:   64,
		CPUs:         4,
		MemoryGB:     8,
		OutputDir:    t.TempDir(),
	})
	require.NoError(t, err)
	defer handle.Stop()

	select {
	case <-handle.Done():
		require.NoError(t, handle.Wait())
	case <-ctx.Done():
		t.Fatal("timeout waiting for install")
	}
}

func TestHelperBoot(t *testing.T) {
	bin := helperPath(t)
	backend := &HelperBackend{HelperPath: bin}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	handle, err := backend.StartRun(ctx, vm.VMRunConfig{
		DiskPath:  "/tmp/test-disk.raw",
		CPUs:      4,
		MemoryGB:  8,
		OutputDir: t.TempDir(),
	})
	require.NoError(t, err)
	defer handle.Stop()

	assert.Equal(t, 15900, GetVNCPort(handle))
}

func TestHelperScreenshot(t *testing.T) {
	bin := helperPath(t)
	backend := &HelperBackend{HelperPath: bin}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	handle, err := backend.StartRun(ctx, vm.VMRunConfig{
		DiskPath:  "/tmp/test-disk.raw",
		CPUs:      4,
		MemoryGB:  8,
		OutputDir: t.TempDir(),
	})
	require.NoError(t, err)
	defer handle.Stop()

	hh := handle.(*helperHandle)
	img, err := hh.Screenshot(ctx)
	require.NoError(t, err)
	require.NotNil(t, img)
	bounds := img.Bounds()
	assert.Equal(t, 320, bounds.Dx())
	assert.Equal(t, 240, bounds.Dy())
}

func TestHelperInput(t *testing.T) {
	bin := helperPath(t)
	backend := &HelperBackend{HelperPath: bin}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	handle, err := backend.StartRun(ctx, vm.VMRunConfig{
		DiskPath:  "/tmp/test-disk.raw",
		CPUs:      4,
		MemoryGB:  8,
		OutputDir: t.TempDir(),
	})
	require.NoError(t, err)
	defer handle.Stop()

	hh := handle.(*helperHandle)
	require.NoError(t, hh.SendKeys(ctx, "ret"))
	require.NoError(t, hh.Click(ctx, image.Pt(100, 200), image.Rect(0, 0, 1920, 1080)))
	require.NoError(t, hh.Type(ctx, "hello world"))
}
