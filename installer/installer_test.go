package installer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareInstall_FileNotFound(t *testing.T) {
	err := PrepareInstall("/nonexistent/BaseSystem.dmg")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestPrepareInstall_IsDirectory(t *testing.T) {
	err := PrepareInstall(t.TempDir())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "directory")
}

func TestPrepareInstall_TooSmall(t *testing.T) {
	f := filepath.Join(t.TempDir(), "tiny.dmg")
	require.NoError(t, os.WriteFile(f, []byte("small"), 0o644))
	err := PrepareInstall(f)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too small")
}

func TestPrepareInstall_ValidFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "base.dmg")
	data := make([]byte, 2*1024*1024)
	require.NoError(t, os.WriteFile(f, data, 0o644))
	err := PrepareInstall(f)
	assert.NoError(t, err)
}

func TestEnsureOpenCore_VersionedCachedFile(t *testing.T) {
	dir := t.TempDir()
	ocPath := filepath.Join(dir, "OpenCore-v21.qcow2")
	require.NoError(t, os.WriteFile(ocPath, []byte("opencore-v21"), 0o644))

	result, err := EnsureOpenCore(t.Context(), OpenCoreConfig{
		CacheDir: dir,
		Version:  "v21",
	})
	assert.NoError(t, err)
	assert.Equal(t, ocPath, result)
}

func TestEnsureOpenCore_CachedFile(t *testing.T) {
	dir := t.TempDir()
	ocPath := filepath.Join(dir, "OpenCore.qcow2")
	require.NoError(t, os.WriteFile(ocPath, []byte("opencore"), 0o644))

	result, err := EnsureOpenCore(t.Context(), OpenCoreConfig{
		CacheDir: dir,
	})
	assert.NoError(t, err)
	assert.Equal(t, ocPath, result)
}

func TestEnsureOpenCore_ArchSpecificFile(t *testing.T) {
	dir := t.TempDir()
	ocPath := filepath.Join(dir, "OpenCore-arm64.qcow2")
	require.NoError(t, os.WriteFile(ocPath, []byte("opencore-arm64"), 0o644))

	result, err := EnsureOpenCore(t.Context(), OpenCoreConfig{
		CacheDir: dir,
		Arch:     "arm64",
	})
	assert.NoError(t, err)
	assert.Equal(t, ocPath, result)
}
